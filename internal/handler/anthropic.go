package handler

import (
	"context"
	"net/http"

	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
)

// anthropicRequestFields are the request leaves that carry text in the Messages
// dialect:
//
//	{"system": "..." | [{"type":"text","text":"..."}],
//	 "messages":[{"content":"..."} |
//	             [{"type":"text","text":"..."},
//	              {"type":"tool_use","input":{...}},
//	              {"type":"tool_result","content":"..." | [{"type":"text","text":"..."}]}]]}
//
// tool_use.input is an arbitrary JSON object chosen by the model, so it is
// walked as a subtree: every string leaf inside it is redacted. thinking and
// redacted_thinking blocks carry "thinking"/"signature" instead of "text", so
// they are untouched.
var anthropicRequestFields = mergeFieldSets(
	newFieldSet(
		[]string{"messages", "content"},
		[]string{"messages", "content", "text"},
		[]string{"messages", "content", "content"},
		[]string{"messages", "content", "content", "text"},
		[]string{"system"},
		[]string{"system", "text"},
	),
	newSubtreeFieldSet(
		[]string{"messages", "content", "input"},
	),
)

// anthropicResponseFields are the buffered Messages response leaves:
// content[].text, plus every string leaf of a tool_use block's input. Thinking
// blocks and tool signatures are left alone.
var anthropicResponseFields = mergeFieldSets(
	newFieldSet(
		[]string{"content", "text"},
	),
	newSubtreeFieldSet(
		[]string{"content", "input"},
	),
)

// anthropicStreamFields are the streaming Messages delta leaves:
// content_block_delta.delta.text and the input_json_delta partial_json payload,
// which carries a fragment of the tool_use input as a JSON-encoded string.
var anthropicStreamFields = newFieldSet(
	[]string{"delta", "text"},
	[]string{"delta", "partial_json"},
)

// AnthropicHandler redacts Anthropic Messages API requests and unredacts the
// matching responses.
//
// The Messages dialect is structurally close to OpenAI's but differs in the
// places text hides: the system prompt is a top-level "system" field (string or
// array of text blocks) rather than a message, and responses carry an array of
// typed content blocks instead of a single message object. Both forms are
// walked here.
//
// An AnthropicHandler is stateless and safe for concurrent use.
type AnthropicHandler struct {
	redactor    *pii.Redactor
	unredactor  *pii.Unredactor
	logRedacted bool
	logBodies   bool
}

// Compile-time assertions that AnthropicHandler satisfies the proxy contracts.
var (
	_ proxy.RequestMutator  = (*AnthropicHandler)(nil)
	_ proxy.ResponseMutator = (*AnthropicHandler)(nil)
)

// NewAnthropicHandler returns a handler that redacts with r and unredacts with u.
// A nil redactor or unredactor disables that direction of the transformation.
// When logRedacted is true every redacted item is logged at info level. When
// logBodies is true every request, response and stream chunk body is logged at
// debug level, before and after the transformation.
func NewAnthropicHandler(r *pii.Redactor, u *pii.Unredactor, logRedacted bool, logBodies bool) *AnthropicHandler {
	return &AnthropicHandler{redactor: r, unredactor: u, logRedacted: logRedacted, logBodies: logBodies}
}

// MutateRequest redacts system (string or array of text blocks) and
// messages[].content (string or array of blocks), and publishes the
// per-request mapping on the request context.
func (h *AnthropicHandler) MutateRequest(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
	if h == nil {
		return body, nil
	}
	out, err := redactBody(req, body, h.redactor, anthropicRequestFields, h.logRedacted)
	if err != nil {
		return nil, err
	}
	logBody(h.logBodies, "request body (original)", body)
	logBody(h.logBodies, "request body (redacted)", out)
	return out, nil
}

// MutateResponse restores placeholders in content[].text.
func (h *AnthropicHandler) MutateResponse(ctx context.Context, resp *http.Response, body []byte) ([]byte, error) {
	if h == nil {
		return body, nil
	}
	out, err := unredactBody(ctx, resp, body, h.unredactor, anthropicResponseFields)
	if err != nil {
		return nil, err
	}
	logBody(h.logBodies, "response body (redacted)", body)
	logBody(h.logBodies, "response body (unredacted)", out)
	return out, nil
}

// MutateStreamChunk restores placeholders in one SSE event's
// content_block_delta.delta.text.
//
// Every other event type — message_start, content_block_start, ping,
// message_delta, message_stop — is returned byte-for-byte unchanged, including
// its "event:" line, because Anthropic clients dispatch on the event name.
// When logBodies is enabled the chunk is logged before and after unredaction,
// matching the OpenAI and Gemini handlers.
func (h *AnthropicHandler) MutateStreamChunk(ctx context.Context, chunk []byte, contentType string) ([]byte, error) {
	if h == nil {
		return chunk, nil
	}
	out, err := unredactSSEEvent(ctx, chunk, h.unredactor, anthropicStreamFields)
	if err != nil {
		return nil, err
	}
	logBody(h.logBodies, "stream chunk (redacted)", chunk)
	logBody(h.logBodies, "stream chunk (unredacted)", out)
	return out, nil
}
