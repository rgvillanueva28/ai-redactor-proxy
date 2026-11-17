package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
)

// geminiRequestFields are the request leaves that carry text in the Gemini
// generateContent dialect:
//
//	{"contents":[{"parts":[{"text":"..."}]}],
//	 "systemInstruction":{"parts":[{"text":"..."}]}}
var geminiRequestFields = newFieldSet(
	[]string{"contents", "parts", "text"},
	[]string{"systemInstruction", "parts", "text"},
)

// geminiResponseFields are the response leaves: candidates[].content.parts[].text.
var geminiResponseFields = newFieldSet(
	[]string{"candidates", "content", "parts", "text"},
)

// GeminiHandler redacts Google Gemini generateContent requests and unredacts
// the matching responses.
//
// Gemini differs from OpenAI and Anthropic in two ways that the handler has to
// respect: text always lives inside a "parts" array element (mixed with
// inlineData/functionCall parts that must not change), and the classic
// streaming endpoint returns newline-delimited JSON objects rather than SSE
// frames (the SSE-flavoured streaming endpoint still needs SSE unwrapping).
//
// A GeminiHandler is stateless and safe for concurrent use.
type GeminiHandler struct {
	redactor    *pii.Redactor
	unredactor  *pii.Unredactor
	logRedacted bool
	logBodies   bool
}

// Compile-time assertions that GeminiHandler satisfies the proxy contracts.
var (
	_ proxy.RequestMutator  = (*GeminiHandler)(nil)
	_ proxy.ResponseMutator = (*GeminiHandler)(nil)
)

// NewGeminiHandler returns a handler that redacts with r and unredacts with u.
// A nil redactor or unredactor disables that direction of the transformation.
// When logRedacted is true every redacted item is logged at info level. When
// logBodies is true every request, response and stream chunk body is logged at
// debug level, before and after the transformation.
func NewGeminiHandler(r *pii.Redactor, u *pii.Unredactor, logRedacted bool, logBodies bool) *GeminiHandler {
	return &GeminiHandler{redactor: r, unredactor: u, logRedacted: logRedacted, logBodies: logBodies}
}

// MutateRequest redacts contents[].parts[].text and
// systemInstruction.parts[].text, and publishes the per-request mapping on the
// request context.
func (h *GeminiHandler) MutateRequest(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
	if h == nil {
		return body, nil
	}
	out, err := redactBody(req, body, h.redactor, geminiRequestFields, h.logRedacted)
	if err != nil {
		return nil, err
	}
	logBody(h.logBodies, "request body (original)", body)
	logBody(h.logBodies, "request body (redacted)", out)
	return out, nil
}

// MutateResponse restores placeholders in candidates[].content.parts[].text.
func (h *GeminiHandler) MutateResponse(ctx context.Context, resp *http.Response, body []byte) ([]byte, error) {
	if h == nil {
		return body, nil
	}
	out, err := unredactBody(ctx, resp, body, h.unredactor, geminiResponseFields)
	if err != nil {
		return nil, err
	}
	logBody(h.logBodies, "response body (redacted)", body)
	logBody(h.logBodies, "response body (unredacted)", out)
	return out, nil
}

// MutateStreamChunk restores placeholders in Gemini's streaming response.
//
// Gemini has two streaming shapes. Its classic endpoint streams
// newline-delimited JSON: each line is a complete GenerateContentResponse and
// the Content-Type is application/json rather than text/event-stream. A chunk
// from that endpoint that is a single complete JSON document is walked as a
// buffered body; a chunk with several lines is walked line by line. Lines that
// are blank or not JSON are copied through with their original framing.
//
// The newer SSE-flavoured streaming endpoint sends data: frames, which the
// proxy hands over as one serialised event per call. Those need the same
// SSE-aware unwrapping OpenAI and Anthropic use; treating the framing as JSON
// would make the chunk look invalid and leak placeholders to the client.
//
// When logBodies is enabled every chunk is logged as it passes through, before
// and after unredaction.
func (h *GeminiHandler) MutateStreamChunk(ctx context.Context, chunk []byte, contentType string) ([]byte, error) {
	if h == nil {
		return chunk, nil
	}
	out, err := h.unredactStreamChunk(ctx, chunk, contentType)
	if err != nil {
		return nil, err
	}
	logBody(h.logBodies, "stream chunk (redacted)", chunk)
	logBody(h.logBodies, "stream chunk (unredacted)", out)
	return out, nil
}

// unredactStreamChunk applies the Gemini streaming unredaction rules and
// returns chunk unchanged when there was nothing to restore.
func (h *GeminiHandler) unredactStreamChunk(ctx context.Context, chunk []byte, contentType string) ([]byte, error) {
	if len(chunk) == 0 || h.unredactor == nil {
		return chunk, nil
	}
	m := mappingFromContext(ctx)
	if m == nil {
		return chunk, nil
	}

	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream") {
		return unredactSSEEvent(ctx, chunk, h.unredactor, geminiResponseFields)
	}

	state := streamStateFromContext(ctx)

	// Whole-chunk JSON is the common case for a single streamed line (and for
	// callers that hand us the entire body at once).
	if json.Valid(bytes.TrimSpace(chunk)) {
		return unredactFieldsWithState(chunk, m, h.unredactor, geminiResponseFields, state)
	}

	var out bytes.Buffer
	out.Grow(len(chunk))
	changed := false

	// SplitAfter keeps each segment's own "\n" so framing survives untouched.
	for _, segment := range bytes.SplitAfter(chunk, []byte("\n")) {
		if len(segment) == 0 {
			continue
		}

		line, ending := segment, []byte(nil)
		if bytes.HasSuffix(line, []byte("\n")) {
			line, ending = line[:len(line)-1], []byte("\n")
		}
		if bytes.HasSuffix(line, []byte("\r")) {
			line = line[:len(line)-1]
			ending = append([]byte("\r"), ending...)
		}

		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || !json.Valid(trimmed) {
			out.Write(segment)
			continue
		}

		walked, err := unredactFieldsWithState(trimmed, m, h.unredactor, geminiResponseFields, state)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(walked, trimmed) {
			out.Write(segment)
			continue
		}
		out.Write(walked)
		out.Write(ending)
		changed = true
	}

	if !changed {
		return chunk, nil
	}
	return out.Bytes(), nil
}
