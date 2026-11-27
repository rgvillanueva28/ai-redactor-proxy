package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/handler"
	"github.com/local/redactor/internal/pii"
)

// testEnv wires a Handler the way the binary does, backed by a config file in a
// temporary directory.
type testEnv struct {
	handler *Handler
	cfg     *config.Config
	path    string
	reg     *pii.Registry
	adapter *handler.Adapter
}

// newTestEnv builds a Handler around cfg (defaults when nil) and writes the
// starting config to a temp file so PUT and reload have a real target.
func newTestEnv(t *testing.T, cfg *config.Config) *testEnv {
	t.Helper()

	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	reg, err := pii.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("pii.NewRegistry: %v", err)
	}
	adapter := handler.NewAdapter(reg.Redactor(), reg.Unredactor(), cfg.PII.LogRedacted, cfg.PII.LogBodies)

	return &testEnv{
		handler: NewHandler(cfg, path, reg, adapter),
		cfg:     cfg,
		path:    path,
		reg:     reg,
		adapter: adapter,
	}
}

// serve dispatches to the method that owns target, mirroring the mux wiring.
func (e *testEnv) serve(method, target string, body []byte) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()

	switch {
	case strings.HasPrefix(target, "/api/config/reload"):
		e.handler.Reload(rec, req)
	case strings.HasPrefix(target, "/api/config/preview"):
		e.handler.Preview(rec, req)
	default:
		e.handler.Config(rec, req)
	}
	return rec
}

// decodeJSONBody decodes rec's body into a fresh T.
func decodeJSONBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
	}
	return v
}

// mustJSON marshals v or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}

// redactText runs text through reg's live redactor and returns the original
// placeholders mapping cleaned up.
func redactText(t *testing.T, reg *pii.Registry, text string) string {
	t.Helper()
	redacted, mapping := reg.Redactor().Redact(text)
	pii.PutMapping(mapping)
	return redacted
}

// assertSecurityHeaders fails unless the API's three security headers are set.
func assertSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	h := rec.Header()
	if got := h.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
	if got := h.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestGetConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Listen.Address = "127.0.0.1:12345"
	env := newTestEnv(t, cfg)

	rec := env.serve(http.MethodGet, "/api/config", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)

	got := decodeJSONBody[config.Config](t, rec)
	if got.Listen.Address != cfg.Listen.Address {
		t.Errorf("listen.address = %q, want %q", got.Listen.Address, cfg.Listen.Address)
	}
	if !got.PII.EnableEmail {
		t.Error("enable_email = false, want true")
	}
	if len(got.Upstream) != len(cfg.Upstream) {
		t.Errorf("upstream count = %d, want %d", len(got.Upstream), len(cfg.Upstream))
	}
}

func TestPutConfigPersistsAndApplies(t *testing.T) {
	env := newTestEnv(t, nil)

	if got := redactText(t, env.reg, "alice@example.com"); !strings.Contains(got, "<EMAIL_1>") {
		t.Fatalf("precondition: email not redacted: %q", got)
	}

	updated := config.DefaultConfig()
	updated.Listen.Address = "127.0.0.1:9999"
	updated.PII.EnableEmail = false
	updated.PII.EnableDomain = false
	updated.PII.LogRedacted = true

	rec := env.serve(http.MethodPut, "/api/config", mustJSON(t, updated))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)

	got := decodeJSONBody[config.Config](t, rec)
	if got.Listen.Address != "127.0.0.1:9999" {
		t.Errorf("response listen.address = %q", got.Listen.Address)
	}
	if got.PII.EnableEmail {
		t.Error("response enable_email = true, want false")
	}

	onDisk, err := config.Load(env.path)
	if err != nil {
		t.Fatalf("reload written config: %v", err)
	}
	if onDisk.Listen.Address != "127.0.0.1:9999" {
		t.Errorf("on-disk listen.address = %q", onDisk.Listen.Address)
	}
	if onDisk.PII.EnableEmail {
		t.Error("on-disk enable_email = true, want false")
	}
	if !onDisk.PII.LogRedacted {
		t.Error("on-disk log_redacted = false, want true")
	}

	// The running registry must observe the change immediately.
	if got := redactText(t, env.reg, "alice@example.com"); got != "alice@example.com" {
		t.Errorf("live redaction after apply = %q, want the original text", got)
	}

	// GET must report the applied config, not the one from startup.
	getRec := env.serve(http.MethodGet, "/api/config", nil)
	current := decodeJSONBody[config.Config](t, getRec)
	if current.Listen.Address != "127.0.0.1:9999" {
		t.Errorf("GET listen.address = %q, want 127.0.0.1:9999", current.Listen.Address)
	}
}

