package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testHost = "api.test"

// newProxyWithRouter starts a test HTTP server in front of the given router.
func newProxyWithRouter(t *testing.T, router *Router, reqMutators []RequestMutator, respMutators []ResponseMutator) (*Handler, *httptest.Server) {
	t.Helper()
	h := NewHandler(router, NewForwarder(), reqMutators, respMutators)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return h, srv
}

// newProxy starts a test HTTP server routing testHost to upstreamURL.
func newProxy(t *testing.T, upstreamURL string, reqMutators []RequestMutator, respMutators []ResponseMutator) (*Handler, *httptest.Server) {
	t.Helper()
	return newProxyWithRouter(t, NewRouter().WithProvider(testHost, upstreamURL), reqMutators, respMutators)
}

// proxyRequest builds a request to the proxy server with the Host header set to
// host, so the Router resolves it to the mock upstream.
func proxyRequest(t *testing.T, method, proxyURL, path, host string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, proxyURL+path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = host
	return req
}

// mustDo sends req and fails the test on a transport error.
func mustDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	return resp
}

// readBody reads and closes an HTTP response body.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(b)
}

func TestForwarder_BufferedRoundTrip(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen-Method", r.Method)
		w.Header().Set("X-Seen-Path", r.URL.Path)
		w.Header().Set("X-Seen-Query", r.URL.RawQuery)
		w.Header().Set("X-Seen-Trace", r.Header.Get("X-Trace"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"echo":%q,"body":%q}`, r.Header.Get("X-Echo"), string(body))
	}))
	defer upstream.Close()

	_, proxySrv := newProxy(t, upstream.URL, nil, nil)

	req := proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/chat/completions?stream=false", testHost,
		strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("X-Trace", "trace-1")
	req.Header.Set("X-Echo", "echoed")
	resp := mustDo(t, req)
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	if got := resp.Header.Get("X-Seen-Method"); got != http.MethodPost {
		t.Errorf("method seen upstream = %q, want POST", got)
	}
	if got := resp.Header.Get("X-Seen-Path"); got != "/v1/chat/completions" {
		t.Errorf("path seen upstream = %q", got)
	}
	if got := resp.Header.Get("X-Seen-Query"); got != "stream=false" {
		t.Errorf("query seen upstream = %q", got)
	}
	if got := resp.Header.Get("X-Seen-Trace"); got != "trace-1" {
		t.Errorf("header seen upstream = %q", got)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(body, `"echo":"echoed"`) {
		t.Errorf("body missing echoed header: %s", body)
	}
	if !strings.Contains(body, `"body":"{\"prompt\":\"hi\"}"`) {
		t.Errorf("body missing request payload: %s", body)
	}
}

func TestForwarder_AcceptEncodingStripped(t *testing.T) {
	const payload = `{"id":"msg_1","text":"hello"}`

	seen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, payload)
	}))
	defer upstream.Close()

	_, proxySrv := newProxy(t, upstream.URL, nil, nil)

	req := proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/chat/completions", testHost,
		strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("Accept-Encoding", "gzip")
	resp := mustDo(t, req)
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", resp.StatusCode, body)
	}
	if got := <-seen; got != "" {
		t.Errorf("upstream saw Accept-Encoding = %q, want none", got)
	}
	if body != payload {
		t.Errorf("body = %q, want %q", body, payload)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Errorf("response body is not valid JSON: %v (body %q)", err, body)
	} else if got, _ := decoded["text"].(string); got != "hello" {
		t.Errorf("decoded text = %q, want hello", got)
	}
}

func TestForwarder_SSEStreaming(t *testing.T) {
	firstSeen := make(chan struct{})
	var once sync.Once
	streamedEarly := make(chan bool, 1)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: delta\ndata: tok-1\n\n")
		flusher.Flush()

		select {
		case <-firstSeen:
			streamedEarly <- true
		case <-time.After(3 * time.Second):
			streamedEarly <- false
		}
		for i := 2; i <= 5; i++ {
			fmt.Fprintf(w, "data: tok-%d\n\n", i)
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	_, proxySrv := newProxy(t, upstream.URL, nil, nil)
	req := proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/messages", testHost, strings.NewReader(`{"stream":true}`))
	resp := mustDo(t, req)
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var events []SSEEvent
	err := ParseEvents(resp.Body, func(ev SSEEvent) error {
		if len(events) == 0 {
			once.Do(func() { close(firstSeen) })
		}
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("parse SSE stream: %v", err)
	}

	if len(events) != 5 {
		t.Fatalf("got %d events, want 5: %+v", len(events), events)
	}
	if events[0].Event != "delta" {
		t.Errorf("first event name = %q, want delta", events[0].Event)
	}
	for i, ev := range events {
		want := fmt.Sprintf("tok-%d", i+1)
		if ev.Data != want {
			t.Errorf("event %d data = %q, want %q", i, ev.Data, want)
		}
	}

	select {
	case early := <-streamedEarly:
		if !early {
			t.Fatal("proxy buffered the SSE response instead of streaming it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upstream streaming result")
	}
}

func TestMiddleware_RequestMutatorCalled(t *testing.T) {
	const mutated = `{"redacted":true}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen-Length", r.Header.Get("Content-Length"))
		fmt.Fprintf(w, `{"body":%q}`, string(body))
	}))
	defer upstream.Close()

	mutator := RequestMutatorFunc(func(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
		if string(body) != `{"prompt":"secret"}` {
			return nil, fmt.Errorf("unexpected original body %q", body)
		}
		return []byte(mutated), nil
	})
	_, proxySrv := newProxy(t, upstream.URL, []RequestMutator{mutator}, nil)

	resp := mustDo(t, proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/chat", testHost, strings.NewReader(`{"prompt":"secret"}`)))
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, fmt.Sprintf(`"body":%q`, mutated)) {
		t.Fatalf("upstream did not receive mutated body: %s", body)
	}
	if got := resp.Header.Get("X-Seen-Length"); got != strconv.Itoa(len(mutated)) {
		t.Errorf("upstream Content-Length = %q, want %d", got, len(mutated))
	}
}

