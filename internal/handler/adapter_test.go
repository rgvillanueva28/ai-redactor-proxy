package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/local/redactor/internal/proxy"
)

// adapterDispatchBody carries the same text in every field a provider handler
// walks, plus metadata no provider handler is expected to touch. Which fields
// change therefore identifies the handler the adapter dispatched to: OpenAI
// walks only messages[].content, Anthropic also walks system, Gemini walks
// contents[].parts[].text, and the generic fallback walks every string leaf.
const adapterDispatchBody = `{"messages":[{"role":"user","content":"alice.smith@example.com"}],"contents":[{"parts":[{"text":"alice.smith@example.com"}]}],"system":"alice.smith@example.com","metadata":{"user_email":"bob@example.org"}}`

// newAdapterForTest builds an Adapter backed by the default detector set.
func newAdapterForTest(t *testing.T) *Adapter {
	t.Helper()
	reg := newProviderRegistry(t)
	return NewAdapter(reg.Redactor(), reg.Unredactor(), false, false)
}

// adapterContext stores p under the context key the proxy uses for a resolved
// provider. UpstreamProviderKey is exported, so using it here exercises the
// same lookup path ContextProvider takes in production.
func adapterContext(ctx context.Context, p proxy.Provider) context.Context {
	return context.WithValue(ctx, proxy.UpstreamProviderKey, p)
}

func TestAdapter_MutateRequest_Dispatch(t *testing.T) {
	adapter := newAdapterForTest(t)

	cases := []struct {
		name        string
		ctx         func(context.Context) context.Context
		wantPresent []string
		wantAbsent  []string
	}{
		{
			name: "openai",
			ctx: func(ctx context.Context) context.Context {
				return adapterContext(ctx, proxy.ProviderOpenAI)
			},
			wantPresent: []string{
				`"content":"<EMAIL_1>"`,
				`"system":"alice.smith@example.com"`,
				`"text":"alice.smith@example.com"`,
				`"user_email":"bob@example.org"`,
			},
			wantAbsent: []string{`"content":"alice.smith@example.com"`},
		},
		{
			name: "anthropic",
			ctx: func(ctx context.Context) context.Context {
				return adapterContext(ctx, proxy.ProviderAnthropic)
			},
			wantPresent: []string{
				`"content":"<EMAIL_1>"`,
				`"system":"<EMAIL_2>"`,
				`"text":"alice.smith@example.com"`,
				`"user_email":"bob@example.org"`,
			},
			wantAbsent: []string{`"system":"alice.smith@example.com"`},
		},
		{
			name: "gemini",
			ctx: func(ctx context.Context) context.Context {
				return adapterContext(ctx, proxy.ProviderGemini)
			},
			wantPresent: []string{
				`"text":"<EMAIL_1>"`,
				`"content":"alice.smith@example.com"`,
				`"system":"alice.smith@example.com"`,
				`"user_email":"bob@example.org"`,
			},
			wantAbsent: []string{`"text":"alice.smith@example.com"`},
		},
		{
			name: "generic",
			ctx: func(ctx context.Context) context.Context {
				return adapterContext(ctx, proxy.ProviderGeneric)
			},
			wantPresent: []string{
				`"content":"<EMAIL_1>"`,
				`"text":"<EMAIL_2>"`,
				`"system":"<EMAIL_3>"`,
				`"user_email":"<EMAIL_4>"`,
			},
			wantAbsent: []string{"alice.smith@example.com", "bob@example.org"},
		},
		{
			name: "unknown provider",
			ctx: func(ctx context.Context) context.Context {
				return adapterContext(ctx, proxy.Provider("mystery"))
			},
			wantPresent: []string{
				`"content":"<EMAIL_1>"`,
				`"user_email":"<EMAIL_4>"`,
			},
			wantAbsent: []string{"alice.smith@example.com", "bob@example.org"},
		},
		{
			name: "no provider in context",
			ctx:  func(ctx context.Context) context.Context { return ctx },
			wantPresent: []string{
				`"content":"<EMAIL_1>"`,
				`"user_email":"<EMAIL_4>"`,
			},
			wantAbsent: []string{"alice.smith@example.com", "bob@example.org"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(adapterDispatchBody)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(src))

			got, err := adapter.MutateRequest(tc.ctx(req.Context()), req, src)
			if err != nil {
				t.Fatalf("MutateRequest: %v", err)
			}
			if !json.Valid(got) {
				t.Fatalf("MutateRequest returned invalid JSON: %s", got)
			}
			if mappingFromContext(req.Context()) == nil {
				t.Fatal("MutateRequest did not publish a mapping on the request context")
			}

			for _, want := range tc.wantPresent {
				if !strings.Contains(string(got), want) {
					t.Errorf("redacted body is missing %s:\n%s", want, got)
				}
			}
			for _, unwanted := range tc.wantAbsent {
				if strings.Contains(string(got), unwanted) {
					t.Errorf("redacted body still contains %s:\n%s", unwanted, got)
				}
			}
		})
	}
}

