package proxy

import (
	"context"
	"net/http"
)

// RequestMutator can inspect and rewrite the request body before it is
// forwarded upstream. Implementations must be safe for concurrent use: a
// single mutator may be invoked by many request goroutines at once.
type RequestMutator interface {
	// MutateRequest returns the body to forward upstream. Returning a nil
	// body means "send an empty body"; returning an error aborts the
	// request without contacting the upstream.
	MutateRequest(ctx context.Context, req *http.Request, body []byte) (newBody []byte, err error)
}

// ResponseMutator can inspect and rewrite the response body before it is
// returned to the client. It must handle both ordinary buffered responses
// and streaming responses (for example SSE chunks), which arrive one chunk
// at a time.
type ResponseMutator interface {
	// MutateResponse rewrites a fully buffered response body.
	MutateResponse(ctx context.Context, resp *http.Response, body []byte) (newBody []byte, err error)

	// MutateStreamChunk rewrites a single chunk of a streaming response.
	// contentType is the response Content-Type, which lets implementations
	// distinguish SSE from other streaming formats.
	MutateStreamChunk(ctx context.Context, chunk []byte, contentType string) (newChunk []byte, err error)
}

// RequestMutatorFunc adapts an ordinary function to RequestMutator.
type RequestMutatorFunc func(ctx context.Context, req *http.Request, body []byte) ([]byte, error)

// MutateRequest implements RequestMutator.
func (f RequestMutatorFunc) MutateRequest(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
	return f(ctx, req, body)
}

// ResponseMutatorFuncs adapts ordinary functions to ResponseMutator. A nil
// function is treated as the identity transformation so callers only need to
// supply the side they care about.
type ResponseMutatorFuncs struct {
	RespFunc   func(ctx context.Context, resp *http.Response, body []byte) ([]byte, error)
	StreamFunc func(ctx context.Context, chunk []byte, contentType string) ([]byte, error)
}

// MutateResponse implements ResponseMutator.
func (r ResponseMutatorFuncs) MutateResponse(ctx context.Context, resp *http.Response, body []byte) ([]byte, error) {
	if r.RespFunc == nil {
		return body, nil
	}
	return r.RespFunc(ctx, resp, body)
}

// MutateStreamChunk implements ResponseMutator.
func (r ResponseMutatorFuncs) MutateStreamChunk(ctx context.Context, chunk []byte, contentType string) ([]byte, error) {
	if r.StreamFunc == nil {
		return chunk, nil
	}
	return r.StreamFunc(ctx, chunk, contentType)
}

// NoopMutator is a Mutator that leaves requests and responses untouched.
type NoopMutator struct{}

// MutateRequest implements RequestMutator.
func (NoopMutator) MutateRequest(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
	return body, nil
}

// MutateResponse implements ResponseMutator.
func (NoopMutator) MutateResponse(ctx context.Context, resp *http.Response, body []byte) ([]byte, error) {
	return body, nil
}

// MutateStreamChunk implements ResponseMutator.
func (NoopMutator) MutateStreamChunk(ctx context.Context, chunk []byte, contentType string) ([]byte, error) {
	return chunk, nil
}
