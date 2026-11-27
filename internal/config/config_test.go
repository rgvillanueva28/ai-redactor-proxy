package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

	// Per-category override blocks default to the zero value: no enable
	// override, and no names, patterns, or denylist entries.
	for name, cat := range map[string]CategoryConfig{
		"email":       pii.Email,
		"phone":       pii.Phone,
		"credit_card": pii.CreditCard,
		"ssn":         pii.SSN,
		"ip":          pii.IP,
		"path":        pii.Path,
		"api_key":     pii.APIKey,
		"domain":      pii.Domain,
		"client":      pii.Client,
		"credential":  pii.Credential,
		"person_name": pii.PersonName,
	} {
		if cat.Enabled != nil || len(cat.Names) != 0 || len(cat.Patterns) != 0 || len(cat.Denylist) != 0 {
			t.Errorf("default [pii.%s] = %#v, want zero value", name, cat)
		}
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

func boolPtr(v bool) *bool { return &v }

func TestParsePerCategoryBlocks(t *testing.T) {
	cfg, err := parse([]byte(`
[pii.email]
enabled = false
names = ["a@b.com"]
patterns = ["\\bX\\d+\\b"]
denylist = ["y@z.com"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	email := cfg.PII.Email
	if email.Enabled == nil {
		t.Fatal("pii.email.enabled = nil, want explicit false")
	}
	if *email.Enabled {
		t.Error("pii.email.enabled = true, want false")
	}
	if want := []string{"a@b.com"}; !reflect.DeepEqual(email.Names, want) {
		t.Errorf("pii.email.names = %#v, want %#v", email.Names, want)
	}
	if want := []string{`\bX\d+\b`}; !reflect.DeepEqual(email.Patterns, want) {
		t.Errorf("pii.email.patterns = %#v, want %#v", email.Patterns, want)
	}
	if want := []string{"y@z.com"}; !reflect.DeepEqual(email.Denylist, want) {
		t.Errorf("pii.email.denylist = %#v, want %#v", email.Denylist, want)
	}

	// Categories without a block stay at their zero value.
	if cfg.PII.Phone.Enabled != nil {
		t.Error("pii.phone.enabled = non-nil, want nil (unset)")
	}
	if len(cfg.PII.Phone.Names) != 0 || len(cfg.PII.Phone.Patterns) != 0 || len(cfg.PII.Phone.Denylist) != 0 {
		t.Errorf("pii.phone = %#v, want zero value", cfg.PII.Phone)
	}

	// Flat toggles are unaffected by per-category blocks.
	if !cfg.PII.EnableEmail {
		t.Error("enable_email = false, want default true")
	}
}

func TestParsePerCategoryNamesOnly(t *testing.T) {
	cfg, err := parse([]byte(`
[pii.email]
names = ["x@y.com"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.PII.Email.Enabled != nil {
		t.Error("pii.email.enabled = non-nil, want nil (unset)")
	}
	if want := []string{"x@y.com"}; !reflect.DeepEqual(cfg.PII.Email.Names, want) {
		t.Errorf("pii.email.names = %#v, want %#v", cfg.PII.Email.Names, want)
	}
}

func TestParseLegacyClientNamesMerged(t *testing.T) {
	cfg, err := parse([]byte(`
[pii.client_names]
names = ["Acme", "Project Phoenix"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := []string{"Acme", "Project Phoenix"}
	if !reflect.DeepEqual(cfg.PII.Client.Names, want) {
		t.Errorf("pii.client.names = %#v, want %#v", cfg.PII.Client.Names, want)
	}
	// The legacy field itself is left intact for callers that still read it.
	if !reflect.DeepEqual(cfg.PII.ClientNames.Names, want) {
		t.Errorf("pii.client_names.names = %#v, want %#v", cfg.PII.ClientNames.Names, want)
	}
}

func TestParseLegacyClientNamesAppendedAfterNewBlock(t *testing.T) {
	cfg, err := parse([]byte(`
[pii.client]
names = ["Globex"]
enabled = true

[pii.client_names]
names = ["Acme"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := []string{"Globex", "Acme"}
	if !reflect.DeepEqual(cfg.PII.Client.Names, want) {
		t.Errorf("pii.client.names = %#v, want %#v", cfg.PII.Client.Names, want)
	}
	if cfg.PII.Client.Enabled == nil || !*cfg.PII.Client.Enabled {
		t.Error("pii.client.enabled = nil or false, want explicit true")
	}
}

func TestResolveEnabled(t *testing.T) {
	tests := []struct {
		name       string
		override   *bool
		flatToggle bool
		want       bool
	}{
		{name: "nil falls back to enabled flat toggle", override: nil, flatToggle: true, want: true},
		{name: "nil falls back to disabled flat toggle", override: nil, flatToggle: false, want: false},
		{name: "true overrides disabled flat toggle", override: boolPtr(true), flatToggle: false, want: true},
		{name: "false overrides enabled flat toggle", override: boolPtr(false), flatToggle: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveEnabled(CategoryConfig{Enabled: tt.override}, tt.flatToggle)
			if got != tt.want {
				t.Errorf("ResolveEnabled(flat=%v) = %v, want %v", tt.flatToggle, got, tt.want)
			}
		})
	}
}

// saveRoundTripTOML is a non-trivial document: legacy client names, per-category
// enable overrides, custom names, patterns and a denylist, non-default upstreams
// and the UI toggle. It exercises every branch Save has to encode.
const saveRoundTripTOML = sampleTOML + `
[pii.email]
enabled = true
names = ["alerts@corp.io"]

[pii.credit_card]
patterns = ["\\bCC-\\d{4}-\\d{4}\\b"]
denylist = ["4111 1111 1111 1111"]
`

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(src, []byte(saveRoundTripTOML), 0o600); err != nil {
		t.Fatalf("write source config: %v", err)
	}

	first, err := Load(src)
	if err != nil {
		t.Fatalf("Load source: %v", err)
	}

	saved := filepath.Join(dir, "saved.toml")
	if err := first.Save(saved); err != nil {
		t.Fatalf("Save: %v", err)
	}

	second, err := Load(saved)
	if err != nil {
		t.Fatalf("Load saved: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("round trip changed the config:\n first = %#v\nsecond = %#v", first, second)
	}

	// Spot-check the values that are easiest to lose when encoding.
	if second.Listen.Address != "127.0.0.1:9443" {
		t.Errorf("listen.address = %q, want 127.0.0.1:9443", second.Listen.Address)
	}
	if !second.PII.LogRedacted || !second.PII.LogBodies {
		t.Errorf("log toggles = (%v, %v), want (true, true)", second.PII.LogRedacted, second.PII.LogBodies)
	}
	if want := []string{`\bCC-\d{4}-\d{4}\b`}; !reflect.DeepEqual(second.PII.CreditCard.Patterns, want) {
		t.Errorf("credit_card.patterns = %#v, want %#v", second.PII.CreditCard.Patterns, want)
	}
	if second.PII.Email.Enabled == nil || !*second.PII.Email.Enabled {
		t.Errorf("email.enabled = %v, want explicit true", second.PII.Email.Enabled)
	}
	if second.UI.Enable {
		t.Error("ui.enable = true, want false")
	}
}

// TestSaveLegacyClientNamesRoundTripIsIdempotent pins the reason
// MergeLegacyClientNames dedupes: Save writes both the merged [pii.client]
// block and the legacy [pii.client_names] block, so a second Load must not
// append the legacy names again.
func TestSaveLegacyClientNamesRoundTripIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(src, []byte(`
[pii.client_names]
names = ["Acme Corp", "Globex"]
`), 0o600); err != nil {
		t.Fatalf("write source config: %v", err)
	}

	first, err := Load(src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := []string{"Acme Corp", "Globex"}; !reflect.DeepEqual(first.PII.Client.Names, want) {
		t.Fatalf("client names = %#v, want %#v", first.PII.Client.Names, want)
	}

	saved := filepath.Join(dir, "saved.toml")
	if err := first.Save(saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	second, err := Load(saved)
	if err != nil {
		t.Fatalf("Load saved: %v", err)
	}
	if want := []string{"Acme Corp", "Globex"}; !reflect.DeepEqual(second.PII.Client.Names, want) {
		t.Fatalf("client names after round trip = %#v, want %#v", second.PII.Client.Names, want)
	}
}

func TestSaveOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("not toml at all"), 0o600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Listen.Address = "127.0.0.1:7777"
	cfg.PII.Email.Patterns = []string{`\bX-\d+\b`}

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save over existing file: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Listen.Address != "127.0.0.1:7777" {
		t.Errorf("listen.address = %q, want 127.0.0.1:7777", got.Listen.Address)
	}
	if want := []string{`\bX-\d+\b`}; !reflect.DeepEqual(got.PII.Email.Patterns, want) {
		t.Errorf("email.patterns = %#v, want %#v", got.PII.Email.Patterns, want)
	}

	// The temporary file must be gone after a successful save.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("directory entries = %v, want only config.toml", names)
	}
}

func TestSaveMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "config.toml")
	err := DefaultConfig().Save(path)
	if err == nil {
		t.Fatal("Save into a missing directory returned nil error")
	}
	if !strings.Contains(err.Error(), "config: save") {
		t.Errorf("error = %q, want save context", err)
	}
}

func TestSaveFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not map Unix permission bits")
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := DefaultConfig().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file permissions = %o, want 600", perm)
	}
}

func TestValidateAcceptsDefaults(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("Validate(DefaultConfig) = %v, want nil", err)
	}
	if err := (*Config)(nil).Validate(); err == nil {
		t.Fatal("Validate(nil) returned nil error")
	}
}

