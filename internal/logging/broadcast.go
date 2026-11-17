package logging

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
)

// DefaultBufferSize is the number of log entries queued for each SSE client
// before new entries start being dropped for that client.
const DefaultBufferSize = 256

// Broadcaster fans JSON log lines out to any number of subscribers.
//
// Delivery is best-effort and never blocks the caller: each subscriber owns a
// buffered channel and Broadcast uses a non-blocking send, so a slow or stalled
// client only loses its own events. This keeps the proxy's request path
// independent of how many log viewers are connected.
//
// A Broadcaster is safe for concurrent use. Its zero value is usable but queues
// entries with DefaultBufferSize; construct one with NewBroadcaster to choose a
// different depth.
type Broadcaster struct {
	buffer int

	mu   sync.RWMutex
	subs map[*Subscriber]struct{}
}

// Subscriber is one connected log consumer. Read entries from C and release the
// subscription with Close when done.
type Subscriber struct {
	b  *Broadcaster
	ch chan []byte

	dropped atomic.Uint64
}

// NewBroadcaster returns a Broadcaster whose subscribers each queue up to
// buffer entries. Non-positive values fall back to DefaultBufferSize.
func NewBroadcaster(buffer int) *Broadcaster {
	if buffer <= 0 {
		buffer = DefaultBufferSize
	}
	return &Broadcaster{
		buffer: buffer,
		subs:   make(map[*Subscriber]struct{}),
	}
}

// bufferSize reports the queue depth used for new subscribers.
func (b *Broadcaster) bufferSize() int {
	if b.buffer <= 0 {
		return DefaultBufferSize
	}
	return b.buffer
}

