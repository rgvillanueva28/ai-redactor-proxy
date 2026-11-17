package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
)

// Adapter implements proxy.RequestMutator and proxy.ResponseMutator. It reads
// the resolved provider from the request context and dispatches to the
// matching provider handler. Falls back to GenericHandler for unknown
// providers (with a warning log).
//
// An Adapter is stateless and safe for concurrent use: the provider handlers it
// owns carry only the immutable redactor/unredactor pair, and all per-request
// state travels on the request context.
type Adapter struct {
	openai      *OpenAIHandler
	anthropic   *AnthropicHandler
	gemini      *GeminiHandler
	generic     *GenericHandler
	logRedacted bool
}

// Compile-time assertions that Adapter satisfies the proxy contracts.
var (
	_ proxy.RequestMutator  = (*Adapter)(nil)
	_ proxy.ResponseMutator = (*Adapter)(nil)
)

// NewAdapter returns an Adapter that dispatches to per-provider handlers built
// from r and u. A nil redactor or unredactor disables that direction of the
// transformation. When logRedacted is true, every redacted item is logged at
// info level as it is produced.
func NewAdapter(r *pii.Redactor, u *pii.Unredactor, logRedacted bool) *Adapter {
	return &Adapter{
		openai:      NewOpenAIHandler(r, u, logRedacted),
		anthropic:   NewAnthropicHandler(r, u, logRedacted),
		gemini:      NewGeminiHandler(r, u, logRedacted),
		generic:     NewGenericHandler(r, u, logRedacted),
		logRedacted: logRedacted,
	}
}

// handlerFor returns the provider-specific handler for the provider stored in
// ctx.
func (a *Adapter) handlerFor(ctx context.Context) interface {
	proxy.RequestMutator
	proxy.ResponseMutator
} {
	switch proxy.ContextProvider(ctx) {
	case proxy.ProviderOpenAI:
		return a.openai
	case proxy.ProviderAnthropic:
		return a.anthropic
	case proxy.ProviderGemini:
		return a.gemini
	default:
		slog.Warn("adapter: unknown provider, falling back to generic",
			"provider", proxy.ContextProvider(ctx))
		return a.generic
	}
}

// MutateRequest dispatches to the provider handler.
func (a *Adapter) MutateRequest(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
	return a.handlerFor(ctx).MutateRequest(ctx, req, body)
}

// MutateResponse dispatches to the provider handler.
func (a *Adapter) MutateResponse(ctx context.Context, resp *http.Response, body []byte) ([]byte, error) {
	return a.handlerFor(ctx).MutateResponse(ctx, resp, body)
}

// MutateStreamChunk dispatches to the provider handler.
func (a *Adapter) MutateStreamChunk(ctx context.Context, chunk []byte, contentType string) ([]byte, error) {
	return a.handlerFor(ctx).MutateStreamChunk(ctx, chunk, contentType)
}
