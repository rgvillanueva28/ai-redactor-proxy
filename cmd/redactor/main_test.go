package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/handler"
	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
)

// e2eUpstream records the last request it received and replies with whatever
// its handler writes. The proxy tests assert on the recorded body to prove PII
// never reached the upstream.
type e2eUpstream struct {
	mu     sync.Mutex
	body   []byte
	path   string
	query  string
	header http.Header
}

// record copies r's body and metadata into the receiver.
func (u *e2eUpstream) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.body = body
	u.path = r.URL.Path
	u.query = r.URL.RawQuery
	u.header = r.Header.Clone()
}

// received returns a snapshot of the last recorded request.
func (u *e2eUpstream) received() (body []byte, path, query string, header http.Header) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.body, u.path, u.query, u.header
}

// newUpstream starts a mock provider API that records every request and replies
// with respond. It returns the server and its recorder.
func newUpstream(t *testing.T, respond http.HandlerFunc) (*httptest.Server, *e2eUpstream) {
	t.Helper()
	rec := &e2eUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// newProxyServer wires the full proxy stack (registry, adapter, router,
// forwarder, handler) exactly as the binary does, but points every upstream dial
// at upstreamURL so the tests stay off the network. Requests are routed by the
// Host header, letting each test exercise a specific provider handler.
func newProxyServer(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()

	reg, err := pii.NewRegistry(config.DefaultConfig())
	if err != nil {
		t.Fatalf("pii.NewRegistry: %v", err)
	}
	adapter := handler.NewAdapter(reg.Redactor(), reg.Unredactor())

	// Use plain http upstream URLs so the redirected dial does not attempt TLS.
	router := proxy.NewRouter().
		WithProvider("api.openai.com", "http://api.openai.com").
		WithProvider("api.anthropic.com", "http://api.anthropic.com").
		WithProvider("generativelanguage.googleapis.com", "http://generativelanguage.googleapis.com").
		WithProvider("api.example.com", "http://api.example.com")

	// Redirect every outbound connection to the mock upstream. The request's
	// URL still carries the provider host, so routing and provider detection
	// behave exactly as in production.
	upstreamAddr := strings.TrimPrefix(upstreamURL, "http://")
	forwarder := proxy.NewForwarder()
	forwarder.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstreamAddr)
		},
	}

	h := proxy.NewHandler(router, forwarder, []proxy.RequestMutator{adapter}, []proxy.ResponseMutator{adapter})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// postJSON sends a JSON POST to the proxy with the Host header set to host, so
// the router resolves it to the matching upstream.
func postJSON(t *testing.T, proxyURL, path, host, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, proxyURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
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

// assertRedactedUpstream fails the test unless the upstream body contains
// placeholders and none of the original PII.
func assertRedactedUpstream(t *testing.T, body []byte, wanted []string, originals []string) {
	t.Helper()
	s := string(body)
	for _, ph := range wanted {
		if !strings.Contains(s, ph) {
			t.Errorf("upstream body missing placeholder %q: %s", ph, s)
		}
	}
	for _, original := range originals {
		if strings.Contains(s, original) {
			t.Errorf("upstream body leaked original PII %q: %s", original, s)
		}
	}
}

func TestE2E_OpenAI(t *testing.T) {
	const requestBody = `{"model":"gpt-4o","messages":[{"role":"user","content":"My email is alice@example.com"}]}`

	upstream, rec := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"I will email <EMAIL_1> shortly."},"finish_reason":"stop"}]}`)
	})
	proxySrv := newProxyServer(t, upstream.URL)

	resp := postJSON(t, proxySrv.URL, "/v1/chat/completions", "api.openai.com", requestBody)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}

	seen, path, _, _ := rec.received()
	if path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", path)
	}
	assertRedactedUpstream(t, seen, []string{"<EMAIL_1>"}, []string{"alice@example.com"})

	if strings.Contains(body, "<EMAIL_1>") {
		t.Errorf("client response still contains placeholder: %s", body)
	}
	if !strings.Contains(body, "alice@example.com") {
		t.Errorf("client response did not restore the original email: %s", body)
	}
}

func TestE2E_Anthropic(t *testing.T) {
	const requestBody = `{"model":"claude-3-sonnet","max_tokens":256,"system":"Escalate to alice@example.com","messages":[{"role":"user","content":"My SSN is 123-45-6789"}]}`

	upstream, rec := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"Contact <EMAIL_1>; SSN <SSN_1>."}]}`)
	})
	proxySrv := newProxyServer(t, upstream.URL)

	resp := postJSON(t, proxySrv.URL, "/v1/messages", "api.anthropic.com", requestBody)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}

	seen, path, _, _ := rec.received()
	if path != "/v1/messages" {
		t.Errorf("upstream path = %q, want /v1/messages", path)
	}
	assertRedactedUpstream(t, seen, []string{"<EMAIL_1>", "<SSN_1>"}, []string{"alice@example.com", "123-45-6789"})

	for _, want := range []string{"alice@example.com", "123-45-6789"} {
		if !strings.Contains(body, want) {
			t.Errorf("client response missing restored value %q: %s", want, body)
		}
	}
	if strings.Contains(body, "<EMAIL_1>") || strings.Contains(body, "<SSN_1>") {
		t.Errorf("client response still contains a placeholder: %s", body)
	}
}