func TestValidateRejectsBadListenAddress(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen.Address = "   "
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate with empty listen.address returned nil error")
	}
	if !strings.Contains(err.Error(), "config: validate:") || !strings.Contains(err.Error(), "listen.address") {
		t.Errorf("error = %q, want validate context naming listen.address", err)
	}
}

func TestValidateRejectsBadUpstream(t *testing.T) {
	tests := []struct {
		name string
		up   UpstreamConfig
		want string
	}{
		{"missing_host_match", UpstreamConfig{BaseURL: "https://api.example.com"}, "host_match"},
		{"missing_base_url", UpstreamConfig{HostMatch: "api.example.com"}, "base_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Upstream["broken"] = tt.up
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate returned nil error")
			}
			if !strings.Contains(err.Error(), `upstream "broken"`) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to name upstream \"broken\" and %s", err, tt.want)
			}
		})
	}
}

func TestValidateRejectsInvalidPatternNamingCategory(t *testing.T) {
	// Every per-category block must be checked, and the error has to say which
	// one is broken so the API can point at the offending field.
	for _, cat := range DefaultConfig().categoryConfigs() {
		t.Run(cat.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.setCategoryPatterns(cat.name, []string{`(unclosed`})

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate accepted an invalid pattern")
			}
			msg := err.Error()
			if !strings.Contains(msg, "config: validate:") {
				t.Errorf("error = %q, want validate context", msg)
			}
			if want := "pii." + cat.name + ".patterns[0]"; !strings.Contains(msg, want) {
				t.Errorf("error = %q, want it to contain %q", msg, want)
			}
		})
	}
}

