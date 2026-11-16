package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/pii"
)

// fixture reads a testdata file and trims the trailing newline editors add.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return bytes.TrimSpace(b)
}

// newGenericHandler builds a handler backed by the default detector set, so the
// fixtures exercise the real email/phone/card/SSN detectors.
func newGenericHandler(t *testing.T) *GenericHandler {
	t.Helper()
	reg, err := pii.NewRegistry(config.DefaultConfig())
	if err != nil {
		t.Fatalf("pii.NewRegistry: %v", err)
	}
	return NewGenericHandler(reg.Redactor(), reg.Unredactor())
}

// mappingFor produces a mapping by redacting src, the same way the proxy does
// before it forwards a request.
func mappingFor(t *testing.T, h *GenericHandler, src []byte) (*pii.Mapping, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(src))
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

func TestGeneric_MutateRequest(t *testing.T) {
	h := newGenericHandler(t)
	src := fixture(t, "generic_request.json")
	want := fixture(t, "generic_request_redacted.json")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(src))
	got, err := h.MutateRequest(req.Context(), req, src)
	if err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("redacted body:\n got %s\nwant %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatalf("redacted body is not valid JSON: %s", got)
	}

	for _, leak := range []string{
		"alice.smith@example.com",
		"415-555-2671",
		"4111 1111 1111 1111",
		"123-45-6789",
	} {
		if bytes.Contains(got, []byte(leak)) {
			t.Errorf("redacted body still contains %q", leak)
		}
	}
	// Field names and non-PII values survive untouched.
	for _, keep := range []string{`"model":"gpt-4o"`, `"seed":9007199254740993`, `"role":"user"`} {
		if !bytes.Contains(got, []byte(keep)) {
			t.Errorf("redacted body lost %s", keep)
		}
	}

	m := mappingFromContext(req.Context())
	if m == nil {
		t.Fatal("mapping missing from request context")
	}
	for ph, want := range map[string]string{
		"<EMAIL_1>": "alice.smith@example.com",
		"<PHONE_1>": "+1 415-555-2671",
		"<CC_1>":    "4111 1111 1111 1111",
		"<SSN_1>":   "123-45-6789",
	} {
		if got, ok := m.Lookup(ph); !ok || got != want {
			t.Errorf("mapping[%s] = %q (present=%v), want %q", ph, got, ok, want)
		}
	}
}