func TestPutConfigRejectsInvalidWithoutSideEffects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.Config)
		field  string
	}{
		{
			name:   "empty listen address",
			mutate: func(c *config.Config) { c.Listen.Address = "" },
			field:  "listen.address",
		},
		{
			name:   "invalid category pattern",
			mutate: func(c *config.Config) { c.PII.Email.Patterns = []string{"("} },
			field:  "pii.email.patterns[0]",
		},
		{
			name: "empty upstream host match",
			mutate: func(c *config.Config) {
				c.Upstream["openai"] = config.UpstreamConfig{HostMatch: "", BaseURL: "https://api.openai.com"}
			},
			field: "upstream.openai.host_match",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, nil)
			before, err := os.ReadFile(env.path)
			if err != nil {
				t.Fatalf("read initial config: %v", err)
			}

			invalid := config.DefaultConfig()
			tc.mutate(invalid)

			rec := env.serve(http.MethodPut, "/api/config", mustJSON(t, invalid))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			assertSecurityHeaders(t, rec)
			body := decodeJSONBody[errorResponse](t, rec)
			if body.Error == "" {
				t.Error("error message is empty")
			}
			if body.Field != tc.field {
				t.Errorf("field = %q, want %q", body.Field, tc.field)
			}

			after, err := os.ReadFile(env.path)
			if err != nil {
				t.Fatalf("read config after rejected PUT: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Error("rejected PUT modified the config file")
			}
			if !env.cfg.PII.EnableEmail {
				t.Error("rejected PUT changed the live config")
			}
			if got := redactText(t, env.reg, "alice@example.com"); !strings.Contains(got, "<EMAIL_1>") {
				t.Errorf("rejected PUT changed live redaction: %q", got)
			}
		})
	}
}

func TestPutConfigRejectsMalformedJSON(t *testing.T) {
	env := newTestEnv(t, nil)

	for _, body := range []string{"", "{", `{} {}`, `{"listen": "not an object"}`} {
		rec := env.serve(http.MethodPut, "/api/config", []byte(body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %q status = %d, want 400: %s", body, rec.Code, rec.Body.String())
			continue
		}
		assertSecurityHeaders(t, rec)
		resp := decodeJSONBody[errorResponse](t, rec)
		if resp.Field != "body" {
			t.Errorf("PUT %q field = %q, want %q", body, resp.Field, "body")
		}
	}
}

func TestPutConfigWithoutPathFails(t *testing.T) {
	cfg := config.DefaultConfig()
	reg, err := pii.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("pii.NewRegistry: %v", err)
	}
	h := NewHandler(cfg, "", reg, handler.NewAdapter(reg.Redactor(), reg.Unredactor(), false, false))

	rec := httptest.NewRecorder()
	h.Config(rec, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(mustJSON(t, cfg))))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)
	resp := decodeJSONBody[errorResponse](t, rec)
	if !strings.Contains(resp.Error, "path") {
		t.Errorf("error = %q, want it to mention the missing path", resp.Error)
	}
}

func TestPutConfigSaveError(t *testing.T) {
	cfg := config.DefaultConfig()
	reg, err := pii.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("pii.NewRegistry: %v", err)
	}
	path := filepath.Join(t.TempDir(), "missing", "config.toml")
	h := NewHandler(cfg, path, reg, handler.NewAdapter(reg.Redactor(), reg.Unredactor(), false, false))

	rec := httptest.NewRecorder()
	h.Config(rec, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(mustJSON(t, cfg))))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)
	if res := decodeJSONBody[errorResponse](t, rec); !strings.Contains(res.Error, "save") {
		t.Errorf("error = %q, want it to mention the failed save", res.Error)
	}
	// The live registry must still redact with the unchanged config.
	if got := redactText(t, reg, "alice@example.com"); !strings.Contains(got, "<EMAIL_1>") {
		t.Errorf("live redaction changed after failed save: %q", got)
	}
}

func TestReloadReadsDiskAndApplies(t *testing.T) {
	env := newTestEnv(t, nil)

	updated := config.DefaultConfig()
	updated.Listen.Address = "127.0.0.1:9500"
	updated.PII.EnableEmail = false
	updated.PII.EnableDomain = false
	if err := updated.Save(env.path); err != nil {
		t.Fatalf("write updated config: %v", err)
	}

	rec := env.serve(http.MethodPost, "/api/config/reload", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)

	got := decodeJSONBody[config.Config](t, rec)
	if got.Listen.Address != "127.0.0.1:9500" {
		t.Errorf("response listen.address = %q", got.Listen.Address)
	}
	if got.PII.EnableEmail {
		t.Error("response enable_email = true, want false")
	}
	if got := redactText(t, env.reg, "alice@example.com"); got != "alice@example.com" {
		t.Errorf("live redaction after reload = %q, want the original text", got)
	}

	// GET must report the reloaded document.
	current := decodeJSONBody[config.Config](t, env.serve(http.MethodGet, "/api/config", nil))
	if current.Listen.Address != "127.0.0.1:9500" {
		t.Errorf("GET listen.address = %q, want 127.0.0.1:9500", current.Listen.Address)
	}
}

