package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/local/redactor/internal/pii"
)

// sseDoneSentinel is the marker OpenAI (and compatible gateways) send as the
// final SSE data line. It is not JSON, so it is always passed through.
const sseDoneSentinel = "[DONE]"

// redactBody redacts the string leaves of body selected by fields and returns
// the rewritten JSON.
//
// Placeholder numbering restarts at 1 for every call to Redact, so each leaf's
// mapping is merged into one shared per-request mapping: otherwise two
// different addresses would both be "<EMAIL_1>" and unredaction would restore
// the wrong one. The mapping is attached to req's context with
// contextWithMapping, which is where MutateResponse and MutateStreamChunk look
// for it.
//
// Bodies that are empty, not JSON, or have no selected leaves are returned
// unchanged so non-JSON endpoints keep working.
func redactBody(req *http.Request, body []byte, r *pii.Redactor, fields fieldSet, logRedacted bool) ([]byte, error) {
	if len(body) == 0 || r == nil || len(fields) == 0 {
		return body, nil
	}
	if !json.Valid(body) {
		return body, nil
	}

	m := pii.GetMapping()
	changed := false

	out, err := walkFields(body, fields, func(s string) (string, error) {
		redacted, local := r.Redact(s)
		if local == nil {
			return redacted, nil
		}
		defer pii.PutMapping(local)

		merged := mergeMapping(redacted, local, m)
		if merged != s {
			changed = true
		}
		return merged, nil
	})
	if err != nil {
		pii.PutMapping(m)
		return nil, err
	}

	if req != nil {
		*req = *req.WithContext(contextWithMapping(req.Context(), m))
	}
	if logRedacted {
		logMapping(m)
	}
	if !changed {
		// Nothing was redacted: leave the body byte-for-byte intact rather than
		// re-serializing it.
		return body, nil
	}
	return out, nil
}

// logMapping emits one info-level structured log line per redacted item.
//
// Entries are sorted by placeholder first so the log order is deterministic
// regardless of map iteration order. It is only called when the log_redacted
// config option is enabled.
func logMapping(m *pii.Mapping) {
	entries := m.All()
	if len(entries) == 0 {
		return
	}
	placeholders := make([]string, 0, len(entries))
	for ph := range entries {
		placeholders = append(placeholders, ph)
	}
	sortPlaceholders(placeholders)

	for _, ph := range placeholders {
		slog.Info("redacted",
			"placeholder", ph,
			"category", string(categoryFromPlaceholder(ph)),
			"value", entries[ph])
	}
}

// logBody emits one info-level structured log line carrying body.
//
// It is only called when the log_bodies config option is enabled, so a disabled
// option costs nothing at all rather than a level check per body. Info lines are
// rendered at the default log level, so enabling log_bodies is enough to see
// them; no --log-level debug is required.
func logBody(enabled bool, msg string, body []byte) {
	if !enabled {
		return
	}
	slog.Info(msg, "body", string(body))
}

// unredactBody restores placeholders in the string leaves of body selected by
// fields.
//
// The mapping is looked up on ctx first and then on resp.Request's context,
// because the proxy hands response mutators the request's context rather than
// any value a request mutator attached. A missing mapping, an empty body, a
// non-JSON body, or a body with no selected leaves is passed through unchanged.
func unredactBody(ctx context.Context, resp *http.Response, body []byte, u *pii.Unredactor, fields fieldSet) ([]byte, error) {
	if len(body) == 0 || u == nil || len(fields) == 0 {
		return body, nil
	}
	m := mappingFromContext(ctx)
	if m == nil && resp != nil && resp.Request != nil {
		m = mappingFromContext(resp.Request.Context())
	}
	if m == nil {
		return body, nil
	}
	if !json.Valid(body) {
		return body, nil
	}
	return unredactFields(body, m, u, fields)
}

