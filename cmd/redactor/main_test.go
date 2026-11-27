package main

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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/handler"
	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
	"github.com/local/redactor/internal/rules"
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
	adapter := handler.NewAdapter(reg.Redactor(), reg.Unredactor(), false, false)

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

// TestE2E_SSE_Streaming_SplitPlaceholder streams the echoed placeholder one
// token at a time, the way a real model does, and verifies the proxy stitches it
// back together instead of forwarding the partial tokens.
func TestE2E_SSE_Streaming_SplitPlaceholder(t *testing.T) {
	const requestBody = `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"My email is alice@example.com"}]}`

	upstream, rec := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, part := range []string{"<", "EMAIL", "_1>", " now."} {
			fmt.Fprintf(w, `data: {"choices":[{"index":0,"delta":{"content":%q}}]}`+"\n\n", part)
			flusher.Flush()
		}
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

	var events []proxy.SSEEvent
	if err := proxy.ParseEvents(resp.Body, func(ev proxy.SSEEvent) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		t.Fatalf("parse SSE stream: %v", err)
	}

	joined := make([]string, len(events))
	for i, ev := range events {
		joined[i] = ev.Data
	}
	stream := strings.Join(joined, "\n")
	if strings.Contains(stream, "<EMAIL") {
		t.Errorf("streamed data leaked a partial placeholder: %s", stream)
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

func TestE2E_Gemini_SSE_Streaming(t *testing.T) {
	const requestBody = `{"contents":[{"parts":[{"text":"My email is alice@example.com"}]}]}`
	const path = "/v1beta/models/gemini-1.5-pro:streamGenerateContent"

	upstream, rec := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: message\n"+
			`data: {"candidates":[{"content":{"parts":[{"text":"Sure, email <EMAIL_1> now."}]}}]}`+"\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})
	proxySrv := newProxyServer(t, upstream.URL)

	req, err := http.NewRequest(http.MethodPost, proxySrv.URL+path, strings.NewReader(requestBody))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "generativelanguage.googleapis.com"
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
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(events), events)
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

	seen, upstreamPath, _, _ := rec.received()
	if upstreamPath != path {
		t.Errorf("upstream path = %q, want %q", upstreamPath, path)
	}
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
	adapter := handler.NewAdapter(reg.Redactor(), reg.Unredactor(), false, false)
	proxyHandler := proxy.NewHandler(
		proxy.NewRouter(),
		proxy.NewForwarder(),
		[]proxy.RequestMutator{adapter},
		[]proxy.ResponseMutator{adapter},
	)

	srv := proxy.NewServer(addr, newMux(proxyHandler, config.DefaultConfig(), nil))
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

// markerProxy stands in for the proxy handler in routing tests. It flags every
// request that reaches the catch-all route so tests can prove a path was not
// captured by a more specific pattern.
func markerProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handled-By", "proxy")
		http.NotFound(w, r)
	})
}

func TestNewMuxServesUIWhenEnabled(t *testing.T) {
	mux := newMux(markerProxy(), config.DefaultConfig(), nil)

	for _, path := range []string{"/logs", "/logs/"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("GET %s Content-Type = %q, want text/html; charset=utf-8", path, ct)
		}
		if !strings.Contains(rec.Body.String(), "<title>Redactor") {
			t.Errorf("GET %s did not serve the viewer page", path)
		}
		if got := rec.Header().Get("X-Handled-By"); got != "" {
			t.Errorf("GET %s reached the proxy handler (%s), want the UI handler", path, got)
		}
	}

	assets := map[string]string{
		"/logs/app.js":    "text/javascript; charset=utf-8",
		"/logs/style.css": "text/css; charset=utf-8",
	}
	for path, wantCT := range assets {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != wantCT {
			t.Errorf("GET %s Content-Type = %q, want %q", path, ct, wantCT)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s served an empty body", path)
		}
		if got := rec.Header().Get("X-Handled-By"); got != "" {
			t.Errorf("GET %s reached the proxy handler (%s), want the UI handler", path, got)
		}
	}
}

