package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
)

// GenericHandler implements proxy.RequestMutator and proxy.ResponseMutator by
// walking every string leaf in the JSON body.
//
// It is the fallback for upstreams without a dedicated dialect handler: every
// string in the request — prompts, tool arguments, file contents embedded as
// text — is redacted, and every string in the response is unredacted again.
// Object keys are left alone so the JSON shape the provider expects does not
// change.
//
// A GenericHandler is stateless and safe for concurrent use.
type GenericHandler struct {
	redactor    *pii.Redactor
	unredactor  *pii.Unredactor
	logRedacted bool
}

// Compile-time assertions that GenericHandler satisfies the proxy contracts.
var (
	_ proxy.RequestMutator  = (*GenericHandler)(nil)
	_ proxy.ResponseMutator = (*GenericHandler)(nil)
)

// NewGenericHandler returns a handler that redacts with r and unredacts with u.
// A nil redactor or unredactor disables that direction of the transformation,
// which keeps the handler usable in pass-through tests. When logRedacted is
// true every redacted item is logged at info level.
func NewGenericHandler(r *pii.Redactor, u *pii.Unredactor, logRedacted bool) *GenericHandler {
	return &GenericHandler{redactor: r, unredactor: u, logRedacted: logRedacted}
}

// MutateRequest walks the body, redacts every string leaf, and returns the
// redacted JSON.
//
// The resulting mapping is attached to the request's context (and the same
// helper, contextWithMapping, is what the response side reads back). A mutator
// cannot replace the context its caller holds, so *req is rewritten in place:
// anything downstream that reads req.Context() — including tests — can retrieve
// the mapping with mappingFromContext.
//
// Bodies that are not JSON are passed through unchanged: the proxy also carries
// file uploads and other binary payloads, and rejecting them would break
// endpoints that have nothing to redact at the leaf level.
func (g *GenericHandler) MutateRequest(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
	if len(body) == 0 || g == nil || g.redactor == nil {
		return body, nil
	}
	if !json.Valid(body) {
		return body, nil
	}

	// One mapping per request, shared by every leaf: placeholders must be
	// unique across the whole document or unredaction would restore the wrong
	// value. Redact allocates a fresh mapping per call, so each leaf's
	// placeholders are re-registered into m in document order.
	m := pii.GetMapping()

	out, err := WalkJSON(body, func(s string) (string, error) {
		redacted, local := g.redactor.Redact(s)
		if local == nil {
			return redacted, nil
		}
		defer pii.PutMapping(local)
		return mergeMapping(redacted, local, m), nil
	})
	if err != nil {
		pii.PutMapping(m)
		return nil, err
	}

	// Publish the mapping for the response half of the exchange. A mutator
	// cannot replace the context its caller holds, so the request is rewritten
	// in place; anything downstream that reads req.Context() (including tests)
	// can then retrieve the mapping with mappingFromContext.
	if req != nil {
		*req = *req.WithContext(contextWithMapping(req.Context(), m))
	}
	if g.logRedacted {
		logMapping(m)
	}
	return out, nil
}

// MutateResponse reads the mapping from ctx, walks the body, and unredacts
// every string leaf.
//
// The mapping is looked up on ctx first and then on resp.Request's context,
// because the proxy hands response mutators the request's context rather than
// any value a request mutator attached.
func (g *GenericHandler) MutateResponse(ctx context.Context, resp *http.Response, body []byte) ([]byte, error) {
	m := mappingFromContext(ctx)
	if m == nil && resp != nil && resp.Request != nil {
		m = mappingFromContext(resp.Request.Context())
	}
	if m == nil || len(body) == 0 || g == nil || g.unredactor == nil {
		return body, nil
	}
	if !json.Valid(body) {
		return body, nil
	}

	return WalkJSON(body, func(s string) (string, error) {
		return g.unredactor.Unredact(s, m), nil
	})
}

// MutateStreamChunk handles one SSE event (the proxy hands us one fully
// serialized SSE event per call). For generic, parse data: line(s), treat the
// data as JSON, walk string leaves with unredactor.
//
// Events whose data is not JSON (the "[DONE]" sentinel, keep-alives, provider
// specific non-JSON frames) are passed through untouched rather than aborting a
// stream that is already in flight.
func (g *GenericHandler) MutateStreamChunk(ctx context.Context, chunk []byte, contentType string) ([]byte, error) {
	if len(chunk) == 0 || g == nil || g.unredactor == nil {
		return chunk, nil
	}

	data, ok := ParseSSEData(chunk)
	if !ok {
		return chunk, nil
	}
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[DONE]" || !json.Valid([]byte(trimmed)) {
		return chunk, nil
	}

	m := mappingFromContext(ctx)
	if m == nil {
		return chunk, nil
	}

	out, err := WalkJSON([]byte(data), func(s string) (string, error) {
		return g.unredactor.Unredact(s, m), nil
	})
	if err != nil {
		return nil, err
	}
	return FormatSSEData(string(out)), nil
}

// mergeMapping re-registers every placeholder produced for one string leaf into
// dst, and rewrites redacted to use dst's placeholders.
//
// Placeholder numbering restarts at 1 for each call to Redact, so merging
// straight into a shared mapping would give two different values the same
// placeholder. Replacing them in a single pass (strings.Replacer does not
// rescan its own output) avoids the double-substitution trap that sequential
// strings.ReplaceAll calls would hit.
func mergeMapping(redacted string, local, dst *pii.Mapping) string {
	entries := local.All()
	if len(entries) == 0 {
		return redacted
	}

	placeholders := make([]string, 0, len(entries))
	for ph := range entries {
		placeholders = append(placeholders, ph)
	}
	// Deterministic numbering: per-category counters ascend in document order.
	sortPlaceholders(placeholders)

	pairs := make([]string, 0, 2*len(placeholders))
	for _, ph := range placeholders {
		pairs = append(pairs, ph, dst.Add(categoryFromPlaceholder(ph), entries[ph]))
	}
	return strings.NewReplacer(pairs...).Replace(redacted)
}

// categoryFromPlaceholder recovers the category encoded in a placeholder, e.g.
// "<EMAIL_1>" -> pii.CategoryEmail.
func categoryFromPlaceholder(placeholder string) pii.Category {
	name := strings.TrimSuffix(strings.TrimPrefix(placeholder, "<"), ">")
	if i := strings.LastIndexByte(name, '_'); i > 0 {
		name = name[:i]
	}
	return pii.Category(name)
}

// sortPlaceholders orders placeholders by numeric suffix, then by name.
func sortPlaceholders(placeholders []string) {
	sort.Slice(placeholders, func(i, j int) bool {
		a, b := placeholderIndex(placeholders[i]), placeholderIndex(placeholders[j])
		if a != b {
			return a < b
		}
		return placeholders[i] < placeholders[j]
	})
}

// placeholderIndex returns the numeric suffix of a placeholder, or 0 when there
// is none.
func placeholderIndex(placeholder string) int {
	s := strings.TrimSuffix(placeholder, ">")
	if i := strings.LastIndexByte(s, '_'); i >= 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			return n
		}
	}
	return 0
}