func TestReloadMissingFileFails(t *testing.T) {
	cfg := config.DefaultConfig()
	reg, err := pii.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("pii.NewRegistry: %v", err)
	}
	h := NewHandler(cfg, filepath.Join(t.TempDir(), "absent.toml"), reg, handler.NewAdapter(reg.Redactor(), reg.Unredactor(), false, false))

	rec := httptest.NewRecorder()
	h.Reload(rec, httptest.NewRequest(http.MethodPost, "/api/config/reload", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)
	if res := decodeJSONBody[errorResponse](t, rec); res.Error == "" {
		t.Error("error message is empty")
	}
}

func TestReloadInvalidFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		status  int
		field   string
	}{
		{
			name:    "malformed TOML",
			content: "[pii\n",
			status:  http.StatusInternalServerError,
		},
		{
			name:    "invalid value",
			content: "[listen]\naddress = \"\"\n",
			status:  http.StatusBadRequest,
			field:   "listen.address",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, nil)
			if err := os.WriteFile(env.path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			rec := env.serve(http.MethodPost, "/api/config/reload", nil)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			assertSecurityHeaders(t, rec)
			resp := decodeJSONBody[errorResponse](t, rec)
			if resp.Error == "" {
				t.Error("error message is empty")
			}
			if resp.Field != tc.field {
				t.Errorf("field = %q, want %q", resp.Field, tc.field)
			}
			// A rejected reload must leave the live config alone.
			if got := redactText(t, env.reg, "alice@example.com"); !strings.Contains(got, "<EMAIL_1>") {
				t.Errorf("live redaction changed after rejected reload: %q", got)
			}
		})
	}
}

func TestPreviewGet(t *testing.T) {
	env := newTestEnv(t, nil)
	text := "My email is alice@example.com"

	rec := env.serve(http.MethodGet, "/api/config/preview?text="+url.QueryEscape(text), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)

	resp := decodeJSONBody[previewResponse](t, rec)
	if resp.Redacted != "My email is <EMAIL_1>" {
		t.Errorf("redacted = %q, want %q", resp.Redacted, "My email is <EMAIL_1>")
	}
	if len(resp.Matches) != 1 {
		t.Fatalf("matches = %d, want 1: %+v", len(resp.Matches), resp.Matches)
	}
	m := resp.Matches[0]
	if m.Category != "EMAIL" {
		t.Errorf("category = %q, want EMAIL", m.Category)
	}
	if text[m.Start:m.End] != "alice@example.com" {
		t.Errorf("span = %q, want alice@example.com", text[m.Start:m.End])
	}
	if m.Replacement != "<EMAIL_1>" {
		t.Errorf("replacement = %q, want <EMAIL_1>", m.Replacement)
	}
	// Placeholders must not be HTML-escaped in the wire format.
	if !strings.Contains(rec.Body.String(), "<EMAIL_1>") {
		t.Errorf("response body HTML-escaped the placeholder: %s", rec.Body.String())
	}

	// A preview must not replace the live config.
	if !env.cfg.PII.EnableEmail {
		t.Error("preview mutated the live config")
	}
	if got := redactText(t, env.reg, text); !strings.Contains(got, "<EMAIL_1>") {
		t.Errorf("preview changed live redaction: %q", got)
	}
}

func TestPreviewGetMissingText(t *testing.T) {
	env := newTestEnv(t, nil)

	rec := env.serve(http.MethodGet, "/api/config/preview", nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)
	resp := decodeJSONBody[errorResponse](t, rec)
	if resp.Field != "text" {
		t.Errorf("field = %q, want text", resp.Field)
	}
}

