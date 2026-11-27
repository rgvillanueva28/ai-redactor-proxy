// Package config loads the redactor proxy configuration from TOML.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the top-level configuration document.
type Config struct {
	Listen   ListenConfig              `toml:"listen" json:"listen"`
	PII      PIIConfig                 `toml:"pii" json:"pii"`
	Upstream map[string]UpstreamConfig `toml:"upstream" json:"upstream"`
	UI       UIConfig                  `toml:"ui" json:"ui"`
}

// ListenConfig configures the local listener.
type ListenConfig struct {
	Address string `toml:"address" json:"address"`
}

// PIIConfig toggles individual PII detectors and configures static data used
// by them.
type PIIConfig struct {
	EnableEmail      bool `toml:"enable_email" json:"enable_email"`
	EnablePhone      bool `toml:"enable_phone" json:"enable_phone"`
	EnableCreditCard bool `toml:"enable_creditcard" json:"enable_creditcard"`
	EnableSSN        bool `toml:"enable_ssn" json:"enable_ssn"`
	EnableIP         bool `toml:"enable_ip" json:"enable_ip"`
	EnablePath       bool `toml:"enable_path" json:"enable_path"`
	EnableAPIKey     bool `toml:"enable_apikey" json:"enable_apikey"`
	EnableCredential bool `toml:"enable_credential" json:"enable_credential"`
	EnableDomain     bool `toml:"enable_domain" json:"enable_domain"`
	EnablePersonName bool `toml:"enable_person_name" json:"enable_person_name"`
	EnableClientName bool `toml:"enable_client_name" json:"enable_client_name"`

	// Per-category overrides, one TOML block each. A block may replace the
	// flat enable toggle, add literal names, add regex patterns, and list
	// values that must never be redacted.
	Email      CategoryConfig `toml:"email" json:"email"`
	Phone      CategoryConfig `toml:"phone" json:"phone"`
	CreditCard CategoryConfig `toml:"credit_card" json:"credit_card"`
	SSN        CategoryConfig `toml:"ssn" json:"ssn"`
	IP         CategoryConfig `toml:"ip" json:"ip"`
	Path       CategoryConfig `toml:"path" json:"path"`
	APIKey     CategoryConfig `toml:"api_key" json:"api_key"`
	Domain     CategoryConfig `toml:"domain" json:"domain"`
	Client     CategoryConfig `toml:"client" json:"client"`
	Credential CategoryConfig `toml:"credential" json:"credential"`
	PersonName CategoryConfig `toml:"person_name" json:"person_name"`

	// ClientNames is the legacy [pii.client_names] block. Its names are
	// merged into Client.Names during parse.
	ClientNames ClientNamesConfig `toml:"client_names" json:"client_names"`

	LogRedacted bool `toml:"log_redacted" json:"log_redacted"`
	LogBodies   bool `toml:"log_bodies" json:"log_bodies"`
}

// Replacement modes supported by ResolveMode.
const (
	// ModePlaceholder replaces each detected value with a numbered
	// <CATEGORY_n> token such as <EMAIL_1>.
	ModePlaceholder = "placeholder"
	// ModeCustom replaces each detected value with a user-supplied string
	// such as "[EMAIL]".
	ModeCustom = "custom"
)

// Replacement-mode validation errors. Callers can match them with errors.Is.
var (
	// ErrInvalidMode is wrapped when a category config names an unknown
	// replacement mode.
	ErrInvalidMode = errors.New("invalid replacement mode")
	// ErrMissingReplacementText is wrapped when custom mode is selected but
	// no replacement text was supplied.
	ErrMissingReplacementText = errors.New("missing replacement text")
)

// CategoryConfig customizes a single PII category. Enabled is a pointer so an
// omitted key (nil) can be distinguished from an explicit false.
type CategoryConfig struct {
	Enabled  *bool    `toml:"enabled" json:"enabled"`
	Names    []string `toml:"names" json:"names"`
	Patterns []string `toml:"patterns" json:"patterns"`
	Denylist []string `toml:"denylist" json:"denylist"`

	// Mode selects how detected values are replaced: "placeholder" (the
	// default) or "custom". An omitted/empty mode means "placeholder".
	Mode string `toml:"mode" json:"mode"`
	// Text is the replacement text used when Mode is "custom", for example
	// "[EMAIL]". It is ignored by other modes.
	Text string `toml:"text" json:"text"`
}