// TestNewMuxServesSSEWhenEnabled proves /logs/events resolves to the SSE
// handler rather than the UI subtree. The request context is cancelled up front
// so the streaming handler returns immediately after writing its headers.
func TestNewMuxServesSSEWhenEnabled(t *testing.T) {
	mux := newMux(markerProxy(), config.DefaultConfig(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/logs/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /logs/events status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("GET /logs/events Content-Type = %q, want text/event-stream", ct)
	}
	if got := rec.Header().Get("X-Handled-By"); got != "" {
		t.Errorf("GET /logs/events reached the proxy handler (%s), want the SSE handler", got)
	}
}

func TestNewMuxLogsHiddenWhenUIDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UI.Enable = false
	mux := newMux(markerProxy(), cfg, nil)

	paths := []string{"/logs", "/logs/", "/logs/events", "/logs/app.js", "/logs/style.css"}
	for _, path := range paths {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 from the proxy catch-all", path, rec.Code)
		}
		if got := rec.Header().Get("X-Handled-By"); got != "proxy" {
			t.Errorf("GET %s X-Handled-By = %q, want the proxy handler", path, got)
		}
	}
}

// newConfigAPIMux wires the config API exactly as the binary does, backed by a
// temp config file, and returns the live registry so tests can observe reloads.
func newConfigAPIMux(t *testing.T, cfg *config.Config) (*http.ServeMux, *pii.Registry, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("save config: %v", err)
	}
	reg, err := pii.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("pii.NewRegistry: %v", err)
	}
	adapter := handler.NewAdapter(reg.Redactor(), reg.Unredactor(), cfg.PII.LogRedacted, cfg.PII.LogBodies)
	api := rules.NewHandler(cfg, path, reg, adapter)
	return newMux(markerProxy(), cfg, api), reg, path
}

// doAPIRequest performs one request against the config API.
func doAPIRequest(t *testing.T, client *http.Client, method, url string, body []byte) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// mustMarshalJSON marshals v for an API request body.
func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}

