package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"
)

// hopByHopHeaders lists the connection-specific headers a proxy must not
// forward, per RFC 9110 section 7.6.1, plus the widely used Proxy-Connection
// variant. Headers named by a Connection header are stripped as well.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// maxPooledBuffer bounds the buffer retained by the pool. Buffers larger than
// this (for example a 20 MB request body) are dropped instead of pinned in
// memory for the lifetime of the process.
const maxPooledBuffer = 1 << 20

// Forwarder forwards requests to an upstream server. It owns a tuned
// *http.Transport (connection pooling sized for many concurrent CLI sessions,
// HTTP/2 enabled, compression left to the caller) and a pool of body-copy
// buffers so the steady state does not allocate per request.
//
// A Forwarder is safe for concurrent use.
type Forwarder struct {
	*http.Transport

	// roundTripper overrides the embedded transport. It exists so tests can
	// substitute an in-process mock upstream and measure the proxy pipeline
	// without the network stack's allocations.
	roundTripper http.RoundTripper

	bufPool sync.Pool
}

// RoundTrip implements http.RoundTripper. It delegates to the configured
// override when present and to the embedded transport otherwise, so a
// Forwarder can always stand in for an *http.Transport.
func (f *Forwarder) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.roundTripper != nil {
		return f.roundTripper.RoundTrip(req)
	}
	return f.Transport.RoundTrip(req)
}

// NewForwarder returns a Forwarder with a transport tuned for proxying AI CLI
// traffic: 64 idle connections per host, a 90 second idle timeout and a 10
// second TLS handshake timeout.
func NewForwarder() *Forwarder {
	f := &Forwarder{
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConnsPerHost: 64,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			// DisableCompression keeps the upstream body byte-for-byte
			// intact so mutators see exactly what the provider sent.
			DisableCompression: true,
			// Provider APIs are HTTP/2; ForceAttemptHTTP2 keeps that
			// possible even with a custom DialContext in the future.
			ForceAttemptHTTP2: true,
		},
	}
	f.bufPool.New = func() any { return new(bytes.Buffer) }
	return f
}