func TestValidateAcceptsValidPatterns(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PII.Email.Patterns = []string{`\bX-\d+\b`}
	cfg.PII.Phone.Patterns = []string{`\+1\d{10}`}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// setCategoryPatterns writes patterns into the named per-category block. It
// keeps TestValidateRejectsInvalidPatternNamingCategory from listing every
// field by hand and silently drifting from categoryConfigs.
func (c *Config) setCategoryPatterns(name string, patterns []string) {
	switch name {
	case "email":
		c.PII.Email.Patterns = patterns
	case "phone":
		c.PII.Phone.Patterns = patterns
	case "credit_card":
		c.PII.CreditCard.Patterns = patterns
	case "ssn":
		c.PII.SSN.Patterns = patterns
	case "ip":
		c.PII.IP.Patterns = patterns
	case "path":
		c.PII.Path.Patterns = patterns
	case "api_key":
		c.PII.APIKey.Patterns = patterns
	case "domain":
		c.PII.Domain.Patterns = patterns
	case "client":
		c.PII.Client.Patterns = patterns
	case "credential":
		c.PII.Credential.Patterns = patterns
	case "person_name":
		c.PII.PersonName.Patterns = patterns
	}
}

func TestJSONRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen.Address = "0.0.0.0:9000"
	cfg.PII.LogRedacted = true
	cfg.PII.LogBodies = true
	cfg.PII.EnableEmail = false
	yes := true
	cfg.PII.Phone.Enabled = &yes
	cfg.PII.Email.Names = []string{"alerts@corp.io"}
	cfg.PII.Email.Patterns = []string{`\bX-\d+\b`}
	cfg.PII.Client.Denylist = []string{"Acme"}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(cfg, &back) {
		t.Fatalf("JSON round trip changed the config:\n got = %#v\nwant = %#v", &back, cfg)
	}

	// The wire format uses the toml key names so the API and the file agree.
	for _, key := range []string{
		`"listen"`, `"pii"`, `"upstream"`, `"ui"`,
		`"enable_email"`, `"log_redacted"`, `"log_bodies"`,
		`"credit_card"`, `"api_key"`, `"person_name"`, `"client_names"`,
		`"enabled"`, `"names"`, `"patterns"`, `"denylist"`,
		`"host_match"`, `"base_url"`,
	} {
		if !strings.Contains(string(data), key) {
			t.Errorf("JSON output is missing key %s:\n%s", key, data)
		}
	}
}

func TestJSONRoundTripDefaultConfig(t *testing.T) {
	data, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(&back, DefaultConfig()) {
		t.Fatalf("JSON round trip changed the defaults:\n got = %#v\nwant = %#v", &back, DefaultConfig())
	}
}
