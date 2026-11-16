package handler

import (
	"bytes"
	"strings"
)

// sseDataField is the SSE field name that carries event payloads.
const sseDataField = "data:"

// ParseSSEData extracts the concatenation of all "data:" lines from a
// serialized SSE event (e.g. "event: message\ndata: {...}\ndata: more\n\n").
//
// Multiple data lines are joined with "\n" as the SSE specification requires; a
// single optional space after the colon is removed. Comment lines, "event:",
// "id:" and "retry:" fields are ignored. The second return value reports
// whether the event carried any data at all — an event with only comments, or
// an empty chunk, yields ("", false) so callers can pass it through untouched.
func ParseSSEData(chunk []byte) (string, bool) {
	if len(chunk) == 0 {
		return "", false
	}

	var b strings.Builder
	found := false
	rest := chunk
	for len(rest) > 0 {
		var line []byte
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			line, rest = rest, nil
		}
		// Tolerate CRLF framing.
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if !bytes.HasPrefix(line, []byte(sseDataField)) {
			continue
		}
		value := line[len(sseDataField):]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		if found {
			b.WriteByte('\n')
		}
		b.Write(value)
		found = true
	}

	if !found {
		return "", false
	}
	return b.String(), true
}

// FormatSSEData wraps data back into a minimal SSE event: "data: <data>\n\n".
// Multi-line data is split into multiple "data: " lines, matching what the
// SSE writer in package proxy produces.
func FormatSSEData(data string) []byte {
	var buf bytes.Buffer
	buf.Grow(len(data) + len(sseDataField) + 2)

	rest := data
	for {
		var line string
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			line, rest = rest, ""
		} else {
			line, rest = rest[:i], rest[i+1:]
		}
		buf.WriteString(sseDataField)
		buf.WriteByte(' ')
		buf.WriteString(line)
		buf.WriteByte('\n')
		if i < 0 {
			break
		}
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}