// unredactFields walks the selected leaves of one JSON document, returning body
// unchanged when no placeholder was found.
func unredactFields(body []byte, m *pii.Mapping, u *pii.Unredactor, fields fieldSet) ([]byte, error) {
	changed := false

	out, err := walkFields(body, fields, func(s string) (string, error) {
		restored := u.Unredact(s, m)
		if restored != s {
			changed = true
		}
		return restored, nil
	})
	if err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	return out, nil
}

// unredactSSEEvent restores placeholders in the JSON payload of one SSE event.
//
// Events that carry no data, keep-alives, the "[DONE]" sentinel and non-JSON
// payloads are passed through untouched rather than aborting a stream that is
// already in flight. Because rewriteSSEData only rebuilds an event whose data
// actually changed, payload-free events (Anthropic's message_start and
// message_delta, for instance) keep their exact framing.
func unredactSSEEvent(ctx context.Context, chunk []byte, u *pii.Unredactor, fields fieldSet) ([]byte, error) {
	if len(chunk) == 0 || u == nil || len(fields) == 0 {
		return chunk, nil
	}
	m := mappingFromContext(ctx)
	if m == nil {
		return chunk, nil
	}

	return rewriteSSEData(chunk, func(data string) (string, error) {
		trimmed := strings.TrimSpace(data)
		if trimmed == "" || trimmed == sseDoneSentinel || !json.Valid([]byte(trimmed)) {
			return data, nil
		}

		changed := false
		out, err := walkFields([]byte(data), fields, func(s string) (string, error) {
			restored := u.Unredact(s, m)
			if restored != s {
				changed = true
			}
			return restored, nil
		})
		if err != nil {
			return "", err
		}
		if !changed {
			return data, nil
		}
		return string(out), nil
	})
}

// rewriteSSEData applies fn to the concatenated data lines of one serialized
// SSE event and re-serializes the event when the data changed.
//
// The event name, id and any comment lines are preserved, which matters for
// Anthropic: its clients dispatch on "event: content_block_delta" and would
// lose the frame if the mutator emitted a bare data line. When fn returns the
// data unchanged, or the chunk carries no data at all, the original bytes are
// returned exactly as received.
func rewriteSSEData(chunk []byte, fn func(data string) (string, error)) ([]byte, error) {
	data, ok := ParseSSEData(chunk)
	if !ok {
		return chunk, nil
	}

	newData, err := fn(data)
	if err != nil {
		return nil, err
	}
	if newData == data {
		return chunk, nil
	}

	var buf bytes.Buffer
	buf.Grow(len(chunk) + len(newData))

	wroteData := false
	for _, line := range splitSSELines(chunk) {
		if isSSEDataLine(line) {
			if !wroteData {
				writeSSEDataLines(&buf, newData)
				wroteData = true
			}
			// Additional data lines belong to the payload that was just
			// written; the replacement already contains them.
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if !wroteData {
		writeSSEDataLines(&buf, newData)
	}
	// The blank line that terminates an SSE event.
	buf.WriteByte('\n')

	return buf.Bytes(), nil
}

// splitSSELines splits a serialized event into its lines without their
// terminators. Trailing newlines (the event terminator) are dropped and CRLF
// framing is tolerated.
func splitSSELines(chunk []byte) [][]byte {
	trimmed := bytes.TrimRight(chunk, "\n")
	if len(trimmed) == 0 {
		return nil
	}
	lines := bytes.Split(trimmed, []byte("\n"))
	for i, line := range lines {
		lines[i] = bytes.TrimSuffix(line, []byte("\r"))
	}
	return lines
}

// isSSEDataLine reports whether line is a data field (with or without the
// optional space after the colon).
func isSSEDataLine(line []byte) bool {
	return bytes.HasPrefix(line, []byte(sseDataField))
}

// writeSSEDataLines writes data as one "data: " line per embedded newline.
func writeSSEDataLines(buf *bytes.Buffer, data string) {
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
			return
		}
	}
}