func TestPreviewPostWithConfig(t *testing.T) {
	env := newTestEnv(t, nil)
	text := "My email is alice@example.com"
	body := mustJSON(t, map[string]any{
		"text": text,
		"config": map[string]any{
			"pii": map[string]any{
				"enable_email":  false,
				"enable_domain": false,
			},
		},
	})

	rec := env.serve(http.MethodPost, "/api/config/preview", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)

	resp := decodeJSONBody[previewResponse](t, rec)
	if resp.Redacted != text {
		t.Errorf("redacted = %q, want the original text", resp.Redacted)
	}
	if len(resp.Matches) != 0 {
		t.Errorf("matches = %+v, want none", resp.Matches)
	}
	if resp.Matches == nil {
		t.Error("matches = null, want an empty array")
	}

	// The supplied config applies to the preview only.
	if !env.cfg.PII.EnableEmail {
		t.Error("preview with a supplied config mutated the live config")
	}
	if got := redactText(t, env.reg, text); !strings.Contains(got, "<EMAIL_1>") {
		t.Errorf("preview with a supplied config changed live redaction: %q", got)
	}
}

func TestPreviewPostFallsBackToLiveConfig(t *testing.T) {
	env := newTestEnv(t, nil)

	for _, body := range [][]byte{
		mustJSON(t, map[string]any{"text": "alice@example.com"}),
		mustJSON(t, map[string]any{"text": "alice@example.com", "config": nil}),
	} {
		rec := env.serve(http.MethodPost, "/api/config/preview", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		resp := decodeJSONBody[previewResponse](t, rec)
		if !strings.Contains(resp.Redacted, "<EMAIL_1>") {
			t.Errorf("redacted = %q, want an email placeholder", resp.Redacted)
		}
	}
}

func TestPreviewPostMissingText(t *testing.T) {
	env := newTestEnv(t, nil)

	rec := env.serve(http.MethodPost, "/api/config/preview", []byte(`{"config":{}}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)
	resp := decodeJSONBody[errorResponse](t, rec)
	if resp.Field != "text" {
		t.Errorf("field = %q, want text", resp.Field)
	}
}

func TestPreviewPostRejectsInvalidConfig(t *testing.T) {
	env := newTestEnv(t, nil)
	body := mustJSON(t, map[string]any{
		"text": "alice@example.com",
		"config": map[string]any{
			"pii": map[string]any{
				"email": map[string]any{"patterns": []string{"("}},
			},
		},
	})

	rec := env.serve(http.MethodPost, "/api/config/preview", body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)
	resp := decodeJSONBody[errorResponse](t, rec)
	if resp.Field != "pii.email.patterns[0]" {
		t.Errorf("field = %q, want pii.email.patterns[0]", resp.Field)
	}
}

func TestPreviewPostMalformedJSON(t *testing.T) {
	env := newTestEnv(t, nil)

	rec := env.serve(http.MethodPost, "/api/config/preview", []byte("{"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	assertSecurityHeaders(t, rec)
	resp := decodeJSONBody[errorResponse](t, rec)
	if resp.Field != "body" {
		t.Errorf("field = %q, want body", resp.Field)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	env := newTestEnv(t, nil)

	tests := []struct {
		method string
		target string
		allow  string
	}{
		{http.MethodDelete, "/api/config", "GET, PUT"},
		{http.MethodPost, "/api/config", "GET, PUT"},
		{http.MethodGet, "/api/config/reload", "POST"},
		{http.MethodDelete, "/api/config/preview", "GET, POST"},
	}

	for _, tc := range tests {
		rec := env.serve(tc.method, tc.target, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status = %d, want 405", tc.method, tc.target, rec.Code)
			continue
		}
		assertSecurityHeaders(t, rec)
		if got := rec.Header().Get("Allow"); got != tc.allow {
			t.Errorf("%s %s Allow = %q, want %q", tc.method, tc.target, got, tc.allow)
		}
		resp := decodeJSONBody[errorResponse](t, rec)
		if resp.Error != "method not allowed" {
			t.Errorf("%s %s error = %q", tc.method, tc.target, resp.Error)
		}
	}
}

func TestValidationField(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("config: validate: listen.address must not be empty"), "listen.address"},
		{errors.New(`config: validate: upstream "openai": base_url must not be empty`), "upstream.openai.base_url"},
		{errors.New(`config: validate: upstream "gemini": host_match must not be empty`), "upstream.gemini.host_match"},
		{errors.New(`config: validate: pii.email.patterns[1]: error parsing regexp: missing closing )`), "pii.email.patterns[1]"},
		{errors.New("some other failure"), "some"},
	}
	for _, tc := range tests {
		if got := validationField(tc.err); got != tc.want {
			t.Errorf("validationField(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestNewHandlerNilConfigUsesDefaults(t *testing.T) {
	h := NewHandler(nil, "", nil, nil)

	if h.current() == nil {
		t.Fatal("current config is nil")
	}
	if got := h.current().Listen.Address; got != config.DefaultConfig().Listen.Address {
		t.Errorf("listen.address = %q, want the default", got)
	}
}
