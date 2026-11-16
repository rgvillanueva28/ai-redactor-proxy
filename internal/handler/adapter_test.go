package handler

import (
	"bytes"
	"context"
	"encoding/json"
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
	return NewAdapter(reg.Redactor(), reg.Unredactor())
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
			name:        "gemini",
			provider:    proxy.ProviderGemini,
			request:     "gemini_request.json",
			chunk:       []byte(`{"candidates":[{"content":{"parts":[{"text":"Email <EMAIL_1>."}]}}]}`),
			want:        []byte(`{"candidates":[{"content":{"parts":[{"text":"Email alice.smith@example.com."}]}}]}`),
			contentType: "application/json",
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