func TestAdapter_MutateResponse_Dispatch(t *testing.T) {
	adapter := newAdapterForTest(t)

	cases := []struct {
		name       string
		provider   proxy.Provider
		request    string
		response   string
		unredacted string
	}{
		{"openai", proxy.ProviderOpenAI, "openai_request.json", "openai_response.json", "openai_response_unredacted.json"},
		{"anthropic", proxy.ProviderAnthropic, "anthropic_request.json", "anthropic_response.json", "anthropic_response_unredacted.json"},
		{"gemini", proxy.ProviderGemini, "gemini_request.json", "gemini_response.json", "gemini_response_unredacted.json"},
		{"generic", proxy.ProviderGeneric, "generic_request.json", "generic_response.json", "generic_response_unredacted.json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqSrc := fixture(t, tc.request)
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(reqSrc))

			// Build the mapping the way the proxy does, then hand the response
			// side a context that still carries the provider and the mapping.
			redacted, err := adapter.MutateRequest(adapterContext(req.Context(), tc.provider), req, reqSrc)
			if err != nil {
				t.Fatalf("MutateRequest: %v", err)
			}
			if !bytes.Contains(redacted, []byte("<EMAIL_1>")) {
				t.Fatalf("request fixture was not redacted: %s", redacted)
			}

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}
			ctx := adapterContext(req.Context(), tc.provider)

			got, err := adapter.MutateResponse(ctx, resp, fixture(t, tc.response))
			if err != nil {
				t.Fatalf("MutateResponse: %v", err)
			}
			want := fixture(t, tc.unredacted)
			if !bytes.Equal(got, want) {
				t.Fatalf("MutateResponse:\n got %s\nwant %s", got, want)
			}
		})
	}
}

