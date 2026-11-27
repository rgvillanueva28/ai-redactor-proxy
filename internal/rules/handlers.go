// Package rules exposes the redaction configuration over HTTP.
//
// The API lets an operator inspect the running config, replace it, re-read it
// from disk, and preview how a sample string would be redacted. Every write is
// validated and persisted before it is applied, so a rejected edit leaves both
// the file and the live proxy untouched.
package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/handler"
	"github.com/local/redactor/internal/pii"
)

// maxBodyBytes bounds the size of a PUT or preview request body. Config
// documents are small; the limit exists to keep a hostile or broken client from
// making the process allocate without bound.
const maxBodyBytes = 1 << 20 // 1 MiB

// Handler serves the configuration API. It owns the current config and the
// file path it was loaded from, and it applies accepted edits to the live
// registry and adapter.
//
// The config pointer is replaced, never mutated, under mu, so callers can read
// a snapshot without holding the lock for the duration of an encode.
type Handler struct {
	mu       sync.RWMutex
	cfg      *config.Config
	path     string
	registry *pii.Registry
	adapter  *handler.Adapter
}

// NewHandler returns a Handler that serves cfg and, when a write succeeds,
// reloads reg and adapter. path is the TOML file used by PUT and the reload
// endpoint; an empty path makes both of those endpoints fail with 500 because
// there is nothing to persist to.
//
// A nil cfg is treated as the default configuration.
func NewHandler(cfg *config.Config, path string, reg *pii.Registry, adapter *handler.Adapter) *Handler {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return &Handler{
		cfg:      cfg,
		path:     path,
		registry: reg,
		adapter:  adapter,
	}
}

// Config handles GET and PUT on /api/config.
func (h *Handler) Config(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.current())
	case http.MethodPut:
		h.putConfig(w, r)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPut)
	}
}

// Reload handles POST on /api/config/reload. It re-reads the config file,
// validates it, and applies it to the live proxy.
func (h *Handler) Reload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.path == "" {
		writeError(w, http.StatusInternalServerError, "config file path is not configured", "")
		return
	}

	cfg, err := config.Load(h.path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), validationField(err))
		return
	}
	if err := handler.ReloadConfig(h.registry, h.adapter, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "apply config: "+err.Error(), "")
		return
	}

	h.cfg = cfg
	writeJSON(w, http.StatusOK, cfg)
}

// Preview handles GET and POST on /api/config/preview.
//
// GET takes the sample from the "text" query parameter and uses the running
// config. POST takes a JSON body of the form
//
//	{"text": "sample", "config": {...}}
//
// where "config" is optional and falls back to the running config when omitted.
// Either form builds a throwaway registry, so previewing never mutates the live
// detector set.
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.previewGet(w, r)
	case http.MethodPost:
		h.previewPost(w, r)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

// putConfig validates, saves, and applies a replacement config.
func (h *Handler) putConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := decodeConfig(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "body")
		return
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), validationField(err))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.path == "" {
		writeError(w, http.StatusInternalServerError, "config file path is not configured", "")
		return
	}
	if err := cfg.Save(h.path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	if err := handler.ReloadConfig(h.registry, h.adapter, cfg); err != nil {
		// The file already holds the new config. Report the apply failure so
		// the operator can fix the cause and use the reload endpoint, rather
		// than silently pretending the edit took effect.
		writeError(w, http.StatusInternalServerError, "apply config: "+err.Error(), "")
		return
	}

	h.cfg = cfg
	writeJSON(w, http.StatusOK, cfg)
}

// previewGet serves a preview against the running config.
func (h *Handler) previewGet(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if !query.Has("text") {
		writeError(w, http.StatusBadRequest, `missing required query parameter "text"`, "text")
		return
	}
	h.servePreview(w, h.current(), query.Get("text"))
}

// previewPost serves a preview against the config supplied in the body, or the
// running config when the body omits it.
func (h *Handler) previewPost(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "body")
		return
	}
	if req.Text == nil {
		writeError(w, http.StatusBadRequest, `missing required field "text"`, "text")
		return
	}

	cfg := h.current()
	if len(req.Config) > 0 && !isJSONNull(req.Config) {
		cfg = config.DefaultConfig()
		if err := json.Unmarshal(req.Config, cfg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: config: "+err.Error(), "config")
			return
		}
		config.MergeLegacyClientNames(&cfg.PII)
		if err := cfg.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), validationField(err))
			return
		}
	}

	h.servePreview(w, cfg, *req.Text)
}

