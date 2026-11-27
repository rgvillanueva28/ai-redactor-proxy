// Package config loads the redactor proxy configuration from TOML.
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config is the top-level configuration document.
type Config struct {
	Listen   ListenConfig              `toml:"listen"`
	PII      PIIConfig                 `toml:"pii"`
	Upstream map[string]UpstreamConfig `toml:"upstream"`
	UI       UIConfig                  `toml:"ui"`
}

// ListenConfig configures the local listener.
type ListenConfig struct {
	Address string `toml:"address"`
}

// PIIConfig toggles individual PII detectors and configures static data used
// by them.
type PIIConfig struct {
	EnableEmail      bool `toml:"enable_email"`
	EnablePhone      bool `toml:"enable_phone"`
	EnableCreditCard bool `toml:"enable_creditcard"`
	EnableSSN        bool `toml:"enable_ssn"`
	EnableIP         bool `toml:"enable_ip"`
	EnablePath       bool `toml:"enable_path"`
	EnableAPIKey     bool `toml:"enable_apikey"`
	EnableCredential bool `toml:"enable_credential"`
	EnableDomain     bool `toml:"enable_domain"`
	EnablePersonName bool `toml:"enable_person_name"`
	EnableClientName bool `toml:"enable_client_name"`

	// Per-category overrides, one TOML block each. A block may replace the
	// flat enable toggle, add literal names, add regex patterns, and list
	// values that must never be redacted.
	Email      CategoryConfig `toml:"email"`
	Phone      CategoryConfig `toml:"phone"`
	CreditCard CategoryConfig `toml:"credit_card"`
	SSN        CategoryConfig `toml:"ssn"`
	IP         CategoryConfig `toml:"ip"`
	Path       CategoryConfig `toml:"path"`
	APIKey     CategoryConfig `toml:"api_key"`
	Domain     CategoryConfig `toml:"domain"`
	Client     CategoryConfig `toml:"client"`
	Credential CategoryConfig `toml:"credential"`
	PersonName CategoryConfig `toml:"person_name"`

	// ClientNames is the legacy [pii.client_names] block. Its names are
	// merged into Client.Names during parse.
	ClientNames ClientNamesConfig `toml:"client_names"`

	LogRedacted bool `toml:"log_redacted"`
	LogBodies   bool `toml:"log_bodies"`
}

// CategoryConfig customizes a single PII category. Enabled is a pointer so an
// omitted key (nil) can be distinguished from an explicit false.
type CategoryConfig struct {
	Enabled  *bool    `toml:"enabled"`
	Names    []string `toml:"names"`
	Patterns []string `toml:"patterns"`
	Denylist []string `toml:"denylist"`
}

// ClientNamesConfig holds literal client names that must always be redacted.
type ClientNamesConfig struct {
	Names []string `toml:"names"`
}

// ResolveEnabled returns the effective enabled state for a category. If the
// per-category block sets Enabled, it wins; otherwise the flat toggle is used.
func ResolveEnabled(cat CategoryConfig, flatToggle bool) bool {
	if cat.Enabled != nil {
		return *cat.Enabled
	}
	return flatToggle
}

// MergeLegacyClientNames folds the deprecated [pii.client_names] list into
// [pii.client].names, keeping any names supplied in the new block first.
func MergeLegacyClientNames(pii *PIIConfig) {
	if len(pii.ClientNames.Names) == 0 {
		return
	}
	pii.Client.Names = append(pii.Client.Names, pii.ClientNames.Names...)
}

// UpstreamConfig describes one upstream provider.
type UpstreamConfig struct {
	HostMatch string `toml:"host_match"`
	BaseURL   string `toml:"base_url"`
}

// UIConfig controls the embedded log viewer served by the proxy.
type UIConfig struct {
	// Enable controls whether the log viewer and its event stream are served
	// at /logs. Disabling it leaves every /logs path unregistered so the
	// catch-all proxy handler treats them as ordinary upstream requests.
	Enable bool `toml:"enable"`
}

// DefaultConfig returns the default configuration. Values match the documented
// sample TOML. Per-category override blocks default to their zero value, so
// every category starts with no names, patterns, or denylist entries and no
// explicit enable override.
func DefaultConfig() *Config {
	return &Config{
		Listen: ListenConfig{Address: "127.0.0.1:8443"},
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
