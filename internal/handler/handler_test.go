package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
)

// rawFixture reads a testdata file verbatim. Unlike fixture it does not trim
// whitespace, which matters for stream fixtures: SSE framing (the blank line
// that terminates an event) is part of the bytes under test.
func rawFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// newProviderRegistry builds a registry backed by the default detector set, so
// the provider fixtures exercise the real email/phone/card/SSN detectors.
func newProviderRegistry(t *testing.T) *pii.Registry {
	t.Helper()
	reg, err := pii.NewRegistry(config.DefaultConfig())
	if err != nil {
		t.Fatalf("pii.NewRegistry: %v", err)
	}
	return reg
}

// mutateProviderRequest runs MutateRequest and returns the mapping it published
// on the request context.
func mutateProviderRequest(t *testing.T, h proxy.RequestMutator, src []byte) (*pii.Mapping, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(src))
	got, err := h.MutateRequest(req.Context(), req, src)
	if err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}
	m := mappingFromContext(req.Context())
	if m == nil {
		t.Fatal("MutateRequest did not store a mapping on the request context")
	}
	return m, got
}

// jsonResponse is a plain buffered upstream response for MutateResponse.
func jsonResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func TestOpenAI_MutateRequest(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewOpenAIHandler(reg.Redactor(), reg.Unredactor(), false, false)

	src := fixture(t, "openai_request.json")
	want := fixture(t, "openai_request_redacted.json")

	m, got := mutateProviderRequest(t, h, src)
	if !bytes.Equal(got, want) {
		t.Fatalf("redacted body:\n got %s\nwant %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatalf("redacted body is not valid JSON: %s", got)
	}

	// Every placeholder must map back to its own value.
	for ph, wantVal := range map[string]string{
		"<EMAIL_1>": "alice.smith@example.com",
		"<PHONE_1>": "+1 415-555-2671",
		"<SSN_1>":   "123-45-6789",
		"<CC_1>":    "4111 1111 1111 1111",
	} {
		if gotVal, ok := m.Lookup(ph); !ok || gotVal != wantVal {
			t.Errorf("mapping[%s] = %q (present=%v), want %q", ph, gotVal, ok, wantVal)
		}
	}

	// Fields outside messages[].content are not walked, so a PII-looking value
	// in metadata survives (the upstream may require it verbatim).
	if !bytes.Contains(got, []byte(`"user_email":"bob@example.org"`)) {
		t.Errorf("non-content metadata field was rewritten: %s", got)
	}
	// Structure and numeric precision survive.
	for _, keep := range []string{
		`"seed":9007199254740993`,
		`"type":"image_url"`,
		`"url":"https://example.com/pic.png"`,
	} {
		if !bytes.Contains(got, []byte(keep)) {
			t.Errorf("redacted body lost %s: %s", keep, got)
		}
	}
}

func TestOpenAI_MutateRequest_UniquePlaceholdersAcrossMessages(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewOpenAIHandler(reg.Redactor(), reg.Unredactor(), false, false)

	src := []byte(`{"messages":[{"role":"user","content":"first@example.com"},{"role":"user","content":[{"type":"text","text":"second@example.com"}]}]}`)
	want := `{"messages":[{"role":"user","content":"<EMAIL_1>"},{"role":"user","content":[{"type":"text","text":"<EMAIL_2>"}]}]}`

	_, got := mutateProviderRequest(t, h, src)
	if string(got) != want {
		t.Fatalf("redacted body = %s, want %s", got, want)
	}
}

func TestOpenAI_MutateRequest_PassThrough(t *testing.T) {
	h := NewOpenAIHandler(nil, nil, false, false)

	t.Run("empty body", func(t *testing.T) {
		got, err := h.MutateRequest(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("MutateRequest: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %q, want empty body", got)
		}
	})

	t.Run("non-JSON body", func(t *testing.T) {
		src := []byte("--boundary\r\ncontent\r\n--boundary--\r\n")
		got, err := h.MutateRequest(context.Background(), nil, src)
		if err != nil {
			t.Fatalf("MutateRequest: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("non-JSON body changed: %q", got)
		}
	})

	t.Run("nil handler", func(t *testing.T) {
		var nilHandler *OpenAIHandler
		src := []byte(`{"messages":[]}`)
		if got, err := nilHandler.MutateRequest(context.Background(), nil, src); err != nil || !bytes.Equal(got, src) {
			t.Fatalf("nil handler mutated body: %s (err %v)", got, err)
		}
	})
}

func TestOpenAI_MutateResponse(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewOpenAIHandler(reg.Redactor(), reg.Unredactor(), false, false)

	m, _ := mutateProviderRequest(t, h, fixture(t, "openai_request.json"))

	body := fixture(t, "openai_response.json")
	want := fixture(t, "openai_response_unredacted.json")

	got, err := h.MutateResponse(contextWithMapping(context.Background(), m), jsonResponse(), body)
	if err != nil {
		t.Fatalf("MutateResponse: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("unredacted body:\n got %s\nwant %s", got, want)
	}
	for _, leaked := range []string{"<EMAIL_1>", "<PHONE_1>"} {
		if bytes.Contains(got, []byte(leaked)) {
			t.Errorf("unredacted body still contains %q", leaked)
		}
	}
	// A null content (a filtered choice) is preserved, not rewritten to "".
	if !bytes.Contains(got, []byte(`"content":null`)) {
		t.Errorf("null content was not preserved: %s", got)
	}
}

func TestOpenAI_MutateResponse_FallsBackToRequestContext(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewOpenAIHandler(reg.Redactor(), reg.Unredactor(), false, false)

	src := []byte(`{"messages":[{"role":"user","content":"Email a@b.com"}]}`)
	m, _ := mutateProviderRequest(t, h, src)

	resp := jsonResponse()
	resp.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	*resp.Request = *resp.Request.WithContext(contextWithMapping(resp.Request.Context(), m))

	got, err := h.MutateResponse(context.Background(), resp, []byte(`{"choices":[{"message":{"content":"<EMAIL_1>"}}]}`))
	if err != nil {
		t.Fatalf("MutateResponse: %v", err)
	}
	if want := `{"choices":[{"message":{"content":"a@b.com"}}]}`; string(got) != want {
		t.Fatalf("MutateResponse = %s, want %s", got, want)
	}
}

func TestOpenAI_MutateResponse_PassThrough(t *testing.T) {
	h := NewOpenAIHandler(nil, nil, false, false)

	t.Run("no mapping", func(t *testing.T) {
		src := []byte(`{"choices":[{"message":{"content":"<EMAIL_1>"}}]}`)
		got, err := h.MutateResponse(context.Background(), nil, src)
		if err != nil {
			t.Fatalf("MutateResponse: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("body changed without a mapping: %s", got)
		}
	})

	t.Run("nil unredactor", func(t *testing.T) {
		m := pii.GetMapping()
		defer pii.PutMapping(m)
		src := []byte(`{"choices":[{"message":{"content":"<EMAIL_1>"}}]}`)
		got, err := h.MutateResponse(contextWithMapping(context.Background(), m), nil, src)
		if err != nil {
			t.Fatalf("MutateResponse: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("nil unredactor changed body: %s", got)
		}
	})

	t.Run("non-JSON body", func(t *testing.T) {
		m := pii.GetMapping()
		defer pii.PutMapping(m)
		src := []byte("not json")
		got, err := h.MutateResponse(contextWithMapping(context.Background(), m), nil, src)
		if err != nil {
			t.Fatalf("MutateResponse: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("non-JSON body changed: %q", got)
		}
	})
}

func TestOpenAI_MutateStreamChunk(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewOpenAIHandler(reg.Redactor(), reg.Unredactor(), false, false)

	m, _ := mutateProviderRequest(t, h, fixture(t, "openai_request.json"))
	ctx := contextWithMapping(context.Background(), m)

	chunk := rawFixture(t, "openai_sse_chunk.txt")
	want := rawFixture(t, "openai_sse_chunk_unredacted.txt")

	got, err := h.MutateStreamChunk(ctx, chunk, "text/event-stream")
	if err != nil {
		t.Fatalf("MutateStreamChunk: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("MutateStreamChunk:\n got %q\nwant %q", got, want)
	}

	// Chunks with no delta.content are passed through byte-for-byte.
	for name, chunk := range map[string][]byte{
		"role announcement": []byte(`data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n"),
		"finish reason":     []byte(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"),
		"done sentinel":     []byte("data: [DONE]\n\n"),
		"keep-alive":        []byte(": keep-alive\n\n"),
	} {
		t.Run("unchanged/"+name, func(t *testing.T) {
			got, err := h.MutateStreamChunk(ctx, chunk, "text/event-stream")
			if err != nil {
				t.Fatalf("MutateStreamChunk: %v", err)
			}
			if !bytes.Equal(got, chunk) {
				t.Fatalf("MutateStreamChunk = %q, want unchanged %q", got, chunk)
			}
		})
	}

	t.Run("no mapping", func(t *testing.T) {
		got, err := h.MutateStreamChunk(context.Background(), chunk, "text/event-stream")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if !bytes.Equal(got, chunk) {
			t.Fatalf("MutateStreamChunk = %q, want unchanged %q", got, chunk)
		}
	})

	t.Run("nil handler", func(t *testing.T) {
		var nilHandler *OpenAIHandler
		got, err := nilHandler.MutateStreamChunk(ctx, chunk, "text/event-stream")
		if err != nil || !bytes.Equal(got, chunk) {
			t.Fatalf("nil handler changed chunk (err %v)", err)
		}
	})
}

func TestAnthropic_MutateRequest(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewAnthropicHandler(reg.Redactor(), reg.Unredactor(), false, false)

	src := fixture(t, "anthropic_request.json")
	want := fixture(t, "anthropic_request_redacted.json")

	m, got := mutateProviderRequest(t, h, src)
	if !bytes.Equal(got, want) {
		t.Fatalf("redacted body:\n got %s\nwant %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatalf("redacted body is not valid JSON: %s", got)
	}

	for ph, wantVal := range map[string]string{
		"<EMAIL_1>": "alice.smith@example.com",
		"<CC_1>":    "4111 1111 1111 1111",
		"<CC_2>":    "4111 1111 1111 1111",
	} {
		if gotVal, ok := m.Lookup(ph); !ok || gotVal != wantVal {
			t.Errorf("mapping[%s] = %q (present=%v), want %q", ph, gotVal, ok, wantVal)
		}
	}

	// metadata is not walked.
	if !bytes.Contains(got, []byte(`"user_id":"bob@example.org"`)) {
		t.Errorf("metadata field was rewritten: %s", got)
	}
}

func TestAnthropic_MutateRequest_SystemArray(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewAnthropicHandler(reg.Redactor(), reg.Unredactor(), false, false)

	src := []byte(`{"system":[{"type":"text","text":"Escalate to alice.smith@example.com."},{"type":"text","text":"Be brief."}],"messages":[{"role":"user","content":"hi"}]}`)
	want := `{"system":[{"type":"text","text":"Escalate to <EMAIL_1>."},{"type":"text","text":"Be brief."}],"messages":[{"role":"user","content":"hi"}]}`

	_, got := mutateProviderRequest(t, h, src)
	if string(got) != want {
		t.Fatalf("redacted body = %s, want %s", got, want)
	}
}

func TestAnthropic_MutateRequest_PassThrough(t *testing.T) {
	h := NewAnthropicHandler(nil, nil, false, false)

	src := []byte(`{"system":"x","messages":[{"content":"a@b.com"}]}`)
	got, err := h.MutateRequest(context.Background(), nil, src)
	if err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("nil redactor changed body: %s", got)
	}

	if got, err := h.MutateRequest(context.Background(), nil, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty body: got %q, err %v", got, err)
	}

	var nilHandler *AnthropicHandler
	if got, err := nilHandler.MutateRequest(context.Background(), nil, src); err != nil || !bytes.Equal(got, src) {
		t.Fatalf("nil handler mutated body (err %v)", err)
	}
}

func TestAnthropic_MutateResponse(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewAnthropicHandler(reg.Redactor(), reg.Unredactor(), false, false)

	m, _ := mutateProviderRequest(t, h, fixture(t, "anthropic_request.json"))

	body := fixture(t, "anthropic_response.json")
	want := fixture(t, "anthropic_response_unredacted.json")

	got, err := h.MutateResponse(contextWithMapping(context.Background(), m), jsonResponse(), body)
	if err != nil {
		t.Fatalf("MutateResponse: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("unredacted body:\n got %s\nwant %s", got, want)
	}
	for _, leaked := range []string{"<CC_1>", "<EMAIL_1>"} {
		if bytes.Contains(got, []byte(leaked)) {
			t.Errorf("unredacted body still contains %q", leaked)
		}
	}
	// tool_use blocks are not text leaves and stay intact.
	if !bytes.Contains(got, []byte(`"tool_use"`)) || !bytes.Contains(got, []byte(`"query":"card lookup"`)) {
		t.Errorf("tool_use block was not preserved: %s", got)
	}
}

func TestAnthropic_MutateResponse_PassThrough(t *testing.T) {
	h := NewAnthropicHandler(nil, nil, false, false)

	t.Run("no mapping", func(t *testing.T) {
		src := []byte(`{"content":[{"type":"text","text":"<EMAIL_1>"}]}`)
		if got, err := h.MutateResponse(context.Background(), nil, src); err != nil || !bytes.Equal(got, src) {
			t.Fatalf("body changed without a mapping (err %v): %s", err, got)
		}
	})

	t.Run("nil unredactor", func(t *testing.T) {
		m := pii.GetMapping()
		defer pii.PutMapping(m)
		src := []byte(`{"content":[{"type":"text","text":"<EMAIL_1>"}]}`)
		if got, err := h.MutateResponse(contextWithMapping(context.Background(), m), nil, src); err != nil || !bytes.Equal(got, src) {
			t.Fatalf("nil unredactor changed body (err %v): %s", err, got)
		}
	})
}

func TestAnthropic_MutateStreamChunk(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewAnthropicHandler(reg.Redactor(), reg.Unredactor(), false, false)

	m, _ := mutateProviderRequest(t, h, fixture(t, "anthropic_request.json"))
	ctx := contextWithMapping(context.Background(), m)

	t.Run("content_block_delta", func(t *testing.T) {
		chunk := rawFixture(t, "anthropic_sse_content_block_delta.txt")
		got, err := h.MutateStreamChunk(ctx, chunk, "text/event-stream")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		want := "event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Email alice.smith@example.com for the receipt."}}` + "\n\n"
		if string(got) != want {
			t.Fatalf("MutateStreamChunk = %q, want %q", got, want)
		}
	})

	// Events without text are relayed untouched, including the event: line.
	for _, name := range []string{
		"anthropic_sse_message_start.txt",
		"anthropic_sse_message_delta.txt",
	} {
		t.Run("unchanged/"+name, func(t *testing.T) {
			chunk := rawFixture(t, name)
			got, err := h.MutateStreamChunk(ctx, chunk, "text/event-stream")
			if err != nil {
				t.Fatalf("MutateStreamChunk: %v", err)
			}
			if !bytes.Equal(got, chunk) {
				t.Fatalf("MutateStreamChunk = %q, want unchanged %q", got, chunk)
			}
		})
	}

	t.Run("input_json_delta", func(t *testing.T) {
		chunk := []byte(`event: content_block_delta` + "\n" +
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"card\":\"4111"}}` + "\n\n")
		got, err := h.MutateStreamChunk(ctx, chunk, "text/event-stream")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if !bytes.Equal(got, chunk) {
			t.Fatalf("MutateStreamChunk = %q, want unchanged %q", got, chunk)
		}
	})

	t.Run("no mapping", func(t *testing.T) {
		chunk := rawFixture(t, "anthropic_sse_content_block_delta.txt")
		got, err := h.MutateStreamChunk(context.Background(), chunk, "text/event-stream")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if !bytes.Equal(got, chunk) {
			t.Fatalf("MutateStreamChunk = %q, want unchanged %q", got, chunk)
		}
	})

	t.Run("nil handler", func(t *testing.T) {
		var nilHandler *AnthropicHandler
		chunk := rawFixture(t, "anthropic_sse_content_block_delta.txt")
		if got, err := nilHandler.MutateStreamChunk(ctx, chunk, "text/event-stream"); err != nil || !bytes.Equal(got, chunk) {
			t.Fatalf("nil handler changed chunk (err %v)", err)
		}
	})
}

func TestGemini_MutateRequest(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewGeminiHandler(reg.Redactor(), reg.Unredactor(), false, false)

	src := fixture(t, "gemini_request.json")
	want := fixture(t, "gemini_request_redacted.json")

	m, got := mutateProviderRequest(t, h, src)
	if !bytes.Equal(got, want) {
		t.Fatalf("redacted body:\n got %s\nwant %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatalf("redacted body is not valid JSON: %s", got)
	}

	for ph, wantVal := range map[string]string{
		"<EMAIL_1>": "alice.smith@example.com",
		"<PHONE_1>": "+1 415-555-2671",
		"<SSN_1>":   "123-45-6789",
		"<EMAIL_2>": "alice.smith@example.com",
	} {
		if gotVal, ok := m.Lookup(ph); !ok || gotVal != wantVal {
			t.Errorf("mapping[%s] = %q (present=%v), want %q", ph, gotVal, ok, wantVal)
		}
	}

	// inlineData is not a text leaf; generationConfig and the base64 payload
	// survive untouched.
	for _, keep := range []string{
		`"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgo="}`,
		`"generationConfig":{"temperature":0.4,"maxOutputTokens":512}`,
	} {
		if !bytes.Contains(got, []byte(keep)) {
			t.Errorf("redacted body lost %s: %s", keep, got)
		}
	}
}

func TestGemini_MutateRequest_PassThrough(t *testing.T) {
	h := NewGeminiHandler(nil, nil, false, false)

	src := []byte(`{"contents":[{"parts":[{"text":"a@b.com"}]}]}`)
	got, err := h.MutateRequest(context.Background(), nil, src)
	if err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("nil redactor changed body: %s", got)
	}

	if got, err := h.MutateRequest(context.Background(), nil, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty body: got %q, err %v", got, err)
	}

	var nilHandler *GeminiHandler
	if got, err := nilHandler.MutateRequest(context.Background(), nil, src); err != nil || !bytes.Equal(got, src) {
		t.Fatalf("nil handler mutated body (err %v)", err)
	}
}

func TestGemini_MutateResponse(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewGeminiHandler(reg.Redactor(), reg.Unredactor(), false, false)

	m, _ := mutateProviderRequest(t, h, fixture(t, "gemini_request.json"))

	body := fixture(t, "gemini_response.json")
	want := fixture(t, "gemini_response_unredacted.json")

	got, err := h.MutateResponse(contextWithMapping(context.Background(), m), jsonResponse(), body)
	if err != nil {
		t.Fatalf("MutateResponse: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("unredacted body:\n got %s\nwant %s", got, want)
	}
	if bytes.Contains(got, []byte("<EMAIL_1>")) || bytes.Contains(got, []byte("<PHONE_1>")) {
		t.Errorf("unredacted body still contains a placeholder: %s", got)
	}
}

func TestGemini_MutateResponse_PassThrough(t *testing.T) {
	h := NewGeminiHandler(nil, nil, false, false)

	src := []byte(`{"candidates":[{"content":{"parts":[{"text":"<EMAIL_1>"}]}}]}`)
	if got, err := h.MutateResponse(context.Background(), nil, src); err != nil || !bytes.Equal(got, src) {
		t.Fatalf("body changed without a mapping (err %v): %s", err, got)
	}
}

func TestGemini_MutateStreamChunk(t *testing.T) {
	reg := newProviderRegistry(t)
	h := NewGeminiHandler(reg.Redactor(), reg.Unredactor(), false, false)

	m, _ := mutateProviderRequest(t, h, fixture(t, "gemini_request.json"))
	ctx := contextWithMapping(context.Background(), m)

	t.Run("newline delimited JSON", func(t *testing.T) {
		chunk := []byte(`{"candidates":[{"content":{"parts":[{"text":"Email <EMAIL_1>."}]}}]}` + "\n" +
			`{"candidates":[{"content":{"parts":[{"text":"Done."}]}}]}` + "\n")
		want := []byte(`{"candidates":[{"content":{"parts":[{"text":"Email alice.smith@example.com."}]}}]}` + "\n" +
			`{"candidates":[{"content":{"parts":[{"text":"Done."}]}}]}` + "\n")

		got, err := h.MutateStreamChunk(ctx, chunk, "application/json")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("MutateStreamChunk:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("single JSON document", func(t *testing.T) {
		chunk := []byte(`{"candidates":[{"content":{"parts":[{"text":"Call <PHONE_1>."}]}}]}`)
		want := `{"candidates":[{"content":{"parts":[{"text":"Call +1 415-555-2671."}]}}]}`
		got, err := h.MutateStreamChunk(ctx, chunk, "application/json")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if string(got) != want {
			t.Fatalf("MutateStreamChunk = %s, want %s", got, want)
		}
	})

	t.Run("CRLF framing and non-JSON lines", func(t *testing.T) {
		chunk := []byte(": keep-alive\r\n" +
			`{"candidates":[{"content":{"parts":[{"text":"Email <EMAIL_1>."}]}}]}` + "\r\n" +
			"\r\n")
		want := ": keep-alive\r\n" +
			`{"candidates":[{"content":{"parts":[{"text":"Email alice.smith@example.com."}]}}]}` + "\r\n" +
			"\r\n"

		got, err := h.MutateStreamChunk(ctx, chunk, "application/json")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if string(got) != want {
			t.Fatalf("MutateStreamChunk = %q, want %q", got, want)
		}
	})

	t.Run("no placeholders", func(t *testing.T) {
		chunk := []byte(`{"candidates":[{"content":{"parts":[{"text":"Done."}]}}]}` + "\n")
		got, err := h.MutateStreamChunk(ctx, chunk, "application/json")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if !bytes.Equal(got, chunk) {
			t.Fatalf("MutateStreamChunk = %q, want unchanged %q", got, chunk)
		}
	})

	t.Run("no mapping", func(t *testing.T) {
		chunk := []byte(`{"candidates":[{"content":{"parts":[{"text":"<EMAIL_1>"}]}}]}`)
		got, err := h.MutateStreamChunk(context.Background(), chunk, "application/json")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if !bytes.Equal(got, chunk) {
			t.Fatalf("MutateStreamChunk = %q, want unchanged %q", got, chunk)
		}
	})

	t.Run("nil handler", func(t *testing.T) {
		var nilHandler *GeminiHandler
		chunk := []byte(`{"candidates":[]}`)
		if got, err := nilHandler.MutateStreamChunk(ctx, chunk, "application/json"); err != nil || !bytes.Equal(got, chunk) {
			t.Fatalf("nil handler changed chunk (err %v)", err)
		}
	})
}

func TestRewriteSSEData(t *testing.T) {
	t.Run("preserves event and id, collapses multi-line data", func(t *testing.T) {
		chunk := []byte("event: content_block_delta\nid: 7\ndata: {\"a\":\"one\"\ndata: }")
		got, err := rewriteSSEData(chunk, func(data string) (string, error) {
			if want := "{\"a\":\"one\"\n}"; data != want {
				t.Fatalf("data = %q, want %q", data, want)
			}
			return `{"a":"two"}`, nil
		})
		if err != nil {
			t.Fatalf("rewriteSSEData: %v", err)
		}
		want := "event: content_block_delta\nid: 7\ndata: {\"a\":\"two\"}\n\n"
		if string(got) != want {
			t.Fatalf("rewriteSSEData = %q, want %q", got, want)
		}
	})

	t.Run("unchanged data returns the original bytes", func(t *testing.T) {
		chunk := []byte("event: ping\r\ndata: {}\r\n\r\n")
		got, err := rewriteSSEData(chunk, func(data string) (string, error) { return data, nil })
		if err != nil {
			t.Fatalf("rewriteSSEData: %v", err)
		}
		if !bytes.Equal(got, chunk) {
			t.Fatalf("rewriteSSEData = %q, want unchanged %q", got, chunk)
		}
	})

	t.Run("no data lines", func(t *testing.T) {
		chunk := []byte(": keep-alive\n\n")
		got, err := rewriteSSEData(chunk, func(string) (string, error) {
			t.Fatal("callback ran for an event without data")
			return "", nil
		})
		if err != nil {
			t.Fatalf("rewriteSSEData: %v", err)
		}
		if !bytes.Equal(got, chunk) {
			t.Fatalf("rewriteSSEData = %q, want unchanged %q", got, chunk)
		}
	})

	t.Run("callback error is propagated", func(t *testing.T) {
		boom := errors.New("boom")
		_, err := rewriteSSEData([]byte("data: {}\n\n"), func(string) (string, error) { return "", boom })
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want %v", err, boom)
		}
	})
}

func TestWalkFields(t *testing.T) {
	fields := newFieldSet([]string{"a"}, []string{"b", "c"}, []string{"html"})
	src := []byte(`{"a":"x","not":"y","b":[{"c":"z"},{"c":"w"}],"n":9007199254740993,"html":"<EMAIL_1> & more"}`)
	want := `{"a":"X","not":"y","b":[{"c":"Z"},{"c":"W"}],"n":9007199254740993,"html":"<EMAIL_1> & MORE"}`

	got, err := walkFields(src, fields, func(s string) (string, error) {
		return strings.ToUpper(s), nil
	})
	if err != nil {
		t.Fatalf("walkFields: %v", err)
	}
	if string(got) != want {
		t.Fatalf("walkFields = %s, want %s", got, want)
	}
}

func TestWalkFields_NilCallback(t *testing.T) {
	fields := newFieldSet([]string{"a"})
	src := []byte(`{"a":"<EMAIL_1>","b":"<b>"}`)
	got, err := walkFields(src, fields, nil)
	if err != nil {
		t.Fatalf("walkFields: %v", err)
	}
	if string(got) != `{"a":"<EMAIL_1>","b":"<b>"}` {
		t.Fatalf("walkFields = %s", got)
	}
}

func TestWalkFields_Errors(t *testing.T) {
	fields := newFieldSet([]string{"a"})

	if _, err := walkFields(nil, fields, nil); err == nil {
		t.Fatal("walkFields accepted empty input")
	}
	if _, err := walkFields([]byte(`{"a":`), fields, nil); err == nil {
		t.Fatal("walkFields accepted truncated JSON")
	}
	if _, err := walkFields([]byte(`{"a":"x"} {"a":"y"}`), fields, nil); err == nil {
		t.Fatal("walkFields accepted trailing JSON")
	}

	boom := errors.New("boom")
	_, err := walkFields([]byte(`{"a":"x"}`), fields, func(string) (string, error) { return "", boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

// TestProviderHandlers_CompileTimeContracts documents that each provider
// handler satisfies both proxy mutator interfaces.
func TestProviderHandlers_CompileTimeContracts(t *testing.T) {
	reg := newProviderRegistry(t)
	var mutators []proxy.ResponseMutator = []proxy.ResponseMutator{
		NewOpenAIHandler(reg.Redactor(), reg.Unredactor(), false, false),
		NewAnthropicHandler(reg.Redactor(), reg.Unredactor(), false, false),
		NewGeminiHandler(reg.Redactor(), reg.Unredactor(), false, false),
	}
	for _, m := range mutators {
		// A body with no placeholders must be returned unchanged.
		body := []byte(`{"unrelated":"value"}`)
		got, err := m.MutateResponse(contextWithMapping(context.Background(), pii.NewMapping()), nil, body)
		if err != nil {
			t.Fatalf("MutateResponse: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("MutateResponse = %s, want %s", got, body)
		}
	}
}