// ClientNamesConfig holds literal client names that must always be redacted.
type ClientNamesConfig struct {
	Names []string `toml:"names" json:"names"`
}

// ResolveEnabled returns the effective enabled state for a category. If the
// per-category block sets Enabled, it wins; otherwise the flat toggle is used.
func ResolveEnabled(cat CategoryConfig, flatToggle bool) bool {
	if cat.Enabled != nil {
		return *cat.Enabled
	}
	return flatToggle
}

// ResolveMode validates a category's replacement settings and returns the
// effective mode together with the replacement text it should use.
//
// An omitted mode defaults to "placeholder" and yields no replacement text.
// "custom" requires non-empty Text; anything else is rejected.
func ResolveMode(cat CategoryConfig) (mode, text string, err error) {
	switch cat.Mode {
	case "", ModePlaceholder:
		return ModePlaceholder, "", nil
	case ModeCustom:
		if cat.Text == "" {
			return "", "", fmt.Errorf("config: custom mode requires replacement text: %w", ErrMissingReplacementText)
		}
		return ModeCustom, cat.Text, nil
	default:
		return "", "", fmt.Errorf("config: invalid mode %q: %w", cat.Mode, ErrInvalidMode)
	}
}

// MergeLegacyClientNames folds the deprecated [pii.client_names] list into
// [pii.client].names, keeping any names supplied in the new block first.
//
// Duplicates (compared case-insensitively after trimming) are dropped so that a
// Load → Save → Load cycle is idempotent: Save writes both blocks, and the
// second load would otherwise append the legacy names a second time.
func MergeLegacyClientNames(pii *PIIConfig) {
	if len(pii.ClientNames.Names) == 0 {
		return
	}

	seen := make(map[string]struct{}, len(pii.Client.Names)+len(pii.ClientNames.Names))
	for _, name := range pii.Client.Names {
		seen[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	for _, name := range pii.ClientNames.Names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		pii.Client.Names = append(pii.Client.Names, name)
		seen[key] = struct{}{}
	}
}

// UpstreamConfig describes one upstream provider.
type UpstreamConfig struct {
	HostMatch string `toml:"host_match" json:"host_match"`
	BaseURL   string `toml:"base_url" json:"base_url"`
}

// UIConfig controls the embedded log viewer served by the proxy.
type UIConfig struct {
	// Enable controls whether the log viewer and its event stream are served
	// at /logs. Disabling it leaves every /logs path unregistered so the
	// catch-all proxy handler treats them as ordinary upstream requests.
	Enable bool `toml:"enable" json:"enable"`
}

// DefaultConfig returns the default configuration. Values match the documented
// sample TOML. Per-category override blocks default to their zero value, so
// every category starts with no names, patterns, or denylist entries and no
// explicit enable override.
func DefaultConfig() *Config {
	return &Config{
		Listen: ListenConfig{Address: "127.0.0.1:28443"},
		PII: PIIConfig{
			EnableEmail:      true,
			EnablePhone:      true,
			EnableCreditCard: true,
			EnableSSN:        true,
			EnableIP:         true,
			EnablePath:       true,
			EnableAPIKey:     true,
			EnableCredential: true,
			EnableDomain:     true,
			EnablePersonName: false,
			EnableClientName: true,
			ClientNames:      ClientNamesConfig{Names: nil},
			LogRedacted:      false,
			LogBodies:        false,
		},
		Upstream: map[string]UpstreamConfig{
			"openai": {
				HostMatch: "api.openai.com",
				BaseURL:   "https://api.openai.com",
			},
			"anthropic": {
				HostMatch: "api.anthropic.com",
				BaseURL:   "https://api.anthropic.com",
			},
			"gemini": {
				HostMatch: "generativelanguage.googleapis.com",
				BaseURL:   "https://generativelanguage.googleapis.com",
			},
		},
		UI: UIConfig{Enable: true},
	}
}

// Load reads and decodes the TOML file at path.
//
// Defaults are applied first and the file's values override them, so any key
// omitted from the file keeps its default. Unknown keys are ignored (the
// BurntSushi/toml default) rather than treated as errors; this keeps older
// binaries usable with newer config files.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}
	cfg, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", path, err)
	}
	return cfg, nil
}

