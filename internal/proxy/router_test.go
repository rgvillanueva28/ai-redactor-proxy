package proxy

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestRouterResolve(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		url          string
		auth         string
		wantProvider Provider
		wantUpstream string
	}{
		{
			name:         "openai host",
			method:       "POST",
			url:          "https://api.openai.com/v1/chat/completions",
			wantProvider: ProviderOpenAI,
			wantUpstream: "https://api.openai.com",
		},
		{
			name:         "openai host with port and mixed case",
			method:       "POST",
			url:          "https://API.OPENAI.COM:28443/v1/chat/completions",
			wantProvider: ProviderOpenAI,
			wantUpstream: "https://api.openai.com",
		},
		{
			name:         "anthropic host",
			method:       "POST",
			url:          "https://api.anthropic.com/v1/messages",
			wantProvider: ProviderAnthropic,
			wantUpstream: "https://api.anthropic.com",
		},
		{
			name:         "gemini host",
			method:       "POST",
			url:          "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent",
			wantProvider: ProviderGemini,
			wantUpstream: "https://generativelanguage.googleapis.com",
		},
		{
			name:         "generic fallback unknown host",
			method:       "POST",
			url:          "https://unknown.example.com/v1/chat/completions?trace=1",
			wantProvider: ProviderGeneric,
			wantUpstream: "https://unknown.example.com/v1/chat/completions?trace=1",
		},
		{
			name:         "gemini by path prefix",
			method:       "POST",
			url:          "https://proxy.example.com/v1beta/models/gemini-pro:generateContent",
			wantProvider: ProviderGemini,
			wantUpstream: "https://generativelanguage.googleapis.com",
		},
		{
			name:         "anthropic by auth token",
			method:       "POST",
			url:          "https://proxy.example.com/v1/messages",
			auth:         "Bearer sk-ant-api002-abcdef",
			wantProvider: ProviderAnthropic,
			wantUpstream: "https://api.anthropic.com",
		},
		{
			name:         "openai by auth token",
			method:       "POST",
			url:          "https://proxy.example.com/v1/chat/completions",
			auth:         "Bearer sk-proj-abcdef",
			wantProvider: ProviderOpenAI,
			wantUpstream: "https://api.openai.com",
		},
		{
			name:         "auth scheme is case insensitive",
			method:       "POST",
			url:          "https://proxy.example.com/v1/messages",
			auth:         "bearer sk-ant-api003-abcdef",
			wantProvider: ProviderAnthropic,
			wantUpstream: "https://api.anthropic.com",
		},
		{
			name:         "unknown auth token falls through to generic",
			method:       "POST",
			url:          "https://proxy.example.com/v1/custom",
			auth:         "Bearer opaque-token",
			wantProvider: ProviderGeneric,
			wantUpstream: "https://proxy.example.com/v1/custom",
		},
		{
			name:         "unknown host no auth unknown path",
			method:       "GET",
			url:          "https://localhost:8080/health",
			wantProvider: ProviderGeneric,
			wantUpstream: "https://localhost:8080/health",
		},
	}

	router := NewRouter()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.url, nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			got := router.Resolve(req)
			if got.Provider != tt.wantProvider {
				t.Fatalf("Provider = %q, want %q", got.Provider, tt.wantProvider)
			}
			if got.UpstreamURL != tt.wantUpstream {
				t.Fatalf("UpstreamURL = %q, want %q", got.UpstreamURL, tt.wantUpstream)
			}
			// Resolve must not mutate the request.
			if req.Host == "" {
				t.Fatal("Resolve mutated request host")
			}
		})
	}
}

func TestRouterZeroValue(t *testing.T) {
	var router Router
	req := httptest.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	got := router.Resolve(req)
	if got.Provider != ProviderAnthropic || got.UpstreamURL != "https://api.anthropic.com" {
		t.Fatalf("Resolve = %+v, want anthropic route", got)
	}
}

func TestRouterResolveNilRequest(t *testing.T) {
	var router Router
	if got := router.Resolve(nil); got.Provider != ProviderGeneric {
		t.Fatalf("Provider = %q, want generic", got.Provider)
	}
}