func TestMiddleware_ResponseMutatorCalled(t *testing.T) {
	const original = `{"choices":[]}`
	const suffix = "\n[MUTATED]"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, original)
	}))
	defer upstream.Close()

	called := false
	mutator := ResponseMutatorFuncs{RespFunc: func(ctx context.Context, resp *http.Response, body []byte) ([]byte, error) {
		called = true
		if string(body) != original {
			return nil, fmt.Errorf("unexpected response body %q", body)
		}
		return append(body, suffix...), nil
	}}
	_, proxySrv := newProxy(t, upstream.URL, nil, []ResponseMutator{mutator})

	resp := mustDo(t, proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/chat", testHost, nil))
	body := readBody(t, resp)

	if !called {
		t.Fatal("response mutator was not called")
	}
	want := original + suffix
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(want)) {
		t.Errorf("Content-Length = %q, want %d", got, len(want))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// ctxStateKey is the context key used by tests that exercise per-request
// state sharing between the request and response mutator phases.
type ctxStateKey struct{}

func TestMiddleware_RequestMutatorStoresStateOnContext(t *testing.T) {
	const original = `{"choices":[]}`
	const stored = "stored"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, original)
	}))
	defer upstream.Close()

	// The request mutator enriches the request context, the way an adapter
	// stores a redaction mapping so the response phase can unredact with it.
	reqMutator := RequestMutatorFunc(func(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
		*req = *req.WithContext(context.WithValue(req.Context(), ctxStateKey{}, stored))
		return body, nil
	})

	// The response mutator must observe that value through the ctx it is
	// given, not through the request (which it never sees).
	wantProvider := inferProvider(testHost, upstream.URL)
	sawProvider := false
	respMutator := ResponseMutatorFuncs{RespFunc: func(ctx context.Context, resp *http.Response, body []byte) ([]byte, error) {
		if got := ContextProvider(ctx); got != wantProvider {
			return nil, fmt.Errorf("response-phase provider = %q, want %q", got, wantProvider)
		}
		sawProvider = true
		got, ok := ctx.Value(ctxStateKey{}).(string)
		if !ok {
			return nil, fmt.Errorf("response-phase context missing value stored by request mutator")
		}
		return append(body, "["+got+"]"...), nil
	}}

	_, proxySrv := newProxy(t, upstream.URL, []RequestMutator{reqMutator}, []ResponseMutator{respMutator})

	resp := mustDo(t, proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/chat", testHost, strings.NewReader(`{"prompt":"hi"}`)))
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
	if !sawProvider {
		t.Fatal("response mutator was not called")
	}
	want := original + "[" + stored + "]"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

// uppercaseDataChunk uppercases the payload of every SSE data line, leaving the
// event framing intact.
func uppercaseDataChunk(chunk []byte, contentType string) ([]byte, error) {
	if !strings.HasPrefix(contentType, "text/event-stream") {
		return chunk, nil
	}
	out := make([]byte, 0, len(chunk))
	for _, line := range strings.SplitAfter(string(chunk), "\n") {
		line = strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(line, "data: ") {
			out = append(out, "data: "...)
			out = append(out, strings.ToUpper(line[len("data: "):])...)
			out = append(out, '\n')
			continue
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out, nil
}

func TestMiddleware_StreamChunkMutator(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(w, "data: tok-%d\n\n", i)
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	mutator := ResponseMutatorFuncs{StreamFunc: func(ctx context.Context, chunk []byte, contentType string) ([]byte, error) {
		return uppercaseDataChunk(chunk, contentType)
	}}
	_, proxySrv := newProxy(t, upstream.URL, nil, []ResponseMutator{mutator})

	resp := mustDo(t, proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/messages", testHost, nil))
	defer resp.Body.Close()

	var data []string
	if err := ParseEvents(resp.Body, func(ev SSEEvent) error {
		data = append(data, ev.Data)
		return nil
	}); err != nil {
		t.Fatalf("parse SSE stream: %v", err)
	}

	want := []string{"TOK-1", "TOK-2", "TOK-3"}
	if strings.Join(data, ",") != strings.Join(want, ",") {
		t.Fatalf("streamed data = %v, want %v", data, want)
	}
}

// freePort reserves a loopback port and releases it for the server to bind.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return addr
}

// waitForListener blocks until addr accepts connections.
func waitForListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s did not start", addr)
}

func TestServer_GracefulShutdown(t *testing.T) {
	addr := freePort(t)
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, "in-flight-done")
	})

	srv := NewServer(addr, handler)
	ctx, cancel := context.WithCancel(context.Background())
	startErr := make(chan error, 1)
	go func() { startErr <- srv.Start(ctx) }()
	waitForListener(t, addr)

	type result struct {
		body string
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Get("http://" + addr + "/slow")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		resCh <- result{body: string(b), err: err}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request never reached the handler")
	}

	// Cancel while the request is still running: Shutdown must wait for it.
	cancel()
	close(release)

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("in-flight request failed: %v", res.err)
		}
		if res.body != "in-flight-done" {
			t.Fatalf("in-flight body = %q, want in-flight-done", res.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight request did not complete during graceful shutdown")
	}

	select {
	case err := <-startErr:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after shutdown")
	}
}