// parse decodes TOML data on top of DefaultConfig.
func parse(data []byte) (*Config, error) {
	cfg := DefaultConfig()
	if _, err := toml.Decode(string(data), cfg); err != nil {
		return nil, err
	}
	MergeLegacyClientNames(&cfg.PII)
	return cfg, nil
}

// categoryConfigs lists the per-category override blocks with the names used in
// error messages, in the same fixed order the registry wires detectors. The
// order only exists to make validation errors deterministic.
func (c *Config) categoryConfigs() []struct {
	name string
	cfg  CategoryConfig
} {
	p := c.PII
	return []struct {
		name string
		cfg  CategoryConfig
	}{
		{"email", p.Email},
		{"phone", p.Phone},
		{"credit_card", p.CreditCard},
		{"ssn", p.SSN},
		{"ip", p.IP},
		{"path", p.Path},
		{"api_key", p.APIKey},
		{"domain", p.Domain},
		{"client", p.Client},
		{"credential", p.Credential},
		{"person_name", p.PersonName},
	}
}

// Validate checks the parts of the configuration that cannot be fixed later:
// the listener address, every upstream entry, and every per-category regex
// pattern. It is meant to be called before Save and before applying a reloaded
// configuration, so an invalid edit is rejected instead of replacing a working
// configuration with one that fails at request time.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config: validate: nil config")
	}

	if strings.TrimSpace(c.Listen.Address) == "" {
		return fmt.Errorf("config: validate: listen.address must not be empty")
	}

	// Sort the upstream names so the reported error is stable across runs;
	// Go map iteration order is randomised.
	names := make([]string, 0, len(c.Upstream))
	for name := range c.Upstream {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		up := c.Upstream[name]
		if strings.TrimSpace(up.HostMatch) == "" {
			return fmt.Errorf("config: validate: upstream %q: host_match must not be empty", name)
		}
		if strings.TrimSpace(up.BaseURL) == "" {
			return fmt.Errorf("config: validate: upstream %q: base_url must not be empty", name)
		}
	}

	for _, cat := range c.categoryConfigs() {
		for i, pattern := range cat.cfg.Patterns {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("config: validate: pii.%s.patterns[%d]: %w", cat.name, i, err)
			}
		}
	}

	return nil
}

// Save encodes cfg as TOML and writes it to path atomically.
//
// The data is written to a temporary file in the same directory as path and
// then renamed over the target. Keeping the temporary file in the destination
// directory makes os.Rename atomic on every platform, including Windows, where
// a cross-volume rename would fail. The file is created with 0o600 permissions
// because it may contain client names and other sensitive literals.
//
// Save encodes exactly what is in memory: it does not merge the deprecated
// [pii.client_names] block first, so callers that construct a Config directly
// control the output.
func (c *Config) Save(path string) error {
	if c == nil {
		return fmt.Errorf("config: save %q: nil config", path)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.toml.tmp")
	if err != nil {
		return fmt.Errorf("config: save %q: create temp file: %w", path, err)
	}
	tmpName := tmp.Name()

	// os.CreateTemp already uses 0o600 on Unix, but set it explicitly so the
	// guarantee does not depend on the platform default.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("config: save %q: set permissions: %w", path, err)
	}

	if err := toml.NewEncoder(tmp).Encode(c); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("config: save %q: encode: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("config: save %q: sync: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("config: save %q: close: %w", path, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("config: save %q: rename: %w", path, err)
	}
	return nil
}
