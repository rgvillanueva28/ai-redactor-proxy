package handler

import (
	"context"
	"net/http"

	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
)

// openAIRequestFields are the request leaves that carry user/model text in the
// Chat Completions dialect:
//
//	{"messages":[{"content":"..."},                    // string content
//	             {"content":[{"type":"text","text":"..."}]}]}  // content parts
var openAIRequestFields = newFieldSet(
	[]string{"messages", "content"},
	[]string{"messages", "content", "text"},
)

// openAIResponseFields are the buffered Chat Completions response leaves:
// choices[].message.content.
var openAIResponseFields = newFieldSet(
	[]string{"choices", "message", "content"},
)

// openAIStreamFields are the streaming Chat Completions delta leaves:
// choices[].delta.content.
var openAIStreamFields = newFieldSet(
	[]string{"choices", "delta", "content"},
)

// OpenAIHandler redacts OpenAI Chat Completions requests and unredacts the
// matching responses.
//
// Unlike GenericHandler it walks only the message/content fields, so structure
// the upstream API relies on verbatim — tool definitions, image URLs, function
// names, response_format schemas — is never rewritten. Keeping the walked set
// narrow also means a placeholder can only ever appear where the model is
// expected to quote text back, so unredaction has a much smaller surface.
//
// An OpenAIHandler is stateless and safe for concurrent use.
type OpenAIHandler struct {
	redactor    *pii.Redactor
	unredactor  *pii.Unredactor
	logRedacted bool
}

// Compile-time assertions that OpenAIHandler satisfies the proxy contracts.
var (
	_ proxy.RequestMutator  = (*OpenAIHandler)(nil)
	_ proxy.ResponseMutator = (*OpenAIHandler)(nil)
)

// NewOpenAIHandler returns a handler that redacts with r and unredacts with u.
// A nil redactor or unredactor disables that direction of the transformation.
// When logRedacted is true every redacted item is logged at info level.
func NewOpenAIHandler(r *pii.Redactor, u *pii.Unredactor, logRedacted bool) *OpenAIHandler {
	return &OpenAIHandler{redactor: r, unredactor: u, logRedacted: logRedacted}
}

// MutateRequest redacts messages[].content (string or array of parts) and
// publishes the per-request mapping on the request context.
func (h *OpenAIHandler) MutateRequest(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
	if h == nil {
		return body, nil
	}
	return redactBody(req, body, h.redactor, openAIRequestFields, h.logRedacted)
}

// MutateResponse restores placeholders in choices[].message.content.
func (h *OpenAIHandler) MutateResponse(ctx context.Context, resp *http.Response, body []byte) ([]byte, error) {
	if h == nil {
		return body, nil
	}
	return unredactBody(ctx, resp, body, h.unredactor, openAIResponseFields)
}

// MutateStreamChunk restores placeholders in one SSE chunk's
// choices[].delta.content. Frames without content (role announcements,
// finish_reason events, "[DONE]") pass through unchanged.
func (h *OpenAIHandler) MutateStreamChunk(ctx context.Context, chunk []byte, contentType string) ([]byte, error) {
	if h == nil {
		return chunk, nil
	}
	return unredactSSEEvent(ctx, chunk, h.unredactor, openAIStreamFields)
}