func TestHandler_MaxRequestBodyBytes(t *testing.T) {
	var seen atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen.Store(int64(len(body)))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h, proxySrv := newProxy(t, upstream.URL, nil, nil)
	h.MaxRequestBodyBytes = 16

	resp := mustDo(t, proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/chat", testHost,
		strings.NewReader(strings.Repeat("x", 32))))
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %q)", resp.StatusCode, body)
	}
	if got := seen.Load(); got != 0 {
		t.Fatalf("oversized body reached upstream (%d bytes)", got)
	}

	resp = mustDo(t, proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/chat", testHost,
		strings.NewReader(strings.Repeat("x", 16))))
	readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("body at limit: status = %d, want 200", resp.StatusCode)
	}
	if got := seen.Load(); got != 16 {
		t.Fatalf("body at limit: upstream saw %d bytes, want 16", got)
	}
}

func TestHandler_HopByHopHeadersStripped(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "keep-alive, X-Hop-Only")
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("X-Hop-Only", "should-not-leak")
		w.Header().Set("X-Custom", "kept")
		w.Header().Set("Trailer", "X-Trailer")
		fmt.Fprint(w, "payload")
	}))
	defer upstream.Close()

	_, proxySrv := newProxy(t, upstream.URL, nil, nil)

	resp := mustDo(t, proxyRequest(t, http.MethodGet, proxySrv.URL, "/v1/models", testHost, nil))
	body := readBody(t, resp)

	if body != "payload" {
		t.Fatalf("body = %q, want payload", body)
	}
	if got := resp.Header.Get("Connection"); got != "" {
		t.Errorf("Connection header leaked: %q", got)
	}
	if got := resp.Header.Get("Keep-Alive"); got != "" {
		t.Errorf("Keep-Alive header leaked: %q", got)
	}
	if got := resp.Header.Get("X-Hop-Only"); got != "" {
		t.Errorf("Connection-nominated header leaked: %q", got)
	}
	if got := resp.Header.Get("X-Custom"); got != "kept" {
		t.Errorf("X-Custom = %q, want kept", got)
	}
}

