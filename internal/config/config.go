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
}

// ListenConfig configures the local listener.
type ListenConfig struct {
	Address string `toml:"address"`
}

// PIIConfig toggles individual PII detectors and configures static data used
// by them.
type PIIConfig struct {
	EnableEmail      bool              `toml:"enable_email"`
	EnablePhone      bool              `toml:"enable_phone"`
	EnableCreditCard bool              `toml:"enable_creditcard"`
	EnableSSN        bool              `toml:"enable_ssn"`
	EnableIP         bool              `toml:"enable_ip"`
	EnablePath       bool              `toml:"enable_path"`
	EnableAPIKey     bool              `toml:"enable_apikey"`
	EnableDomain     bool              `toml:"enable_domain"`
	EnablePersonName bool              `toml:"enable_person_name"`
	EnableClientName bool              `toml:"enable_client_name"`
	ClientNames      ClientNamesConfig `toml:"client_names"`
	LogRedacted      bool              `toml:"log_redacted"`
	LogBodies        bool              `toml:"log_bodies"`
}

// ClientNamesConfig holds literal client names that must always be redacted.
type ClientNamesConfig struct {
	Names []string `toml:"names"`
}

// UpstreamConfig describes one upstream provider.
type UpstreamConfig struct {
	HostMatch string `toml:"host_match"`
	BaseURL   string `toml:"base_url"`
}

// DefaultConfig returns the default configuration. Values match the documented
// sample TOML.
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
	return cfg, nil
}
