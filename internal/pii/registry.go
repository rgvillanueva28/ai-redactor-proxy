package pii

import "github.com/local/redactor/internal/config"

// Registry owns the configured detectors and the redaction helpers built from
// them.
type Registry struct {
	detectors  []Detector
	redactor   *Redactor
	unredactor *Unredactor
}

// NewRegistry builds a Registry from cfg.
//
// Phase 1: no detectors are wired yet. Real detector instantiation is added in
// Phases 2-3.
func NewRegistry(cfg *config.Config) (*Registry, error) {
	return &Registry{
		detectors:  []Detector{},
		redactor:   NewRedactor([]Detector{}),
		unredactor: NewUnredactor(),
	}, nil
}

// Redactor returns the configured Redactor.
func (r *Registry) Redactor() *Redactor { return r.redactor }

// Unredactor returns the configured Unredactor.
func (r *Registry) Unredactor() *Unredactor { return r.unredactor }