func TestGeneric_MutateRequest_PlaceholdersAreUniqueAcrossLeaves(t *testing.T) {
	h := newGenericHandler(t)
	src := []byte(`{"a":"first@example.com","b":["second@example.com","third@example.com"]}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(src))
	got, err := h.MutateRequest(req.Context(), req, src)
	if err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}

	want := `{"a":"<EMAIL_1>","b":["<EMAIL_2>","<EMAIL_3>"]}`
	if string(got) != want {
		t.Fatalf("redacted body = %s, want %s", got, want)
	}

	m := mappingFromContext(req.Context())
	if m == nil {
		t.Fatal("mapping missing from request context")
	}
	// Every placeholder must map back to its own address, not the first one.
	restored, err := h.MutateResponse(contextWithMapping(context.Background(), m), nil, got)
	if err != nil {
		t.Fatalf("MutateResponse: %v", err)
	}
	if !bytes.Equal(restored, src) {
		t.Fatalf("round trip = %s, want %s", restored, src)
	}
}

func TestGeneric_MutateRequest_PassThrough(t *testing.T) {
	h := newGenericHandler(t)

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

	t.Run("nil redactor", func(t *testing.T) {
		g := NewGenericHandler(nil, nil)
		src := []byte(`{"email":"a@b.com"}`)
		got, err := g.MutateRequest(context.Background(), nil, src)
		if err != nil {
			t.Fatalf("MutateRequest: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("nil redactor changed body: %s", got)
		}
	})
}

func TestGeneric_MutateResponse(t *testing.T) {
	h := newGenericHandler(t)
	request := fixture(t, "generic_request.json")
	response := fixture(t, "generic_response.json")
	want := fixture(t, "generic_response_unredacted.json")

	m, _ := mappingFor(t, h, request)
	ctx := contextWithMapping(context.Background(), m)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil),
	}
	got, err := h.MutateResponse(ctx, resp, response)
	if err != nil {
		t.Fatalf("MutateResponse: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("unredacted body:\n got %s\nwant %s", got, want)
	}
	for _, leakedPlaceholder := range []string{"<EMAIL_1>", "<PHONE_1>", "<CC_1>", "<SSN_1>"} {
		if bytes.Contains(got, []byte(leakedPlaceholder)) {
			t.Errorf("unredacted body still contains %q", leakedPlaceholder)
		}
	}
}

func TestGeneric_MutateResponse_FallsBackToRequestContext(t *testing.T) {
	h := newGenericHandler(t)
	src := []byte(`{"email":"a@b.com"}`)

	// Prove the mapping can be recovered from the request the proxy attaches to
	// the upstream response instead of the ctx argument.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(src))
	if _, err := h.MutateRequest(req.Context(), req, src); err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}
	resp := &http.Response{StatusCode: http.StatusOK, Request: req}

	got, err := h.MutateResponse(context.Background(), resp, []byte(`{"email":"<EMAIL_1>"}`))
	if err != nil {
		t.Fatalf("MutateResponse: %v", err)
	}
	if want := `{"email":"a@b.com"}`; string(got) != want {
		t.Fatalf("MutateResponse = %s, want %s", got, want)
	}
}

func TestGeneric_MutateResponse_PassThrough(t *testing.T) {
	h := newGenericHandler(t)

	t.Run("no mapping", func(t *testing.T) {
		src := []byte(`{"email":"<EMAIL_1>"}`)
		got, err := h.MutateResponse(context.Background(), nil, src)
		if err != nil {
			t.Fatalf("MutateResponse: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("body changed without a mapping: %s", got)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		m := pii.GetMapping()
		defer pii.PutMapping(m)
		got, err := h.MutateResponse(contextWithMapping(context.Background(), m), nil, nil)
		if err != nil {
			t.Fatalf("MutateResponse: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %q, want empty body", got)
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

	t.Run("nil unredactor", func(t *testing.T) {
		g := NewGenericHandler(nil, nil)
		src := []byte(`{"email":"<EMAIL_1>"}`)
		m := pii.GetMapping()
		defer pii.PutMapping(m)
		got, err := g.MutateResponse(contextWithMapping(context.Background(), m), nil, src)
		if err != nil {
			t.Fatalf("MutateResponse: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("nil unredactor changed body: %s", got)
		}
	})
}

func TestGeneric_MutateStreamChunk(t *testing.T) {
	h := newGenericHandler(t)
	m, _ := mappingFor(t, h, fixture(t, "generic_request.json"))
	ctx := contextWithMapping(context.Background(), m)

	chunk := []byte(`data: {"choices":[{"delta":{"content":"Email <EMAIL_1> and card <CC_1>"}}]}` + "\n\n")

	got, err := h.MutateStreamChunk(ctx, chunk, "text/event-stream")
	if err != nil {
		t.Fatalf("MutateStreamChunk: %v", err)
	}
	want := `data: {"choices":[{"delta":{"content":"Email alice.smith@example.com and card 4111 1111 1111 1111"}}]}` + "\n\n"
	if string(got) != want {
		t.Fatalf("MutateStreamChunk = %q, want %q", got, want)
	}
}

func TestGeneric_MutateStreamChunk_PassThrough(t *testing.T) {
	h := newGenericHandler(t)
	m, _ := mappingFor(t, h, fixture(t, "generic_request.json"))
	ctx := contextWithMapping(context.Background(), m)

	cases := map[string][]byte{
		"empty":         nil,
		"comment only":  []byte(": keep-alive\n\n"),
		"no data":       []byte("event: ping\n\n"),
		"done sentinel": []byte("data: [DONE]\n\n"),
		"non-JSON data": []byte("data: not json\n\n"),
		"empty data":    []byte("data: \n\n"),
	}
	for name, chunk := range cases {
		t.Run(name, func(t *testing.T) {
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
		chunk := []byte(`data: {"content":"<EMAIL_1>"}` + "\n\n")
		got, err := h.MutateStreamChunk(context.Background(), chunk, "text/event-stream")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if !bytes.Equal(got, chunk) {
			t.Fatalf("MutateStreamChunk = %q, want unchanged %q", got, chunk)
		}
	})

	t.Run("nil unredactor", func(t *testing.T) {
		g := NewGenericHandler(nil, nil)
		chunk := []byte("data: {}\n\n")
		got, err := g.MutateStreamChunk(ctx, chunk, "text/event-stream")
		if err != nil {
			t.Fatalf("MutateStreamChunk: %v", err)
		}
		if !bytes.Equal(got, chunk) {
			t.Fatalf("MutateStreamChunk = %q, want unchanged %q", got, chunk)
		}
	})
}

func TestGeneric_MutateStreamChunk_MultilineData(t *testing.T) {
	h := newGenericHandler(t)
	m := pii.GetMapping()
	defer pii.PutMapping(m)
	m.Add(pii.CategoryEmail, "alice.smith@example.com")
	ctx := contextWithMapping(context.Background(), m)

	// The SSE parser joins multi-line data with "\n"; JSON tolerates the
	// resulting whitespace between tokens.
	chunk := []byte("data: {\"content\":\"<EMAIL_1>\"\n\ndata: }\n\n")
	got, err := h.MutateStreamChunk(ctx, chunk, "text/event-stream")
	if err != nil {
		t.Fatalf("MutateStreamChunk: %v", err)
	}
	want := `data: {"content":"alice.smith@example.com"}` + "\n\n"
	if string(got) != want {
		t.Fatalf("MutateStreamChunk = %q, want %q", got, want)
	}
}

func TestGeneric_RoundTrip(t *testing.T) {
	h := newGenericHandler(t)
	original := fixture(t, "generic_request.json")

	m, redacted := mappingFor(t, h, original)
	if bytes.Equal(redacted, original) {
		t.Fatal("request was not redacted")
	}

	restored, err := h.MutateResponse(contextWithMapping(context.Background(), m), nil, redacted)
	if err != nil {
		t.Fatalf("MutateResponse: %v", err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("round trip:\n got %s\nwant %s", restored, original)
	}
}

func TestParseSSEData(t *testing.T) {
	tests := []struct {
		name      string
		chunk     string
		wantData  string
		wantFound bool
	}{
		{"empty", "", "", false},
		{"no data", "event: ping\nid: 7\n\n", "", false},
		{"comment only", ": keep-alive\n\n", "", false},
		{"single line", "data: hello\n\n", "hello", true},
		{"no space after colon", "data:hello\n\n", "hello", true},
		{"crlf", "data: hello\r\n\r\n", "hello", true},
		{"multi line", "data: one\ndata: two\n\n", "one\ntwo", true},
		{"ignores event", "event: message\ndata: {}\n\n", "{}", true},
		{"no trailing newline", "data: tail", "tail", true},
		{"empty data", "data: \n\n", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, found := ParseSSEData([]byte(tt.chunk))
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if data != tt.wantData {
				t.Fatalf("data = %q, want %q", data, tt.wantData)
			}
		})
	}
}

func TestFormatSSEData(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"single line", "hello", "data: hello\n\n"},
		{"empty", "", "data: \n\n"},
		{"multi line", "one\ntwo", "data: one\ndata: two\n\n"},
		{"trailing newline", "one\n", "data: one\ndata: \n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(FormatSSEData(tt.data)); got != tt.want {
				t.Fatalf("FormatSSEData(%q) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestSSEDataRoundTrip(t *testing.T) {
	for _, data := range []string{"{}", `{"a":"b"}`, "line1\nline2", ""} {
		chunk := FormatSSEData(data)
		got, ok := ParseSSEData(chunk)
		if !ok {
			t.Fatalf("ParseSSEData(FormatSSEData(%q)) reported no data", data)
		}
		if got != data {
			t.Fatalf("round trip = %q, want %q", got, data)
		}
	}
}

func TestMappingFromContext(t *testing.T) {
	if m := mappingFromContext(nil); m != nil {
		t.Fatalf("mappingFromContext(nil) = %v, want nil", m)
	}
	if m := mappingFromContext(context.Background()); m != nil {
		t.Fatalf("mappingFromContext(background) = %v, want nil", m)
	}

	m := pii.GetMapping()
	defer pii.PutMapping(m)
	ctx := contextWithMapping(context.Background(), m)
	if got := mappingFromContext(ctx); got != m {
		t.Fatalf("mappingFromContext round trip = %v, want %v", got, m)
	}
	if got := contextWithMapping(nil, m); mappingFromContext(got) != m {
		t.Fatal("contextWithMapping(nil, m) lost the mapping")
	}
}

func TestMergeMapping_NoEntries(t *testing.T) {
	local := pii.GetMapping()
	defer pii.PutMapping(local)
	dst := pii.GetMapping()
	defer pii.PutMapping(dst)
	if got := mergeMapping("<EMAIL_1>", local, dst); got != "<EMAIL_1>" {
		t.Fatalf("mergeMapping with empty local = %q", got)
	}
}

func TestCategoryFromPlaceholder(t *testing.T) {
	cases := map[string]pii.Category{
		"<EMAIL_1>": pii.CategoryEmail,
		"<CC_12>":   pii.CategoryCreditCard,
		"<SSN_3>":   pii.CategorySSN,
		"<EMAIL>":   pii.CategoryEmail,
	}
	for ph, want := range cases {
		if got := categoryFromPlaceholder(ph); got != want {
			t.Errorf("categoryFromPlaceholder(%q) = %q, want %q", ph, got, want)
		}
	}
}

func TestPlaceholderIndex(t *testing.T) {
	cases := map[string]int{
		"<EMAIL_1>":   1,
		"<EMAIL_10>":  10,
		"<EMAIL>":     0,
		"<NOT_A_NUM>": 0,
	}
	for ph, want := range cases {
		if got := placeholderIndex(ph); got != want {
			t.Errorf("placeholderIndex(%q) = %d, want %d", ph, got, want)
		}
	}
}

func TestSortPlaceholders(t *testing.T) {
	in := []string{"<EMAIL_10>", "<EMAIL_2>", "<EMAIL_1>"}
	sortPlaceholders(in)
	want := []string{"<EMAIL_1>", "<EMAIL_2>", "<EMAIL_10>"}
	if strings.Join(in, ",") != strings.Join(want, ",") {
		t.Fatalf("sortPlaceholders = %v, want %v", in, want)
	}
}
