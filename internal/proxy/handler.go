package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// DefaultMaxRequestBodyBytes is the request body limit applied by NewHandler:
// 50 MB, comfortably above the largest prompt a CLI sends but small enough to
// bound memory use.
const DefaultMaxRequestBodyBytes int64 = 50 << 20

// Handler is the proxy's HTTP entry point. It resolves the upstream route,
// runs the request body through the RequestMutator chain, forwards it, and
// then either streams (SSE) or buffers the response through the
// ResponseMutator chain.
//
// A Handler is safe for concurrent use as long as its mutators are.
type Handler struct {
	router       *Router
	forwarder    *Forwarder
	reqMutators  []RequestMutator
	respMutators []ResponseMutator

	// MaxRequestBodyBytes bounds the request body that is read into memory.
	// Requests above it are rejected with 413. Zero or negative means
	// DefaultMaxRequestBodyBytes.
	MaxRequestBodyBytes int64
}

// NewHandler returns a Handler that routes with r, forwards with f, and
// applies reqMutators and respMutators in slice order.
func NewHandler(r *Router, f *Forwarder, reqMutators []RequestMutator, respMutators []ResponseMutator) *Handler {
	if f == nil {
		f = NewForwarder()
	}
	if r == nil {
		r = NewRouter()
	}
	return &Handler{
		router:              r,
		forwarder:           f,
		reqMutators:         reqMutators,
		respMutators:        respMutators,
		MaxRequestBodyBytes: DefaultMaxRequestBodyBytes,
	}
}

