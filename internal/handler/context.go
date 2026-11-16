package handler

import (
	"context"

	"github.com/local/redactor/internal/pii"
)

// mappingContextKey is the type of the context key under which a per-request
// pii.Mapping is stored. The type is unexported so no other package can create
// colliding keys.
type mappingContextKey struct{}

// contextWithMapping returns a copy of ctx carrying m.
func contextWithMapping(ctx context.Context, m *pii.Mapping) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, mappingContextKey{}, m)
}

// mappingFromContext returns the Mapping stored in ctx, or nil when the context
// carries none (for example a response to a request that contained no PII).
func mappingFromContext(ctx context.Context) *pii.Mapping {
	if ctx == nil {
		return nil
	}
	if m, ok := ctx.Value(mappingContextKey{}).(*pii.Mapping); ok {
		return m
	}
	return nil
}