func TestAdapter_MutateStreamChunk_Dispatch(t *testing.T) {
	adapter := newAdapterForTest(t)

	const anthropicChunk = "event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Email <EMAIL_1> for the receipt."}}` + "\n\n"
	const anthropicWant = "event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Email alice.smith@example.com for the receipt."}}` + "\n\n"

	const geminiSSEChunk = "event: message\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"Email <EMAIL_1>."}]}}]}` + "\n\n"
	const geminiSSEWant = "event: message\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"Email alice.smith@example.com."}]}}]}` + "\n\n"

	cases := []struct {
		name        string
		provider    proxy.Provider
		request     string
		chunk       []byte
		want        []byte
		contentType string
	}{
		{
			name:        "openai",
			provider:    proxy.ProviderOpenAI,
			request:     "openai_request.json",
			chunk:       rawFixture(t, "openai_sse_chunk.txt"),
			want:        rawFixture(t, "openai_sse_chunk_unredacted.txt"),
			contentType: "text/event-stream",
		},
		{
			name:        "anthropic",
			provider:    proxy.ProviderAnthropic,
			request:     "anthropic_request.json",
			chunk:       []byte(anthropicChunk),
			want:        []byte(anthropicWant),
			contentType: "text/event-stream",
		},
		{
			// The classic streaming endpoint: newline-delimited JSON, not SSE.
			name:        "gemini",
			provider:    proxy.ProviderGemini,
			request:     "gemini_request.json",
			chunk:       []byte(`{"candidates":[{"content":{"parts":[{"text":"Email <EMAIL_1>."}]}}]}`),
			want:        []byte(`{"candidates":[{"content":{"parts":[{"text":"Email alice.smith@example.com."}]}}]}`),
			contentType: "application/json",
		},
		{
			// The SSE-flavoured streaming endpoint: same payload, wrapped in
			// "event:"/"data:" framing.
			name:        "gemini sse",
			provider:    proxy.ProviderGemini,
			request:     "gemini_request.json",
			chunk:       []byte(geminiSSEChunk),
			want:        []byte(geminiSSEWant),
			contentType: "text/event-stream",
		},
		{
			name:        "generic",
			provider:    proxy.ProviderGeneric,
			request:     "generic_request.json",
			chunk:       []byte("data: {\"text\":\"Email <EMAIL_1> or call <PHONE_1>.\"}\n\n"),
			want:        []byte("data: {\"text\":\"Email alice.smith@example.com or call +1 415-555-2671.\"}\n\n"),
			contentType: "text/event-stream",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqSrc := fixture(t, tc.request)
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(reqSrc))

			if _, err := adapter.MutateRequest(adapterContext(req.Context(), tc.provider), req, reqSrc); err != nil {
				t.Fatalf("MutateRequest: %v", err)
			}
			if mappingFromContext(req.Context()) == nil {
				t.Fatal("MutateRequest did not publish a mapping")
			}

			ctx := adapterContext(req.Context(), tc.provider)
			got, err := adapter.MutateStreamChunk(ctx, tc.chunk, tc.contentType)
			if err != nil {
				t.Fatalf("MutateStreamChunk: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("MutateStreamChunk:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestAdapter_LogRedacted verifies that when the log_redacted option is on,
// every redacted item is logged at info level with its placeholder, category
// and original value.
func TestAdapter_LogRedacted(t *testing.T) {
	prev := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	reg := newProviderRegistry(t)
	adapter := NewAdapter(reg.Redactor(), reg.Unredactor(), true, false)

	const body = `{"messages":[{"role":"user","content":"alice.smith@example.com"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))

	if _, err := adapter.MutateRequest(adapterContext(req.Context(), proxy.ProviderOpenAI), req, []byte(body)); err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}

	logged := buf.String()
	for _, want := range []string{
		`"msg":"redacted"`,
		`"placeholder":"<EMAIL_1>"`,
		`"value":"alice.smith@example.com"`,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("log output is missing %s:\n%s", want, logged)
		}
	}
	// Categories are logged in their canonical form (pii.CategoryEmail is
	// "EMAIL"), so compare case-insensitively.
	if !strings.Contains(strings.ToLower(logged), `"category":"email"`) {
		t.Errorf("log output is missing the email category:\n%s", logged)
	}

	// With the option disabled the adapter must stay silent.
	before := buf.Len()
	quiet := NewAdapter(reg.Redactor(), reg.Unredactor(), false, false)
	quietReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	if _, err := quiet.MutateRequest(adapterContext(quietReq.Context(), proxy.ProviderOpenAI), quietReq, []byte(body)); err != nil {
		t.Fatalf("MutateRequest (disabled): %v", err)
	}
	if buf.Len() != before {
		t.Errorf("disabled log_redacted still logged %q", buf.String()[before:])
	}
}

// TestAdapter_LogBodies verifies that when the log_bodies option is on, the
// full request, response and stream chunk bodies are logged at debug level
// before and after the transformation, and that the option being off keeps the
// log silent.
func TestAdapter_LogBodies(t *testing.T) {
	prev := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	reg := newProviderRegistry(t)
	adapter := NewAdapter(reg.Redactor(), reg.Unredactor(), false, true)

	const reqBody = `{"messages":[{"role":"user","content":"alice.smith@example.com"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	if _, err := adapter.MutateRequest(adapterContext(req.Context(), proxy.ProviderOpenAI), req, []byte(reqBody)); err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}

	logged := buf.String()
	for _, want := range []string{
		`"msg":"request body (original)"`,
		`alice.smith@example.com`,
		`"msg":"request body (redacted)"`,
		`<EMAIL_1>`,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("request log is missing %s:\n%s", want, logged)
		}
	}

	// Each direction is checked against only the lines it produced, so a
	// matching string from an earlier stage cannot mask a missing log line.
	// The provider is re-attached because MutateRequest only publishes the
	// mapping on the request's own context, not the caller's.
	before := buf.Len()
	const respBody = `{"choices":[{"message":{"role":"assistant","content":"Email <EMAIL_1> please."}}]}`
	resp := &http.Response{StatusCode: http.StatusOK, Request: req}
	respCtx := adapterContext(req.Context(), proxy.ProviderOpenAI)
	if _, err := adapter.MutateResponse(respCtx, resp, []byte(respBody)); err != nil {
		t.Fatalf("MutateResponse: %v", err)
	}
	respLogged := buf.String()[before:]
	for _, want := range []string{
		`"msg":"response body (redacted)"`,
		`<EMAIL_1>`,
		`"msg":"response body (unredacted)"`,
		`alice.smith@example.com`,
	} {
		if !strings.Contains(respLogged, want) {
			t.Errorf("response log is missing %s:\n%s", want, respLogged)
		}
	}

	before = buf.Len()
	const chunk = "data: {\"choices\":[{\"delta\":{\"content\":\"Email <EMAIL_1> please.\"}}]}\n\n"
	if _, err := adapter.MutateStreamChunk(respCtx, []byte(chunk), "text/event-stream"); err != nil {
		t.Fatalf("MutateStreamChunk: %v", err)
	}
	chunkLogged := buf.String()[before:]
	for _, want := range []string{
		`"msg":"stream chunk (redacted)"`,
		`<EMAIL_1>`,
		`"msg":"stream chunk (unredacted)"`,
		`alice.smith@example.com`,
	} {
		if !strings.Contains(chunkLogged, want) {
			t.Errorf("stream chunk log is missing %s:\n%s", want, chunkLogged)
		}
	}

	// With the option disabled the adapter must not log bodies at all.
	before = buf.Len()
	quiet := NewAdapter(reg.Redactor(), reg.Unredactor(), false, false)
	quietReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	quietCtx := adapterContext(quietReq.Context(), proxy.ProviderOpenAI)
	if _, err := quiet.MutateRequest(quietCtx, quietReq, []byte(reqBody)); err != nil {
		t.Fatalf("MutateRequest (disabled): %v", err)
	}
	quietResp := &http.Response{StatusCode: http.StatusOK, Request: quietReq}
	quietRespCtx := adapterContext(quietReq.Context(), proxy.ProviderOpenAI)
	if _, err := quiet.MutateResponse(quietRespCtx, quietResp, []byte(respBody)); err != nil {
		t.Fatalf("MutateResponse (disabled): %v", err)
	}
	if _, err := quiet.MutateStreamChunk(quietRespCtx, []byte(chunk), "text/event-stream"); err != nil {
		t.Fatalf("MutateStreamChunk (disabled): %v", err)
	}
	if got := buf.String()[before:]; got != "" {
		t.Errorf("disabled log_bodies still logged %q", got)
	}
}