func TestRouterWithProvider(t *testing.T) {
	router := NewRouter().
		WithProvider("llm.internal", "https://llm.internal:28443/").
		WithProvider("mirror-openai.example.com", "https://api.openai.com/v1/")

	tests := []struct {
		name         string
		url          string
		wantProvider Provider
		wantUpstream string
	}{
		{
			name:         "custom generic host",
			url:          "https://llm.internal/v1/chat",
			wantProvider: ProviderGeneric,
			wantUpstream: "https://llm.internal:28443",
		},
		{
			name:         "custom host infers openai from upstream",
			url:          "https://mirror-openai.example.com/v1/chat/completions",
			wantProvider: ProviderOpenAI,
			wantUpstream: "https://api.openai.com/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := router.Resolve(httptest.NewRequest("POST", tt.url, nil))
			if got.Provider != tt.wantProvider {
				t.Fatalf("Provider = %q, want %q", got.Provider, tt.wantProvider)
			}
			if got.UpstreamURL != tt.wantUpstream {
				t.Fatalf("UpstreamURL = %q, want %q", got.UpstreamURL, tt.wantUpstream)
			}
		})
	}
}

func TestRouterWithProviderOverridesDefault(t *testing.T) {
	router := NewRouter().WithProvider("api.openai.com", "https://openai-mirror.internal")
	got := router.Resolve(httptest.NewRequest("POST", "https://api.openai.com/v1/chat/completions", nil))
	if got.Provider != ProviderOpenAI {
		t.Fatalf("Provider = %q, want openai", got.Provider)
	}
	if got.UpstreamURL != "https://openai-mirror.internal" {
		t.Fatalf("UpstreamURL = %q, want override", got.UpstreamURL)
	}

	// The longest matching host substring wins, so a broad mapping cannot
	// shadow the more specific default.
	router = NewRouter().WithProvider("openai.com", "https://broad.example.com")
	got = router.Resolve(httptest.NewRequest("POST", "https://api.openai.com/v1/chat/completions", nil))
	if got.UpstreamURL != "https://api.openai.com" {
		t.Fatalf("UpstreamURL = %q, want most specific default", got.UpstreamURL)
	}
}

func TestRouterWithProviderIgnoresEmptyInput(t *testing.T) {
	router := NewRouter().WithProvider("", "https://ignored.example.com").WithProvider("empty-upstream.example.com", "")
	got := router.Resolve(httptest.NewRequest("GET", "https://empty-upstream.example.com/x", nil))
	if got.Provider != ProviderGeneric || got.UpstreamURL != "https://empty-upstream.example.com/x" {
		t.Fatalf("Resolve = %+v, want generic fallback", got)
	}
}

func TestContextProvider(t *testing.T) {
	if got := ContextProvider(context.Background()); got != ProviderGeneric {
		t.Fatalf("ContextProvider(background) = %q, want generic", got)
	}
	ctx := contextWithProvider(context.Background(), ProviderGemini)
	if got := ContextProvider(ctx); got != ProviderGemini {
		t.Fatalf("ContextProvider = %q, want gemini", got)
	}
	//nolint:staticcheck // nil context is explicitly supported.
	if got := ContextProvider(nil); got != ProviderGeneric {
		t.Fatalf("ContextProvider(nil) = %q, want generic", got)
	}
}

func TestProviderFromAuth(t *testing.T) {
	tests := []struct {
		header string
		want   Provider
		ok     bool
	}{
		{"", "", false},
		{"Basic abc", "", false},
		{"Bearer ", "", false},
		{"Bearer sk-ant-api002-x", ProviderAnthropic, true},
		{"Bearer sk-x", ProviderOpenAI, true},
		{"Bearer sk-ant-", ProviderAnthropic, true},
	}
	for _, tt := range tests {
		got, ok := providerFromAuth(tt.header)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("providerFromAuth(%q) = (%q, %v), want (%q, %v)", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}

func TestRouterConcurrentUse(t *testing.T) {
	router := NewRouter()
	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			router.WithProvider(fmt.Sprintf("host%d.example.com", i), fmt.Sprintf("https://upstream%d.example.com", i))
			for j := 0; j < 100; j++ {
				req := httptest.NewRequest("POST", "https://api.openai.com/v1/chat/completions", nil)
				if got := router.Resolve(req); got.Provider != ProviderOpenAI {
					t.Errorf("Provider = %q, want openai", got.Provider)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		req := httptest.NewRequest("GET", fmt.Sprintf("https://host%d.example.com/x", i), nil)
		if got := router.Resolve(req); got.UpstreamURL != fmt.Sprintf("https://upstream%d.example.com", i) {
			t.Fatalf("host%d UpstreamURL = %q", i, got.UpstreamURL)
		}
	}
}
