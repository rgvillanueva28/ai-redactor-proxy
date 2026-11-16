package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
)

// SSEEvent is a single Server-Sent Events dispatch unit. Data holds the
// concatenated data lines without their trailing newline.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
}

// Field names as byte slices so parsing does not allocate while matching.
var (
	sseFieldData  = []byte("data")
	sseFieldEvent = []byte("event")
	sseFieldID    = []byte("id")
	sseFieldRetry = []byte("retry")
)

// sseLinePool recycles the scratch buffer each line is copied into. Lines are
// only valid until the next read, so pooling avoids one allocation per line.
var sseLinePool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 512)
		return &b
	},
}

// ErrNilSSECallback is returned by ParseEvents when cb is nil.
var ErrNilSSECallback = errors.New("proxy: nil SSE callback")

// ParseEvents reads r and invokes cb once per dispatched SSE event.
//
// Parsing follows the SSE wire format: lines are terminated by "\n" or
// "\r\n"; "data" lines accumulate and are joined with "\n"; "event" and "id"
// are captured; "retry" and comment lines (starting with ":") are ignored;
// unknown fields are ignored. A blank line dispatches the accumulated event,
// and a final event without a trailing blank line is flushed when the stream
// ends. "data: [DONE]" is returned as an ordinary event; interpreting it is
// up to cb.
//
// The callback runs synchronously; if it returns an error, ParseEvents stops
// and returns that error. The reader is never read ahead beyond what the
// parser needs.
func ParseEvents(r io.Reader, cb func(SSEEvent) error) error {
	if r == nil {
		return errors.New("proxy: nil SSE reader")
	}
	if cb == nil {
		return ErrNilSSECallback
	}

	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReaderSize(r, 32*1024)
	}

	lp := sseLinePool.Get().(*[]byte)
	line := (*lp)[:0]
	defer func() {
		*lp = line[:0]
		sseLinePool.Put(lp)
	}()

	// data is reused across events; it only grows to the largest event.
	data := make([]byte, 0, 256)
	var ev SSEEvent
	hasData := false

	dispatch := func() error {
		if !hasData {
			// Event/id without data never dispatches, but still resets.
			ev = SSEEvent{}
			return nil
		}
		ev.Data = string(data)
		err := cb(ev)
		ev = SSEEvent{}
		data = data[:0]
		hasData = false
		return err
	}

	for {
		var err error
		line, err = readSSELine(br, line)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return dispatch()
			}
			return err
		}

		if len(line) == 0 {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if line[0] == ':' {
			// Comment / keep-alive.
			continue
		}

		field, value := splitSSEField(line)
		switch {
		case bytes.Equal(field, sseFieldData):
			if hasData {
				data = append(data, '\n')
			}
			data = append(data, value...)
			hasData = true
		case bytes.Equal(field, sseFieldEvent):
			ev.Event = string(value)
		case bytes.Equal(field, sseFieldID):
			ev.ID = string(value)
		case bytes.Equal(field, sseFieldRetry):
			// Reconnection delay; deliberately ignored.
		default:
			// Unknown field, ignored per the SSE specification.
		}
	}
}

// readSSELine copies the next line into buf, stripping its newline and an
// optional carriage return. It returns io.EOF only when no bytes remain.
// Lines longer than the bufio buffer are reassembled transparently.
func readSSELine(br *bufio.Reader, buf []byte) ([]byte, error) {
	buf = buf[:0]
	for {
		frag, err := br.ReadSlice('\n')
		if len(frag) > 0 {
			buf = append(buf, frag...)
		}
		switch err {
		case nil:
			return trimSSELineEnd(buf), nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(buf) == 0 {
				return buf, io.EOF
			}
			return trimSSELineEnd(buf), nil
		default:
			return buf, err
		}
	}
}

// trimSSELineEnd removes a trailing "\n" and then a trailing "\r".
func trimSSELineEnd(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	return b
}

// splitSSEField splits a line at the first colon, stripping a single leading
// space from the value as the SSE specification requires.
func splitSSEField(line []byte) (field, value []byte) {
	i := bytes.IndexByte(line, ':')
	if i < 0 {
		return line, nil
	}
	field, value = line[:i], line[i+1:]
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return field, value
}

// WriteSSEEvent serialises ev in SSE wire format. Multi-line Data is written
// as one "data:" line per line. It returns the number of bytes written.
func WriteSSEEvent(w io.Writer, ev SSEEvent) (int64, error) {
	if w == nil {
		return 0, errors.New("proxy: nil SSE writer")
	}

	buf := make([]byte, 0, len(ev.Event)+len(ev.ID)+len(ev.Data)+32)
	if ev.Event != "" {
		buf = append(buf, "event: "...)
		buf = append(buf, ev.Event...)
		buf = append(buf, '\n')
	}
	if ev.ID != "" {
		buf = append(buf, "id: "...)
		buf = append(buf, ev.ID...)
		buf = append(buf, '\n')
	}

	rest := ev.Data
	for {
		var line string
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			line, rest = rest, ""
		} else {
			line, rest = rest[:i], rest[i+1:]
		}
		buf = append(buf, "data: "...)
		buf = append(buf, line...)
		buf = append(buf, '\n')
		if i < 0 {
			break
		}
	}
	buf = append(buf, '\n')

	n, err := w.Write(buf)
	if err == nil && n < len(buf) {
		err = io.ErrShortWrite
	}
	return int64(n), err
}