// bodyLimit returns the effective request body limit.
func (h *Handler) bodyLimit() int64 {
	if h.MaxRequestBodyBytes <= 0 {
		return DefaultMaxRequestBodyBytes
	}
	return h.MaxRequestBodyBytes
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Resolve the route and publish the provider on the request context so
	// mutators can adapt to the upstream API dialect.
	route := h.router.Resolve(r)
	ctx := contextWithProvider(r.Context(), route.Provider)
	r = r.WithContext(ctx)

	// 2. Read the body into a pooled buffer, enforcing the size limit.
	bodyBuf := h.forwarder.getBuf()
	readErr, tooLarge := h.readBody(bodyBuf, r)
	if tooLarge {
		h.forwarder.putBuf(bodyBuf)
		http.Error(w, "proxy: request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if readErr != nil {
		h.forwarder.putBuf(bodyBuf)
		http.Error(w, "proxy: read request body: "+readErr.Error(), http.StatusBadRequest)
		return
	}

	// 3. Run the request mutator chain, each on the previous output.
	body := bodyBuf.Bytes()
	for _, m := range h.reqMutators {
		if m == nil {
			continue
		}
		mutated, err := m.MutateRequest(ctx, r, body)
		if err != nil {
			h.forwarder.putBuf(bodyBuf)
			http.Error(w, "proxy: request mutation failed", http.StatusInternalServerError)
			return
		}
		body = mutated
	}
	if body == nil {
		body = []byte{}
	}

	r.Body = getBodyReader(body)
	// The pooled buffer holding body is recycled as soon as the request has
	// been forwarded, and replaying a pre-mutation body would bypass
	// redaction. Dropping GetBody makes the transport fail rather than retry
	// with bytes it no longer owns.
	r.GetBody = nil
	prevLen := r.ContentLength
	r.ContentLength = int64(len(body))
	if len(body) > 0 {
		// Only pay for formatting a new Content-Length when the body actually
		// changed size; the header is usually already correct.
		if prevLen != r.ContentLength || r.Header.Get("Content-Length") == "" {
			r.Header.Set("Content-Length", strconv.Itoa(len(body)))
		}
	} else {
		r.Header.Del("Content-Length")
	}

	// 4. Forward. Forward copies the body into its own pooled buffer before
	// returning, so the buffer we read into can go back to the pool now.
	resp, err := h.forwarder.Forward(ctx, r, route.UpstreamURL)
	putBodyReader(r.Body)
	bodyBuf.Reset()
	h.forwarder.putBuf(bodyBuf)
	if err != nil {
		http.Error(w, "proxy: upstream request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	// Drain before closing so the upstream connection is reusable, and so any
	// pooled buffer held while reading the body is released.
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	// 5. Never leak connection-specific response headers to the client.
	stripHopByHop(resp.Header)

	contentType := resp.Header.Get("Content-Type")
	if isEventStream(contentType) {
		h.serveStream(ctx, w, resp, contentType)
		return
	}
	h.serveBuffered(ctx, w, resp)
}

// readBody copies r's body into buf, refusing to read more than the configured
// limit. It reports a read error and whether the limit was exceeded.
func (h *Handler) readBody(buf *bytes.Buffer, r *http.Request) (err error, tooLarge bool) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, false
	}
	limit := h.bodyLimit()
	if r.ContentLength > limit {
		return nil, true
	}
	if r.ContentLength > 0 {
		// A declared length is trustworthy: net/http rejects requests whose
		// body does not match it, so the buffer can be filled without wrapping
		// the body in a limiting reader. This is the common path.
		if _, err := buf.ReadFrom(r.Body); err != nil {
			return err, false
		}
		if int64(buf.Len()) > limit {
			return nil, true
		}
		return nil, false
	}
	// Unknown length (chunked upload): enforce the limit while reading.
	n, err := io.Copy(buf, io.LimitReader(r.Body, limit+1))
	if err != nil {
		return err, false
	}
	if n > limit {
		return nil, true
	}
	return nil, false
}

// serveBuffered reads the whole upstream body, applies the response mutator
// chain, and writes the result to the client. The pooled buffer is held until
// after the write because the mutated body may alias it, and only then
// returned to the pool.
func (h *Handler) serveBuffered(ctx context.Context, w http.ResponseWriter, resp *http.Response) {
	buf := h.forwarder.getBuf()
	defer h.forwarder.putBuf(buf)

	if _, err := io.Copy(buf, resp.Body); err != nil {
		http.Error(w, "proxy: read upstream response: "+err.Error(), http.StatusBadGateway)
		return
	}

	body := buf.Bytes()
	for _, m := range h.respMutators {
		if m == nil {
			continue
		}
		mutated, err := m.MutateResponse(ctx, resp, body)
		if err != nil {
			http.Error(w, "proxy: response mutation failed", http.StatusBadGateway)
			return
		}
		body = mutated
	}

	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(resp.StatusCode)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

// serveStream relays an SSE response event by event, flushing after each one so
// clients see tokens as the upstream produces them. Each serialised event is
// passed through the stream mutator chain before it is written.
func (h *Handler) serveStream(ctx context.Context, w http.ResponseWriter, resp *http.Response, contentType string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "proxy: streaming requires http.Flusher", http.StatusHTTPVersionNotSupported)
		return
	}

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	buf := h.forwarder.getBuf()
	defer h.forwarder.putBuf(buf)

	// A parse or write error stops the stream; the status line is already sent,
	// so there is nothing left to report to the client.
	_ = ParseEvents(resp.Body, func(ev SSEEvent) error {
		buf.Reset()
		if _, err := WriteSSEEvent(buf, ev); err != nil {
			return err
		}
		chunk := buf.Bytes()
		for _, m := range h.respMutators {
			if m == nil {
				continue
			}
			mutated, err := m.MutateStreamChunk(ctx, chunk, contentType)
			if err != nil {
				return err
			}
			chunk = mutated
		}
		if len(chunk) == 0 {
			return nil
		}
		if _, err := w.Write(chunk); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
}

// bodyReaderPool recycles the adapter used for http.Request.Body, keeping the
// steady state free of per-request allocations.
var bodyReaderPool = sync.Pool{
	New: func() any { return new(bodyReader) },
}

// getBodyReader returns a pooled bodyReader over data.
func getBodyReader(data []byte) *bodyReader {
	br := bodyReaderPool.Get().(*bodyReader)
	br.data = data
	br.off = 0
	return br
}

// putBodyReader releases rc back to the pool when it is a bodyReader. The
// handler calls it only after Forward has copied the body, so no reader
// outlives the call.
func putBodyReader(rc io.ReadCloser) {
	br, ok := rc.(*bodyReader)
	if !ok {
		return
	}
	br.data = nil
	br.off = 0
	bodyReaderPool.Put(br)
}

// bodyReader adapts a byte slice to the io.ReadCloser that http.Request.Body
// requires.
type bodyReader struct {
	data []byte
	off  int
}

// Read implements io.Reader.
func (b *bodyReader) Read(p []byte) (int, error) {
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

// Close implements io.Closer.
func (b *bodyReader) Close() error { return nil }

// isEventStream reports whether contentType denotes SSE.
func isEventStream(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

// copyResponseHeaders copies src into dst, skipping hop-by-hop headers and
// Content-Length, which the handler recomputes. A ResponseWriter's header map
// is never nil in practice, but a nil dst is tolerated.
func copyResponseHeaders(dst, src http.Header) {
	if dst == nil {
		return
	}
	for name, values := range src {
		canonical := http.CanonicalHeaderKey(name)
		if isHopByHopHeader(canonical) || canonical == "Content-Length" {
			continue
		}
		for _, v := range values {
			dst.Add(name, v)
		}
	}
}

// isHopByHopHeader reports whether name is a connection-specific header.
func isHopByHopHeader(name string) bool {
	for _, h := range hopByHopHeaders {
		if name == h {
			return true
		}
	}
	return false
}
