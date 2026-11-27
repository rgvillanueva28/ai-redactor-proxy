package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
)

// Adapter implements proxy.RequestMutator and proxy.ResponseMutator. It reads
// the resolved provider from the request context and dispatches to the
// matching provider handler. Falls back to GenericHandler for unknown
// providers (with a warning log).
//
// An Adapter is stateless with respect to requests and safe for concurrent use:
// the provider handlers it owns carry only the immutable redactor/unredactor
// pair, and all per-request state travels on the request context. The handler
// set can still be replaced at runtime by SetLogToggles, which is why access to
// it is guarded by a mutex.
type Adapter struct {
	mu          sync.RWMutex
	redactor    *pii.Redactor
	unredactor  *pii.Unredactor
	openai      *OpenAIHandler
	anthropic   *AnthropicHandler
	gemini      *GeminiHandler
	generic     *GenericHandler
	logRedacted bool
	logBodies   bool
}

// Compile-time assertions that Adapter satisfies the proxy contracts.
var (
	_ proxy.RequestMutator  = (*Adapter)(nil)
	_ proxy.ResponseMutator = (*Adapter)(nil)
)

// NewAdapter returns an Adapter that dispatches to per-provider handlers built
// from r and u. A nil redactor or unredactor disables that direction of the
// transformation. When logRedacted is true, every redacted item is logged at
// info level as it is produced. When logBodies is true, the full request and
// response bodies are logged at debug level at every transformation stage.
func NewAdapter(r *pii.Redactor, u *pii.Unredactor, logRedacted bool, logBodies bool) *Adapter {
	return &Adapter{
		redactor:    r,
		unredactor:  u,
		openai:      NewOpenAIHandler(r, u, logRedacted, logBodies),
		anthropic:   NewAnthropicHandler(r, u, logRedacted, logBodies),
		gemini:      NewGeminiHandler(r, u, logRedacted, logBodies),
		generic:     NewGenericHandler(r, u, logRedacted, logBodies),
		logRedacted: logRedacted,
		logBodies:   logBodies,
	}
}

// SetLogToggles replaces the adapter's logging toggles and rebuilds the
// per-provider handlers so subsequent requests observe the new values. It is
// safe to call while requests are in flight: a request already inside a handler
// finishes against the old handler, and every later request dispatches through
// the new one.
func (a *Adapter) SetLogToggles(logRedacted, logBodies bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.logRedacted == logRedacted && a.logBodies == logBodies {
		return
	}

	a.logRedacted = logRedacted
	a.logBodies = logBodies
	a.openai = NewOpenAIHandler(a.redactor, a.unredactor, logRedacted, logBodies)
	a.anthropic = NewAnthropicHandler(a.redactor, a.unredactor, logRedacted, logBodies)
	a.gemini = NewGeminiHandler(a.redactor, a.unredactor, logRedacted, logBodies)
	a.generic = NewGenericHandler(a.redactor, a.unredactor, logRedacted, logBodies)
}

// ReloadConfig applies cfg to the running proxy: reg rebuilds its detector set
// and redactor swaps it in atomically, and a adopts the new logging toggles. It
// is the one call the configuration HTTP API needs after validating and saving
// a new config. A nil reg or a skips that half of the update.
func ReloadConfig(reg *pii.Registry, a *Adapter, cfg *config.Config) error {
	if reg != nil {
		if err := reg.Reload(cfg); err != nil {
			return err
		}
	}
	if a != nil && cfg != nil {
		a.SetLogToggles(cfg.PII.LogRedacted, cfg.PII.LogBodies)
	}
	return nil
}

// handlerFor returns the provider-specific handler for the provider stored in
// ctx.
func (a *Adapter) handlerFor(ctx context.Context) interface {
	proxy.RequestMutator
	proxy.ResponseMutator
} {
	a.mu.RLock()
	defer a.mu.RUnlock()

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