// servePreview redacts text with a throwaway registry built from cfg.
func (h *Handler) servePreview(w http.ResponseWriter, cfg *config.Config, text string) {
	reg, err := pii.NewRegistry(cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config: "+err.Error(), "config")
		return
	}

	redacted, matches, mapping := reg.Redactor().RedactWithMatches(text)
	pii.PutMapping(mapping)

	resp := previewResponse{
		Redacted: redacted,
		Matches:  make([]previewMatch, 0, len(matches)),
	}
	for _, m := range matches {
		resp.Matches = append(resp.Matches, previewMatch{
			Category:    string(m.Category),
			Start:       m.Start,
			End:         m.End,
			Replacement: m.Replacement,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// current returns the config currently being served. The returned pointer is
// immutable: writes replace h.cfg rather than editing the value in place.
func (h *Handler) current() *config.Config {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cfg
}

// previewRequest is the POST body for /api/config/preview. Text is a pointer so
// a missing field can be told apart from an intentionally empty one.
type previewRequest struct {
	Text   *string         `json:"text"`
	Config json.RawMessage `json:"config,omitempty"`
}

// previewMatch describes one redaction in a preview response.
type previewMatch struct {
	Category    string `json:"category"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	Replacement string `json:"replacement"`
}

// previewResponse is the body returned by the preview endpoint.
type previewResponse struct {
	Redacted string         `json:"redacted"`
	Matches  []previewMatch `json:"matches"`
}

// errorResponse is the body returned for every non-2xx response. Field is set
// when the failure can be attributed to one part of the config document.
type errorResponse struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
}

// decodeConfig reads a full config document from r. Defaults are applied first,
// mirroring config.Load, so omitted keys keep their documented default. The
// deprecated [pii.client_names] block is folded into [pii.client] just as the
// TOML loader does.
func decodeConfig(w http.ResponseWriter, r *http.Request) (*config.Config, error) {
	cfg := config.DefaultConfig()
	if err := decodeJSON(w, r, cfg); err != nil {
		return nil, err
	}
	config.MergeLegacyClientNames(&cfg.PII)
	return cfg, nil
}

// decodeJSON decodes exactly one JSON value from the request body into dst and
// rejects trailing data. The body is capped at maxBodyBytes.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	defer r.Body.Close()

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected data after JSON value")
	}
	return nil
}

// isJSONNull reports whether raw is the JSON null literal, allowing callers to
// treat an explicit null like an omitted optional field.
func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// writeJSON writes v as JSON with the API's security headers. HTML escaping is
// disabled so placeholders such as <EMAIL_1> stay readable in preview output.
func writeJSON(w http.ResponseWriter, status int, v any) {
	setSecurityHeaders(w)
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		slog.Error("rules: encode response", "error", err)
	}
}

// writeError writes a JSON error response. field may be empty.
func writeError(w http.ResponseWriter, status int, msg, field string) {
	writeJSON(w, status, errorResponse{Error: msg, Field: field})
}

// methodNotAllowed answers with 405 and lists the methods the endpoint accepts.
func methodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed", "")
}

// setSecurityHeaders marks every API response as non-cacheable JSON that must
// not be MIME-sniffed. Config documents can contain client names and other
// sensitive literals, so they must not be stored by an intermediary.
func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// validationField extracts the config key a Validate error points at, so the
// JSON error can name the offending field. Validate's messages are stable and
// prefixed with "config: validate: "; this is a presentation helper, so an
// unrecognised shape falls back to the first token or the empty string.
func validationField(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimPrefix(err.Error(), "config: validate: ")

	if rest, ok := strings.CutPrefix(msg, "upstream "); ok {
		name, tail, found := strings.Cut(rest, ": ")
		if found {
			name = strings.Trim(name, `"`)
			if fields := strings.Fields(tail); len(fields) > 0 {
				return "upstream." + name + "." + fields[0]
			}
		}
	}
	if strings.HasPrefix(msg, "pii.") {
		if field, _, ok := strings.Cut(msg, ": "); ok {
			return field
		}
	}
	if fields := strings.Fields(msg); len(fields) > 0 {
		return fields[0]
	}
	return ""
}