// assertAPISecurityHeaders checks the headers every API response must carry.
func assertAPISecurityHeaders(t *testing.T, resp *http.Response) {
	t.Helper()
	if got := resp.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// decodeAPIResponse decodes and closes an API response body.
func decodeAPIResponse(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// previewAPIResponse mirrors the preview endpoint's wire shape without
// depending on package rules' unexported types.
type previewAPIResponse struct {
	Redacted string `json:"redacted"`
	Matches  []struct {
		Category    string `json:"category"`
		Start       int    `json:"start"`
		End         int    `json:"end"`
		Replacement string `json:"replacement"`
	} `json:"matches"`
}

// TestNewMuxConfigAPI exercises the full end-to-end flow through the same mux
// the binary installs: read, write, preview, and reload, including the effect
// each one has on the live redactor.
func TestNewMuxConfigAPI(t *testing.T) {
	cfg := config.DefaultConfig()
	mux, reg, path := newConfigAPIMux(t, cfg)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := srv.Client()

	// GET returns the running config as JSON.
	resp := doAPIRequest(t, client, http.MethodGet, srv.URL+"/api/config", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/config status = %d, want 200: %s", resp.StatusCode, readBody(t, resp))
	}
	assertAPISecurityHeaders(t, resp)
	var current config.Config
	decodeAPIResponse(t, resp, &current)
	if current.Listen.Address != cfg.Listen.Address {
		t.Errorf("GET listen.address = %q, want %q", current.Listen.Address, cfg.Listen.Address)
	}

	// PUT persists the document and applies it to the live registry at once.
	updated := config.DefaultConfig()
	updated.Listen.Address = "127.0.0.1:9998"
	updated.PII.EnableEmail = false
	updated.PII.EnableDomain = false

	resp = doAPIRequest(t, client, http.MethodPut, srv.URL+"/api/config", mustMarshalJSON(t, updated))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/config status = %d, want 200: %s", resp.StatusCode, readBody(t, resp))
	}
	assertAPISecurityHeaders(t, resp)
	var applied config.Config
	decodeAPIResponse(t, resp, &applied)
	if applied.Listen.Address != "127.0.0.1:9998" {
		t.Errorf("PUT response listen.address = %q", applied.Listen.Address)
	}
	if applied.PII.EnableEmail {
		t.Error("PUT response enable_email = true, want false")
	}

	onDisk, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload written config: %v", err)
	}
	if onDisk.PII.EnableEmail {
		t.Error("on-disk enable_email = true, want false")
	}
	if onDisk.Listen.Address != "127.0.0.1:9998" {
		t.Errorf("on-disk listen.address = %q", onDisk.Listen.Address)
	}

	redacted, mapping := reg.Redactor().Redact("alice@example.com")
	pii.PutMapping(mapping)
	if redacted != "alice@example.com" {
		t.Errorf("live redaction after PUT = %q, want the original text", redacted)
	}

	// GET reflects the new config.
	resp = doAPIRequest(t, client, http.MethodGet, srv.URL+"/api/config", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET after PUT status = %d, want 200", resp.StatusCode)
	}
	var afterPut config.Config
	decodeAPIResponse(t, resp, &afterPut)
	if afterPut.Listen.Address != "127.0.0.1:9998" || afterPut.PII.EnableEmail {
		t.Errorf("GET after PUT = %+v", afterPut)
	}

	// Preview GET uses the running (email-disabled) config.
	text := "My email is alice@example.com"
	resp = doAPIRequest(t, client, http.MethodGet, srv.URL+"/api/config/preview?text="+url.QueryEscape(text), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET preview status = %d, want 200: %s", resp.StatusCode, readBody(t, resp))
	}
	assertAPISecurityHeaders(t, resp)
	var preview previewAPIResponse
	decodeAPIResponse(t, resp, &preview)
	if strings.Contains(preview.Redacted, "<EMAIL_1>") {
		t.Errorf("preview redacted an email while email detection is disabled: %q", preview.Redacted)
	}
	if len(preview.Matches) != 0 {
		t.Errorf("preview matches = %+v, want none", preview.Matches)
	}

	// Preview POST can use a different config without touching live state.
	previewBody := mustMarshalJSON(t, map[string]any{
		"text":   text,
		"config": config.DefaultConfig(),
	})
	resp = doAPIRequest(t, client, http.MethodPost, srv.URL+"/api/config/preview", previewBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST preview status = %d, want 200: %s", resp.StatusCode, readBody(t, resp))
	}
	assertAPISecurityHeaders(t, resp)
	decodeAPIResponse(t, resp, &preview)
	if !strings.Contains(preview.Redacted, "<EMAIL_1>") {
		t.Errorf("preview redacted = %q, want an email placeholder", preview.Redacted)
	}
	if len(preview.Matches) != 1 {
		t.Fatalf("preview matches = %+v, want 1", preview.Matches)
	}
	m := preview.Matches[0]
	if m.Category != "EMAIL" || text[m.Start:m.End] != "alice@example.com" || m.Replacement != "<EMAIL_1>" {
		t.Errorf("preview match = %+v", m)
	}

	// The supplied preview config must not leak into live state.
	redacted, mapping = reg.Redactor().Redact("alice@example.com")
	pii.PutMapping(mapping)
	if redacted != "alice@example.com" {
		t.Errorf("preview changed live redaction to %q", redacted)
	}

	// Reload re-reads the file from disk and applies it.
	if err := config.DefaultConfig().Save(path); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	resp = doAPIRequest(t, client, http.MethodPost, srv.URL+"/api/config/reload", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST reload status = %d, want 200: %s", resp.StatusCode, readBody(t, resp))
	}
	assertAPISecurityHeaders(t, resp)
	var reloaded config.Config
	decodeAPIResponse(t, resp, &reloaded)
	if !reloaded.PII.EnableEmail {
		t.Error("reload response enable_email = false, want true")
	}
	redacted, mapping = reg.Redactor().Redact("alice@example.com")
	pii.PutMapping(mapping)
	if !strings.Contains(redacted, "<EMAIL_1>") {
		t.Errorf("live redaction after reload = %q, want an email placeholder", redacted)
	}

	// Invalid configs are rejected with the offending field named.
	invalid := config.DefaultConfig()
	invalid.Listen.Address = ""
	resp = doAPIRequest(t, client, http.MethodPut, srv.URL+"/api/config", mustMarshalJSON(t, invalid))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid PUT status = %d, want 400: %s", resp.StatusCode, readBody(t, resp))
	}
	assertAPISecurityHeaders(t, resp)
	var apiErr struct {
		Error string `json:"error"`
		Field string `json:"field"`
	}
	decodeAPIResponse(t, resp, &apiErr)
	if apiErr.Field != "listen.address" {
		t.Errorf("invalid PUT field = %q, want listen.address", apiErr.Field)
	}

	// Unsupported methods are rejected without reaching the proxy catch-all.
	resp = doAPIRequest(t, client, http.MethodDelete, srv.URL+"/api/config", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /api/config status = %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != "GET, PUT" {
		t.Errorf("DELETE /api/config Allow = %q, want %q", got, "GET, PUT")
	}
	if got := resp.Header.Get("X-Handled-By"); got != "" {
		t.Errorf("DELETE /api/config reached the proxy handler (%s)", got)
	}
	_ = readBody(t, resp)
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