// getBuf returns an empty pooled buffer.
func (f *Forwarder) getBuf() *bytes.Buffer {
	buf := f.bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

// putBuf returns buf to the pool. Oversized buffers are discarded so a single
// huge body cannot pin memory forever.
func (f *Forwarder) putBuf(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	if buf.Cap() > maxPooledBuffer {
		return
	}
	buf.Reset()
	f.bufPool.Put(buf)
}

// Forward sends req to upstreamURL and returns the upstream response.
//
// req is not modified: it is cloned, and the clone is rewritten to use the
// upstream scheme and host while preserving the request path, query, method
// and headers. The Host header is set to the upstream host and hop-by-hop
// request headers are stripped.
//
// The request body is copied through a pooled buffer and wrapped in a poolBody
// that returns that buffer to the pool when the transport closes it. req.Body
// itself is consumed by the copy but not closed, so the caller keeps ownership
// of it; it must not be read again after Forward returns. The caller must read
// the returned response body to EOF before closing it, both to let the
// transport reuse the upstream connection and to release any pooled buffer the
// handler is holding.
func (f *Forwarder) Forward(ctx context.Context, req *http.Request, upstreamURL string) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("proxy: nil request")
	}
	if req.URL == nil {
		return nil, errors.New("proxy: nil request URL")
	}

	base, err := url.Parse(strings.TrimSpace(upstreamURL))
	if err != nil {
		return nil, fmt.Errorf("proxy: parse upstream URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("proxy: upstream URL %q is not absolute", upstreamURL)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	// WithContext copies the request shallowly: unlike Clone it does not clone
	// the URL or the header map, both of which are replaced below anyway.
	out := req.WithContext(ctx)
	u := new(url.URL)
	*u = *req.URL
	out.URL = u
	out.URL.Scheme = base.Scheme
	out.URL.Host = base.Host
	out.URL.Path, out.URL.RawPath = joinURLPath(base, req.URL)
	out.URL.RawQuery = joinRawQuery(base.RawQuery, req.URL.RawQuery)
	out.URL.Fragment = ""
	out.URL.RawFragment = ""
	// RequestURI must be empty on outgoing client requests.
	out.RequestURI = ""
	// Upstream virtual hosting expects its own Host header.
	out.Host = base.Host
	out.Close = false
	// Replace the cloned header map with one that omits hop-by-hop headers and
	// Content-Length; the transport writes Content-Length from
	// out.ContentLength, so building the string here would be wasted work.
	out.Header = forwardableHeaders(req.Header)

	if req.Body != nil && req.Body != http.NoBody {
		buf := f.getBuf()
		n, copyErr := io.Copy(buf, req.Body)
		if copyErr != nil {
			f.putBuf(buf)
			return nil, fmt.Errorf("proxy: read request body: %w", copyErr)
		}
		if n == 0 {
			f.putBuf(buf)
			out.Body = http.NoBody
			out.ContentLength = 0
		} else {
			out.Body = newPoolBody(buf.Bytes(), buf, &f.bufPool)
			out.ContentLength = n
		}
	} else {
		out.Body = http.NoBody
		out.ContentLength = 0
	}

	return f.RoundTrip(out)
}

// poolBody is a read-only request body backed by a pooled buffer. Closing it
// returns the buffer to the pool; the transport closes a request body only
// after it has been fully written, so the bytes are no longer needed at that
// point. Close is idempotent and safe to call from another goroutine.
type poolBody struct {
	data []byte
	off  int
	buf  *bytes.Buffer
	pool *sync.Pool
	once sync.Once
}

func newPoolBody(data []byte, buf *bytes.Buffer, pool *sync.Pool) *poolBody {
	return &poolBody{data: data, buf: buf, pool: pool}
}

// Read implements io.Reader.
func (b *poolBody) Read(p []byte) (int, error) {
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

// WriteTo lets io.Copy hand the body to the transport without an intermediate
// copy buffer.
func (b *poolBody) WriteTo(w io.Writer) (int64, error) {
	rest := b.data[b.off:]
	if len(rest) == 0 {
		return 0, nil
	}
	n, err := w.Write(rest)
	b.off += n
	if err == nil && n < len(rest) {
		err = io.ErrShortWrite
	}
	return int64(n), err
}

// Close returns the backing buffer to the pool.
func (b *poolBody) Close() error {
	b.once.Do(func() {
		if b.buf != nil {
			b.buf.Reset()
			b.pool.Put(b.buf)
			b.buf = nil
		}
	})
	return nil
}

// forwardableHeaders returns a copy of src without hop-by-hop headers and
// without Content-Length. Header value slices are shared with src, which is
// safe because net/http only reads request headers; sharing avoids allocating
// a fresh slice per header on the proxy hot path.
func forwardableHeaders(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	var drop map[string]bool
	for _, value := range src["Connection"] {
		for _, name := range strings.Split(value, ",") {
			if name = textproto.TrimString(name); name != "" {
				if drop == nil {
					drop = make(map[string]bool, 4)
				}
				drop[http.CanonicalHeaderKey(name)] = true
			}
		}
	}
	for name, values := range src {
		canonical := http.CanonicalHeaderKey(name)
		if isHopByHopHeader(canonical) || canonical == "Content-Length" || drop[canonical] {
			continue
		}
		dst[canonical] = values
	}
	return dst
}

// stripHopByHop deletes hop-by-hop headers from h, including any header named
// by one or more Connection header values.
func stripHopByHop(h http.Header) {
	if h == nil {
		return
	}
	for _, value := range h["Connection"] {
		for _, name := range strings.Split(value, ",") {
			if name = textproto.TrimString(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, name := range hopByHopHeaders {
		h.Del(name)
	}
}

// joinURLPath joins the upstream base path with the request path. When the
// upstream URL already ends with the request path (the generic passthrough
// case, where Router records the request URL as the upstream) the base path is
// kept as-is instead of being duplicated.
func joinURLPath(base, req *url.URL) (path, rawPath string) {
	if base.Path == "" && base.RawPath == "" {
		return req.Path, req.RawPath
	}
	if req.Path == "" && req.RawPath == "" {
		return base.Path, base.RawPath
	}
	if strings.HasSuffix(base.Path, req.Path) {
		return base.Path, base.RawPath
	}
	if base.RawPath != "" || req.RawPath != "" {
		baseRaw, reqRaw := base.RawPath, req.RawPath
		if baseRaw == "" {
			baseRaw = base.Path
		}
		if reqRaw == "" {
			reqRaw = req.Path
		}
		return joinPath(base.Path, req.Path), joinPath(baseRaw, reqRaw)
	}
	return joinPath(base.Path, req.Path), ""
}

// joinPath concatenates two path fragments with exactly one slash between them.
func joinPath(a, b string) string {
	switch {
	case strings.HasSuffix(a, "/") && strings.HasPrefix(b, "/"):
		return a + b[1:]
	case !strings.HasSuffix(a, "/") && !strings.HasPrefix(b, "/"):
		return a + "/" + b
	default:
		return a + b
	}
}

// joinRawQuery merges the upstream and request query strings without
// duplicating an identical query.
func joinRawQuery(base, req string) string {
	switch {
	case base == "":
		return req
	case req == "" || req == base:
		return base
	default:
		return base + "&" + req
	}
}