func TestHandler_ProviderInContext(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, `{"provider":%q}`, string(body))
	}))
	defer upstream.Close()

	router := NewRouter().WithProvider("api.anthropic.com", upstream.URL)
	mutator := RequestMutatorFunc(func(ctx context.Context, req *http.Request, body []byte) ([]byte, error) {
		fromCtx := ContextProvider(ctx)
		fromReq := ContextProvider(req.Context())
		if fromCtx != ProviderAnthropic {
			return nil, fmt.Errorf("context provider = %q, want anthropic", fromCtx)
		}
		if fromReq != ProviderAnthropic {
			return nil, fmt.Errorf("request context provider = %q, want anthropic", fromReq)
		}
		return []byte(fromCtx), nil
	})
	_, proxySrv := newProxyWithRouter(t, router, []RequestMutator{mutator}, nil)

	resp := mustDo(t, proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/messages", "api.anthropic.com",
		strings.NewReader("original")))
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"provider":"anthropic"`) {
		t.Fatalf("mutator did not see provider in context: %s", body)
	}
}

func TestHandler_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstreamURL := upstream.URL
	upstream.Close()

	_, proxySrv := newProxy(t, upstreamURL, nil, nil)
	resp := mustDo(t, proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/chat", testHost, nil))
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %q)", resp.StatusCode, body)
	}
}

func TestHandler_RequestMutationError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be contacted when a request mutator fails")
	}))
	defer upstream.Close()

	mutator := RequestMutatorFunc(func(context.Context, *http.Request, []byte) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	})
	_, proxySrv := newProxy(t, upstream.URL, []RequestMutator{mutator}, nil)

	resp := mustDo(t, proxyRequest(t, http.MethodPost, proxySrv.URL, "/v1/chat", testHost, nil))
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %q)", resp.StatusCode, body)
	}
}

func TestHandler_ResponseMutationError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	mutator := ResponseMutatorFuncs{RespFunc: func(context.Context, *http.Response, []byte) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	}}
	_, proxySrv := newProxy(t, upstream.URL, nil, []ResponseMutator{mutator})

	resp := mustDo(t, proxyRequest(t, http.MethodGet, proxySrv.URL, "/v1/models", testHost, nil))
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %q)", resp.StatusCode, body)
	}
}

// plainWriter hides the http.Flusher implementation of an
// httptest.ResponseRecorder.
type plainWriter struct {
	http.ResponseWriter
}

func TestHandler_StreamWithoutFlusher(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: hi\n\n")
	}))
	defer upstream.Close()

	_, proxySrv := newProxy(t, upstream.URL, nil, nil)

	// The proxy's ServeHTTP is exercised directly so a ResponseWriter without
	// Flush can be supplied.
	req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/v1/stream", nil)
	rec := httptest.NewRecorder()
	handler := NewHandler(NewRouter().WithProvider(testHost, upstream.URL), NewForwarder(), nil, nil)
	handler.ServeHTTP(plainWriter{ResponseWriter: rec}, req)

	if rec.Code != http.StatusHTTPVersionNotSupported {
		t.Fatalf("status = %d, want 505", rec.Code)
	}

	// The same request through the real proxy server streams successfully.
	resp := mustDo(t, proxyRequest(t, http.MethodGet, proxySrv.URL, "/v1/stream", testHost, nil))
	body := readBody(t, resp)
	if !strings.Contains(body, "data: hi") {
		t.Fatalf("stream body = %q", body)
	}
}

func TestForwarder_InvalidUpstreamURL(t *testing.T) {
	f := NewForwarder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/v1", nil)

	if _, err := f.Forward(context.Background(), nil, "http://example.com"); err == nil {
		t.Fatal("expected error for nil request")
	}
	if _, err := f.Forward(context.Background(), req, ""); err == nil {
		t.Fatal("expected error for empty upstream URL")
	}
	if _, err := f.Forward(context.Background(), req, "/relative"); err == nil {
		t.Fatal("expected error for relative upstream URL")
	}
	if _, err := f.Forward(context.Background(), req, "http://[::1"); err == nil {
		t.Fatal("expected error for malformed upstream URL")
	}
}

func TestURLJoining(t *testing.T) {
	tests := []struct {
		name        string
		base        string
		req         string
		wantPath    string
		wantRawPath string
	}{
		{name: "empty base path", base: "https://up.example", req: "/v1/chat", wantPath: "/v1/chat"},
		{name: "base prefix", base: "https://up.example/api", req: "/v1/chat", wantPath: "/api/v1/chat"},
		{name: "base prefix with slash", base: "https://up.example/api/", req: "/v1/chat", wantPath: "/api/v1/chat"},
		{name: "no request path", base: "https://up.example/api", req: "", wantPath: "/api"},
		{name: "generic passthrough", base: "https://up.example/v1/chat", req: "/v1/chat", wantPath: "/v1/chat"},
		{name: "raw path preserved", base: "https://up.example/api", req: "/v1/a%2Fb", wantPath: "/api/v1/a/b", wantRawPath: "/api/v1/a%2Fb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, err := url.Parse(tc.base)
			if err != nil {
				t.Fatalf("parse base: %v", err)
			}
			req, err := url.Parse(tc.req)
			if err != nil {
				t.Fatalf("parse req: %v", err)
			}
			path, rawPath := joinURLPath(base, req)
			if path != tc.wantPath {
				t.Errorf("path = %q, want %q", path, tc.wantPath)
			}
			if rawPath != tc.wantRawPath {
				t.Errorf("rawPath = %q, want %q", rawPath, tc.wantRawPath)
			}
		})
	}

	for _, tc := range []struct{ base, req, want string }{
		{"", "a=1", "a=1"},
		{"a=1", "", "a=1"},
		{"a=1", "a=1", "a=1"},
		{"a=1", "b=2", "a=1&b=2"},
	} {
		if got := joinRawQuery(tc.base, tc.req); got != tc.want {
			t.Errorf("joinRawQuery(%q, %q) = %q, want %q", tc.base, tc.req, got, tc.want)
		}
	}

	if got := isEventStream("Text/Event-Stream; charset=utf-8"); !got {
		t.Error("isEventStream should match case-insensitively")
	}
	if got := isEventStream("application/json"); got {
		t.Error("isEventStream should not match JSON")
	}

	h := http.Header{"X-Test": {"1"}}
	stripHopByHop(h)
	if len(h) != 1 {
		t.Errorf("stripHopByHop removed unrelated header: %v", h)
	}
	// Exercises the nil destination guard.
	copyResponseHeaders(nil, h)

	// Oversized buffers must not be returned to the pool.
	f := NewForwarder()
	big := bytes.NewBuffer(make([]byte, 0, maxPooledBuffer+1))
	f.putBuf(big)
	if big.Len() != 0 {
		t.Errorf("putBuf should reset buffer, len = %d", big.Len())
	}
	f.putBuf(nil)
}

// mockUpstream is an in-process http.RoundTripper standing in for a provider
// API in allocation benchmarks. It reuses a single response object, so its own
// allocations do not pollute the proxy's numbers.
type mockUpstream struct {
	resp *http.Response
	body *reusableBody
}

func newMockUpstream(response []byte) *mockUpstream {
	header := make(http.Header, 2)
	header.Set("Content-Type", "application/json")
	body := &reusableBody{Reader: bytes.NewReader(response), data: response}
	return &mockUpstream{
		resp: &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        header,
			Body:          body,
			ContentLength: int64(len(response)),
		},
		body: body,
	}
}

func (m *mockUpstream) RoundTrip(req *http.Request) (*http.Response, error) {
	_, _ = io.Copy(io.Discard, req.Body)
	_ = req.Body.Close()
	m.body.Reset(m.body.data)
	m.resp.Request = req
	return m.resp, nil
}

// reusableBody is a resettable io.ReadCloser for mock responses.
type reusableBody struct {
	*bytes.Reader
	data []byte
}

// Close implements io.Closer.
func (r *reusableBody) Close() error { return nil }

// sinkWriter is a zero-allocation http.ResponseWriter that stands in for the
// client connection, so a benchmark measures the proxy rather than the
// response recorder's buffer growth.
type sinkWriter struct {
	header http.Header
	status int
}

// Header implements http.ResponseWriter.
func (s *sinkWriter) Header() http.Header { return s.header }

// Write implements http.ResponseWriter.
func (s *sinkWriter) Write(p []byte) (int, error) { return len(p), nil }

// WriteHeader implements http.ResponseWriter.
func (s *sinkWriter) WriteHeader(code int) { s.status = code }

// BenchmarkForward measures the proxy's own allocation footprint for a buffered
// round trip with a 1 KB body and a mock upstream. A fresh inbound request is
// built each iteration (that construction is part of the harness, not the
// proxy), the upstream is an in-process RoundTripper and the client is a
// zero-allocation ResponseWriter. A real socket round trip costs roughly 76
// allocs/op in the network stack alone, which would swamp the number this
// benchmark is about; BenchmarkForwardNetwork measures that end-to-end path.
//
// Target: at most 30 allocs/op.
func BenchmarkForward(b *testing.B) {
	const bodySize = 1024
	payload := bytes.Repeat([]byte("p"), bodySize)
	response := bytes.Repeat([]byte("r"), bodySize)

	f := NewForwarder()
	f.roundTripper = newMockUpstream(response)
	h := NewHandler(NewRouter().WithProvider(testHost, "http://upstream.example"), f, nil, nil)
	sink := &sinkWriter{header: make(http.Header, 8)}

	b.ReportAllocs()
	b.SetBytes(bodySize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "http://"+testHost+"/v1/chat/completions", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		clear(sink.header)
		sink.status = 0
		h.ServeHTTP(sink, req)
		if sink.status != http.StatusOK {
			b.Fatalf("status = %d, want 200", sink.status)
		}
	}
}

// BenchmarkForwardNetwork runs the same round trip over real loopback sockets,
// including the client, proxy and upstream server. It is informational: most of
// its allocations belong to net/http rather than to this package.
func BenchmarkForwardNetwork(b *testing.B) {
	const bodySize = 1024
	payload := bytes.Repeat([]byte("p"), bodySize)
	response := bytes.Repeat([]byte("r"), bodySize)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))
	defer upstream.Close()

	handler := NewHandler(NewRouter().WithProvider(testHost, upstream.URL), NewForwarder(), nil, nil)
	proxySrv := httptest.NewServer(handler)
	defer proxySrv.Close()

	client := proxySrv.Client()
	if tr, ok := client.Transport.(*http.Transport); ok {
		tr.MaxIdleConnsPerHost = 64
	}
	reqURL := proxySrv.URL + "/v1/chat/completions"

	b.ReportAllocs()
	b.SetBytes(bodySize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(payload))
		if err != nil {
			b.Fatalf("build request: %v", err)
		}
		req.Host = testHost
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			b.Fatalf("proxy request: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}
