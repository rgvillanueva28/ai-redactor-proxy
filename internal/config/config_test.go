package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sampleTOML = `
[listen]
address = "127.0.0.1:9443"

[pii]
enable_email = false
enable_phone = true
enable_creditcard = false
enable_ssn = true
enable_ip = false
enable_path = true
enable_apikey = false
enable_domain = true
enable_person_name = true
enable_client_name = true
log_redacted = true
log_bodies = true

[pii.client_names]
names = ["Acme Corp", "Project Phoenix", "John Smith"]

[upstream.openai]
host_match = "api.openai.com"
base_url = "https://api.openai.com"

[upstream.anthropic]
host_match = "api.anthropic.com"
base_url = "https://api.anthropic.com"

[upstream.gemini]
host_match = "generativelanguage.googleapis.com"
base_url = "https://generativelanguage.googleapis.com"

[ui]
enable = false
`

func TestParseValidTOML(t *testing.T) {
	cfg, err := parse([]byte(sampleTOML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if cfg.Listen.Address != "127.0.0.1:9443" {
		t.Errorf("listen.address = %q, want 127.0.0.1:9443", cfg.Listen.Address)
	}

	pii := cfg.PII
	if pii.EnableEmail {
		t.Error("enable_email = true, want false")
	}
	if !pii.EnablePhone {
		t.Error("enable_phone = false, want true")
	}
	if pii.EnableCreditCard {
		t.Error("enable_creditcard = true, want false")
	}
	if !pii.EnableSSN {
		t.Error("enable_ssn = false, want true")
	}
	if pii.EnableIP {
		t.Error("enable_ip = true, want false")
	}
	if !pii.EnablePath {
		t.Error("enable_path = false, want true")
	}
	if pii.EnableAPIKey {
		t.Error("enable_apikey = true, want false")
	}
	if !pii.EnableDomain {
		t.Error("enable_domain = false, want true")
	}
	if !pii.EnablePersonName {
		t.Error("enable_person_name = false, want true")
	}
	if !pii.EnableClientName {
		t.Error("enable_client_name = false, want true")
	}
	if !pii.LogRedacted {
		t.Error("log_redacted = false, want true")
	}
	if !pii.LogBodies {
		t.Error("log_bodies = false, want true")
	}

	wantNames := []string{"Acme Corp", "Project Phoenix", "John Smith"}
	if !reflect.DeepEqual(pii.ClientNames.Names, wantNames) {
		t.Errorf("client names = %#v, want %#v", pii.ClientNames.Names, wantNames)
	}

	if len(cfg.Upstream) != 3 {
		t.Fatalf("upstream count = %d, want 3", len(cfg.Upstream))
	}
	wantUpstreams := map[string]UpstreamConfig{
		"openai":    {HostMatch: "api.openai.com", BaseURL: "https://api.openai.com"},
		"anthropic": {HostMatch: "api.anthropic.com", BaseURL: "https://api.anthropic.com"},
		"gemini": {
			HostMatch: "generativelanguage.googleapis.com",
			BaseURL:   "https://generativelanguage.googleapis.com",
		},
	}
	if !reflect.DeepEqual(cfg.Upstream, wantUpstreams) {
		t.Errorf("upstream = %#v, want %#v", cfg.Upstream, wantUpstreams)
	}

	if cfg.UI.Enable {
		t.Error("ui.enable = true, want false")
	}
}

func TestParseEmptyTOMLUsesDefaults(t *testing.T) {
	cfg, err := parse([]byte(""))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Fatalf("empty config = %#v, want defaults %#v", cfg, DefaultConfig())
	}
}

func TestParseOmittedFieldsKeepDefaults(t *testing.T) {
	cfg, err := parse([]byte(`
[listen]
address = "0.0.0.0:1234"

[pii]
enable_email = false
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if cfg.Listen.Address != "0.0.0.0:1234" {
		t.Errorf("listen.address = %q, want 0.0.0.0:1234", cfg.Listen.Address)
	}
	if cfg.PII.EnableEmail {
		t.Error("enable_email = true, want false (explicitly set)")
	}
	// Omitted toggles fall back to defaults.
	if !cfg.PII.EnablePhone {
		t.Error("enable_phone = false, want default true")
	}
	if !cfg.PII.EnableDomain {
		t.Error("enable_domain = false, want default true")
	}
	if cfg.PII.EnablePersonName {
		t.Error("enable_person_name = true, want default false")
	}
	if !cfg.PII.EnableClientName {
		t.Error("enable_client_name = false, want default true")
	}
	// Omitted upstream blocks fall back to defaults.
	if len(cfg.Upstream) != 3 {
		t.Fatalf("upstream count = %d, want default 3", len(cfg.Upstream))
	}
	if cfg.Upstream["gemini"].HostMatch != "generativelanguage.googleapis.com" {
		t.Errorf("gemini host_match = %q, want default", cfg.Upstream["gemini"].HostMatch)
	}
	// An omitted [ui] section leaves the viewer enabled.
	if !cfg.UI.Enable {
		t.Error("ui.enable = false, want default true")
	}
}

func TestParseUIToggle(t *testing.T) {
	cfg, err := parse([]byte(`
[ui]
enable = false
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.UI.Enable {
		t.Error("ui.enable = true, want false (explicitly set)")
	}

	cfg, err = parse([]byte(`
[ui]
enable = true
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.UI.Enable {
		t.Error("ui.enable = false, want true (explicitly set)")
	}
}

func TestParseUnknownFieldsAreIgnored(t *testing.T) {
	// Documented behaviour: BurntSushi/toml ignores keys that have no matching
	// struct field unless the caller checks the returned MetaData. Load/parse
	// only report syntax and type errors, so unknown keys are backward
	// compatible.
	cfg, err := parse([]byte(`
[listen]
address = "127.0.0.1:8080"
future_option = "ignored"

[unknown_section]
also_ignored = 42
`))
	if err != nil {
		t.Fatalf("parse with unknown fields returned error: %v", err)
	}
	if cfg.Listen.Address != "127.0.0.1:8080" {
		t.Errorf("listen.address = %q, want 127.0.0.1:8080", cfg.Listen.Address)
	}
}

func TestParseRejectsInvalidTOML(t *testing.T) {
	if _, err := parse([]byte("this is not toml")); err == nil {
		t.Fatal("parse of invalid TOML returned nil error")
	}
}

func TestParseRejectsTypeMismatch(t *testing.T) {
	if _, err := parse([]byte("[listen]\naddress = 42\n")); err == nil {
		t.Fatal("parse of mismatched type returned nil error")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(sampleTOML), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen.Address != "127.0.0.1:9443" {
		t.Errorf("listen.address = %q, want 127.0.0.1:9443", cfg.Listen.Address)
	}
	if got := cfg.PII.ClientNames.Names; len(got) != 3 || got[0] != "Acme Corp" {
		t.Errorf("client names = %#v, want 3 entries starting with Acme Corp", got)
	}
	if cfg.Upstream["openai"].BaseURL != "https://api.openai.com" {
		t.Errorf("openai base_url = %q", cfg.Upstream["openai"].BaseURL)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err == nil {
		t.Fatal("Load of missing file returned nil error")
	}
	if !strings.Contains(err.Error(), "config: read") {
		t.Errorf("error = %q, want read error context", err)
	}
}

func TestLoadInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(path, []byte("[[["), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load of invalid TOML returned nil error")
	}
	if !strings.Contains(err.Error(), "config: parse") {
		t.Errorf("error = %q, want parse error context", err)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Listen.Address != "127.0.0.1:8443" {
		t.Errorf("listen.address = %q, want 127.0.0.1:8443", cfg.Listen.Address)
	}

	pii := cfg.PII
	for name, enabled := range map[string]bool{
		"email":       pii.EnableEmail,
		"phone":       pii.EnablePhone,
		"creditcard":  pii.EnableCreditCard,
		"ssn":         pii.EnableSSN,
		"ip":          pii.EnableIP,
		"path":        pii.EnablePath,
		"apikey":      pii.EnableAPIKey,
		"domain":      pii.EnableDomain,
		"client_name": pii.EnableClientName,
	} {
		if !enabled {
			t.Errorf("default %s toggle = false, want true", name)
		}
	}
	if pii.EnablePersonName {
		t.Error("default enable_person_name = true, want false")
	}
	if pii.LogRedacted {
		t.Error("default log_redacted = true, want false")
	}
	if pii.LogBodies {
		t.Error("default log_bodies = true, want false")
	}
	if len(pii.ClientNames.Names) != 0 {
		t.Errorf("default client names = %#v, want empty", pii.ClientNames.Names)
	}

	wantUpstreams := map[string]UpstreamConfig{
		"openai":    {HostMatch: "api.openai.com", BaseURL: "https://api.openai.com"},
		"anthropic": {HostMatch: "api.anthropic.com", BaseURL: "https://api.anthropic.com"},
		"gemini": {
			HostMatch: "generativelanguage.googleapis.com",
			BaseURL:   "https://generativelanguage.googleapis.com",
		},
	}
	if !reflect.DeepEqual(cfg.Upstream, wantUpstreams) {
		t.Errorf("default upstream = %#v, want %#v", cfg.Upstream, wantUpstreams)
	}

	if !cfg.UI.Enable {
		t.Error("default ui.enable = false, want true")
	}
}