func TestE2E_Gemini(t *testing.T) {
	const requestBody = `{"contents":[{"parts":[{"text":"My email is alice@example.com"}]}]}`

	upstream, rec := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"Sure, email <EMAIL_1>."}]}}]}`)
	})
	proxySrv := newProxyServer(t, upstream.URL)

	resp := postJSON(t, proxySrv.URL, "/v1beta/models/gemini-1.5-pro:generateContent", "generativelanguage.googleapis.com", requestBody)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}

	seen, path, _, _ := rec.received()
	if path != "/v1beta/models/gemini-1.5-pro:generateContent" {
		t.Errorf("upstream path = %q", path)
	}
	assertRedactedUpstream(t, seen, []string{"<EMAIL_1>"}, []string{"alice@example.com"})

	if !strings.Contains(body, "alice@example.com") {
		t.Errorf("client response did not restore the original email: %s", body)
	}
	if strings.Contains(body, "<EMAIL_1>") {
		t.Errorf("client response still contains placeholder: %s", body)
	}
}

func TestE2E_SSE_Streaming(t *testing.T) {
	const requestBody = `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"My email is alice@example.com"}]}`

	upstream, rec := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"Email <EMAIL_1> now."}}]}`+"\n\n")
		flusher.Flush()
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})
	proxySrv := newProxyServer(t, upstream.URL)

	req, err := http.NewRequest(http.MethodPost, proxySrv.URL+"/v1/chat/completions", strings.NewReader(requestBody))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "api.openai.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	var events []proxy.SSEEvent
	if err := proxy.ParseEvents(resp.Body, func(ev proxy.SSEEvent) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		t.Fatalf("parse SSE stream: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(events), events)
	}

	joined := make([]string, len(events))
	for i, ev := range events {
		joined[i] = ev.Data
	}
	stream := strings.Join(joined, "\n")
	if strings.Contains(stream, "<EMAIL_1>") {
		t.Errorf("streamed data still contains placeholder: %s", stream)
	}
	if !strings.Contains(stream, "alice@example.com") {
		t.Errorf("streamed data did not restore the original email: %s", stream)
	}
	if !strings.Contains(stream, "[DONE]") {
		t.Errorf("stream is missing the [DONE] sentinel: %s", stream)
	}

	seen, _, _, _ := rec.received()
	assertRedactedUpstream(t, seen, []string{"<EMAIL_1>"}, []string{"alice@example.com"})
}

func TestE2E_GenericFallback(t *testing.T) {
	const requestBody = `{"prompt":"Email alice@example.com about the invoice"}`

	upstream, rec := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"completion":"I will contact <EMAIL_1>."}`)
	})
	proxySrv := newProxyServer(t, upstream.URL)

	resp := postJSON(t, proxySrv.URL, "/v1/complete", "api.example.com", requestBody)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}

	seen, path, _, _ := rec.received()
	if path != "/v1/complete" {
		t.Errorf("upstream path = %q, want /v1/complete", path)
	}
	assertRedactedUpstream(t, seen, []string{"<EMAIL_1>"}, []string{"alice@example.com"})

	if !strings.Contains(body, "alice@example.com") {
		t.Errorf("client response did not restore the original email: %s", body)
	}
	if strings.Contains(body, "<EMAIL_1>") {
		t.Errorf("client response still contains placeholder: %s", body)
	}
}

func TestE2E_HealthEndpoint(t *testing.T) {
	addr := freePort(t)

	reg, err := pii.NewRegistry(config.DefaultConfig())
	if err != nil {
		t.Fatalf("pii.NewRegistry: %v", err)
	}
	adapter := handler.NewAdapter(reg.Redactor(), reg.Unredactor())
	proxyHandler := proxy.NewHandler(
		proxy.NewRouter(),
		proxy.NewForwarder(),
		[]proxy.RequestMutator{adapter},
		[]proxy.ResponseMutator{adapter},
	)

	srv := proxy.NewServer(addr, newMux(proxyHandler))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startErr := make(chan error, 1)
	go func() { startErr <- srv.Start(ctx) }()
	waitForListener(t, addr)

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body != "ok" {
		t.Fatalf("body = %q, want %q", body, "ok")
	}

	cancel()
	select {
	case err := <-startErr:
		if err != nil {
			t.Fatalf("server Start returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down after cancellation")
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