// Subscribe registers a new subscriber. The caller must call Close (or
// Unsubscribe) when it is finished, otherwise the subscription leaks.
func (b *Broadcaster) Subscribe() *Subscriber {
	s := &Subscriber{
		b:  b,
		ch: make(chan []byte, b.bufferSize()),
	}
	b.mu.Lock()
	if b.subs == nil {
		b.subs = make(map[*Subscriber]struct{})
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Unsubscribe detaches s and closes its channel, waking any goroutine blocked
// reading from it. It is safe to call repeatedly and with a nil subscriber.
func (b *Broadcaster) Unsubscribe(s *Subscriber) {
	if b == nil || s == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s]; !ok {
		return
	}
	delete(b.subs, s)
	close(s.ch)
}

// Broadcast delivers line to every current subscriber without blocking.
//
// Subscribers whose queue is full skip this entry and have their Dropped
// counter incremented; the entry still reaches everyone else. line is shared by
// all subscribers and must not be modified after the call.
func (b *Broadcaster) Broadcast(line []byte) {
	if b == nil || len(line) == 0 {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		select {
		case s.ch <- line:
		default:
			s.dropped.Add(1)
		}
	}
}

// Subscribers reports how many clients are currently subscribed. It is mainly
// useful for tests and diagnostics.
func (b *Broadcaster) Subscribers() int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// C returns the channel log lines are delivered on. The channel is closed when
// the subscriber is unsubscribed.
func (s *Subscriber) C() <-chan []byte {
	if s == nil {
		return nil
	}
	return s.ch
}

// Dropped reports how many entries were discarded for this subscriber because
// it could not keep up.
func (s *Subscriber) Dropped() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

// Close detaches the subscriber from its broadcaster. It is safe to call more
// than once.
func (s *Subscriber) Close() {
	if s == nil || s.b == nil {
		return
	}
	s.b.Unsubscribe(s)
}

// SSEHandler returns an http.Handler that streams every broadcast line to the
// client as a Server-Sent Event. Each event is written as "data: <json>\n\n".
func (b *Broadcaster) SSEHandler() http.Handler {
	return http.HandlerFunc(b.serveSSE)
}

// BroadcastHandler is a slog.Handler that writes JSON log records to an output
// writer (normally stderr) and broadcasts each record to a Broadcaster.
//
// Records are encoded once with slog's JSON handler, so terminal output and SSE
// events are byte-identical. Level filtering happens in Enabled, which means
// the stream only carries records the logger would have emitted anyway.
type BroadcastHandler struct {
	out   io.Writer
	level slog.Leveler
	bcast *Broadcaster
	ops   []handlerOp

	writeMu *sync.Mutex
}

// handlerOp records a WithAttrs or WithGroup call so derived handlers can
// reproduce the attribute nesting when encoding a record.
type handlerOp struct {
	group string
	attrs []slog.Attr
}

// NewBroadcastHandler returns a handler writing JSON to out and broadcasting to
// b. Either out or b may be nil, in which case that side is skipped. A nil
// level means slog.LevelInfo.
func NewBroadcastHandler(out io.Writer, level slog.Leveler, b *Broadcaster) *BroadcastHandler {
	return &BroadcastHandler{
		out:     out,
		level:   level,
		bcast:   b,
		writeMu: &sync.Mutex{},
	}
}

// Enabled reports whether records at lvl should be emitted.
func (h *BroadcastHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= h.minLevel()
}

// minLevel resolves the configured threshold.
func (h *BroadcastHandler) minLevel() slog.Level {
	if h.level == nil {
		return slog.LevelInfo
	}
	return h.level.Level()
}

// Handle encodes r as one JSON line, writes it to the output writer and
// broadcasts it. A failure writing to the output writer is returned to slog
// (which ignores it) but never prevents the broadcast.
func (h *BroadcastHandler) Handle(ctx context.Context, r slog.Record) error {
	var buf writeBuffer
	ih := h.encoder(&buf)
	if err := ih.Handle(ctx, r); err != nil {
		return err
	}
	data := buf.b

	var writeErr error
	if h.out != nil && len(data) > 0 {
		h.writeMu.Lock()
		_, writeErr = h.out.Write(data)
		if writeErr == nil && data[len(data)-1] != '\n' {
			_, writeErr = h.out.Write([]byte("\n"))
		}
		h.writeMu.Unlock()
	}

	if h.bcast != nil {
		if line := trimLineEnd(data); len(line) > 0 {
			// Copy: the encoding buffer is reused per call and subscribers may
			// hold on to the slice.
			h.bcast.Broadcast(append([]byte(nil), line...))
		}
	}
	return writeErr
}

// WithAttrs returns a handler that adds attrs to every subsequent record.
func (h *BroadcastHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	nh := h.clone()
	nh.ops = append(nh.ops, handlerOp{attrs: attrs})
	return nh
}

// WithGroup returns a handler that namespaces subsequent attributes.
func (h *BroadcastHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nh := h.clone()
	nh.ops = append(nh.ops, handlerOp{group: name})
	return nh
}

// clone copies the handler with a fresh operations slice so derived handlers
// never share backing storage. The write mutex is intentionally shared.
func (h *BroadcastHandler) clone() *BroadcastHandler {
	nh := *h
	// Cap the inherited slice so the next append copies instead of writing
	// into storage another derived handler may still be using.
	nh.ops = h.ops[:len(h.ops):len(h.ops)]
	return &nh
}

// encoder builds a JSON handler over w that replays the recorded WithAttrs and
// WithGroup calls.
func (h *BroadcastHandler) encoder(w io.Writer) slog.Handler {
	var opts *slog.HandlerOptions
	if h.level != nil {
		opts = &slog.HandlerOptions{Level: h.level}
	}
	var ih slog.Handler = slog.NewJSONHandler(w, opts)
	for _, op := range h.ops {
		if op.group != "" {
			ih = ih.WithGroup(op.group)
			continue
		}
		ih = ih.WithAttrs(op.attrs)
	}
	return ih
}

// writeBuffer is a minimal io.Writer accumulating one encoded record.
type writeBuffer struct {
	b []byte
}

func (w *writeBuffer) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

// trimLineEnd strips trailing newlines from a JSON record so it can be wrapped
// in a single SSE data line.
func trimLineEnd(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// serveSSE streams broadcast entries to one HTTP client until it disconnects.
func (b *Broadcaster) serveSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Subscribe before writing headers so no log line emitted between the
	// client seeing the response and the subscription is lost.
	sub := b.Subscribe()
	defer sub.Close()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Disable buffering in reverse proxies so events arrive immediately.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, open := <-sub.C():
			if !open {
				return
			}
			if err := writeSSELine(w, line); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSELine writes line as a single SSE event.
func writeSSELine(w io.Writer, line []byte) error {
	frame := make([]byte, 0, len(line)+8)
	frame = append(frame, "data: "...)
	frame = append(frame, line...)
	frame = append(frame, '\n', '\n')
	_, err := w.Write(frame)
	return err
}
