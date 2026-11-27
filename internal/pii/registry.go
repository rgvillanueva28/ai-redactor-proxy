package pii

import (
	"fmt"
	"strings"
	"sync"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/pii/detectors"
)

// Registry owns the configured detectors and the redaction helpers built from
// them. The detector set can be replaced at runtime with Reload; the redaction
// helpers and the log toggles stay valid for the lifetime of the process.
type Registry struct {
	mu          sync.RWMutex
	detectors   []Detector
	redactor    *Redactor
	unredactor  *Unredactor
	logRedacted bool
	logBodies   bool
}

// categorySpec describes one detector to wire: its category, the flat enable
// toggle, the matching per-category customization block, and a constructor.
type categorySpec struct {
	category Category
	flat     bool
	cfg      config.CategoryConfig
	build    func() (Detector, error)
}

// NewRegistry builds a Registry from cfg.
//
// Detectors are appended in a fixed order (email, phone, credit card, SSN, IP,
// path, API key, credential, domain, client name, person name) so that
// placeholder numbering is deterministic for a given config and text. The
// Phase 1-2 detectors come first so that adding the newer categories did not
// renumber existing placeholders. Client names need a configured list or
// pattern to be worth wiring; person names are opt-in because the heuristic is
// noisy.
//
// Each category's [pii.<category>] block can override the flat enable toggle,
// add literal names and regex patterns, and suppress values with a denylist.
// A detector is only wrapped when its block actually adds something.
func NewRegistry(cfg *config.Config) (*Registry, error) {
	ds, err := buildDetectors(cfg)
	if err != nil {
		return nil, err
	}

	return &Registry{
		detectors:   ds,
		redactor:    NewRedactor(ds),
		unredactor:  NewUnredactor(),
		logRedacted: cfg != nil && cfg.PII.LogRedacted,
		logBodies:   cfg != nil && cfg.PII.LogBodies,
	}, nil
}

// buildDetectors builds the ordered detector list described by cfg. It is the
// single place that maps configuration to detector construction, shared by
// NewRegistry and Reload so the two cannot drift apart.
func buildDetectors(cfg *config.Config) ([]Detector, error) {
	var ds []Detector
	if cfg != nil {
		p := cfg.PII
		clientCfg := mergedClientConfig(p)

		specs := []categorySpec{
			{CategoryEmail, p.EnableEmail, p.Email, func() (Detector, error) {
				return detectors.NewEmail(), nil
			}},
			{CategoryPhone, p.EnablePhone, p.Phone, func() (Detector, error) {
				return detectors.NewPhone(), nil
			}},
			{CategoryCreditCard, p.EnableCreditCard, p.CreditCard, func() (Detector, error) {
				return detectors.NewCreditCard(), nil
			}},
			{CategorySSN, p.EnableSSN, p.SSN, func() (Detector, error) {
				return detectors.NewSSN(), nil
			}},
			{CategoryIP, p.EnableIP, p.IP, func() (Detector, error) {
				return detectors.NewIP(), nil
			}},
			{CategoryPath, p.EnablePath, p.Path, func() (Detector, error) {
				return detectors.NewPath(), nil
			}},
			{CategoryAPIKey, p.EnableAPIKey, p.APIKey, func() (Detector, error) {
				return detectors.NewAPIKey(), nil
			}},
			{CategoryCredential, p.EnableCredential, p.Credential, func() (Detector, error) {
				return detectors.NewCredential(), nil
			}},
			{CategoryDomain, p.EnableDomain, p.Domain, func() (Detector, error) {
				return detectors.NewDomain(), nil
			}},
			{CategoryClientName, p.EnableClientName, clientCfg, func() (Detector, error) {
				return detectors.NewClientName(clientCfg.Names)
			}},
			{CategoryPersonName, p.EnablePersonName, p.PersonName, func() (Detector, error) {
				return detectors.NewPersonName(), nil
			}},
		}

		for _, s := range specs {
			if !config.ResolveEnabled(s.cfg, s.flat) {
				continue
			}
			// The client-name detector needs at least one literal name or
			// custom pattern to produce anything; an empty dictionary would
			// only add a no-op pass over every request.
			if s.category == CategoryClientName && len(s.cfg.Names) == 0 && len(s.cfg.Patterns) == 0 {
				continue
			}

			base, err := s.build()
			if err != nil {
				return nil, err
			}

			det := base
			if needsCustomization(base, s.cfg) {
				det, err = wrapDetector(base, s.cfg)
				if err != nil {
					return nil, err
				}
			}
			ds = append(ds, det)
		}
	}
	return ds, nil
}

// Reload rebuilds the detector set from cfg and swaps it into the redactor. It
// also refreshes the log toggles returned by LogRedacted and LogBodies, so a
// caller that owns the request handlers can apply them without re-reading the
// file.
//
// Detectors are built completely before the swap, so a failure leaves the
// registry exactly as it was. The swap itself is safe while Redact is running:
// see Redactor.SetDetectors.
func (r *Registry) Reload(cfg *config.Config) error {
	ds, err := buildDetectors(cfg)
	if err != nil {
		return fmt.Errorf("pii: reload: %w", err)
	}

	r.redactor.SetDetectors(ds)

	r.mu.Lock()
	r.detectors = ds
	r.logRedacted = cfg != nil && cfg.PII.LogRedacted
	r.logBodies = cfg != nil && cfg.PII.LogBodies
	r.mu.Unlock()

	return nil
}

// LogRedacted reports whether redacted items should be logged, as of the last
// successful Reload.
func (r *Registry) LogRedacted() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.logRedacted
}

// LogBodies reports whether full request and response bodies should be logged,
// as of the last successful Reload.
func (r *Registry) LogBodies() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.logBodies
}

// mergedClientConfig folds the deprecated [pii.client_names] list into the
// [pii.client] block. parse() already does this for loaded files, but callers
// that build a config value directly (tests, embedders) bypass parse and still
// expect the legacy list to work.
func mergedClientConfig(p config.PIIConfig) config.CategoryConfig {
	client := p.Client
	if len(p.ClientNames.Names) == 0 {
		return client
	}

	seen := make(map[string]struct{}, len(client.Names)+len(p.ClientNames.Names))
	for _, name := range client.Names {
		seen[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	for _, name := range p.ClientNames.Names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		client.Names = append(client.Names, name)
		seen[key] = struct{}{}
	}
	return client
}

// Redactor returns the configured Redactor.
func (r *Registry) Redactor() *Redactor { return r.redactor }

// Unredactor returns the configured Unredactor.
func (r *Registry) Unredactor() *Unredactor { return r.unredactor }
