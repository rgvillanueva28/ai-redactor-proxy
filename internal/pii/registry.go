package pii

import (
	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/pii/detectors"
)

// Registry owns the configured detectors and the redaction helpers built from
// them.
type Registry struct {
	detectors  []Detector
	redactor   *Redactor
	unredactor *Unredactor
}

// NewRegistry builds a Registry from cfg.
//
// Detectors are appended in a fixed order (email, phone, credit card, SSN, IP,
// path, API key, domain, client name, person name) so that placeholder
// numbering is deterministic for a given config and text. The Phase 1-2
// detectors come first so that adding the newer categories did not renumber
// existing placeholders. Client names need a configured list to be worth
// wiring; person names are opt-in because the heuristic is noisy.
func NewRegistry(cfg *config.Config) (*Registry, error) {
	var ds []Detector
	if cfg != nil {
		p := cfg.PII
		if p.EnableEmail {
			ds = append(ds, detectors.NewEmail())
		}
		if p.EnablePhone {
			ds = append(ds, detectors.NewPhone())
		}
		if p.EnableCreditCard {
			ds = append(ds, detectors.NewCreditCard())
		}
		if p.EnableSSN {
			ds = append(ds, detectors.NewSSN())
		}
		if p.EnableIP {
			ds = append(ds, detectors.NewIP())
		}
		if p.EnablePath {
			ds = append(ds, detectors.NewPath())
		}
		if p.EnableAPIKey {
			ds = append(ds, detectors.NewAPIKey())
		}
		if p.EnableDomain {
			ds = append(ds, detectors.NewDomain())
		}
		if p.EnableClientName && len(p.ClientNames.Names) > 0 {
			clientNames, err := detectors.NewClientName(p.ClientNames.Names)
			if err != nil {
				return nil, err
			}
			ds = append(ds, clientNames)
		}
		if p.EnablePersonName {
			ds = append(ds, detectors.NewPersonName())
		}
	}

	return &Registry{
		detectors:  ds,
		redactor:   NewRedactor(ds),
		unredactor: NewUnredactor(),
	}, nil
}

// Redactor returns the configured Redactor.
func (r *Registry) Redactor() *Redactor { return r.redactor }

// Unredactor returns the configured Unredactor.
func (r *Registry) Unredactor() *Unredactor { return r.unredactor }
