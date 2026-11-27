package pii

import (
	"sort"
	"strings"
)

// Replacement modes the Redactor understands. They mirror the config package's
// ModePlaceholder and ModeCustom values; the registry resolves config modes
// into these strings.
const (
	// modePlaceholder replaces each value with a numbered <CATEGORY_n> token.
	modePlaceholder = "placeholder"
	// modeCustom replaces each value with a fixed user-supplied string.
	modeCustom = "custom"
)

// modeConfig is the per-category replacement configuration the Redactor
// applies. A missing entry, or an empty mode, means placeholder mode.
type modeConfig struct {
	mode string // modePlaceholder or modeCustom
	text string // replacement text used by modeCustom
}

// Redactor applies a fixed set of detectors to text and replaces every
// accepted match with either a tagged placeholder or a configured fixed text.
type Redactor struct {
	detectors []Detector
	modes     map[Category]modeConfig
}

// NewRedactor returns a Redactor that runs detectors in the given order and
// replaces matches according to modes. A nil or empty modes map makes every
// category use placeholder mode.
func NewRedactor(detectors []Detector, modes map[Category]modeConfig) *Redactor {
	return &Redactor{detectors: detectors, modes: modes}
}

// replacement records original in mapping for cat and returns the text that
// should stand in for it. Placeholder mode delegates counter and dedup handling
// to Mapping.Add; custom mode registers the configured text instead. Custom
// mode without replacement text falls back to placeholder mode so a malformed
// configuration cannot silently delete values.
func (r *Redactor) replacement(mapping *Mapping, cat Category, original string) string {
	if cfg, ok := r.modes[cat]; ok && cfg.mode == modeCustom && cfg.text != "" {
		return mapping.AddReplacement(cat, cfg.text, original)
	}
	return mapping.Add(cat, original)
}

// candidate is a Match plus the index of the detector that produced it, used
// as the final tie-breaker during sorting.
type candidate struct {
	match Match
	order int
}

// Redact applies detectors to text. Overlapping matches: leftmost wins; on
// tie, longer wins; on tie, first detector in list wins. It returns the
// redacted text and the populated Mapping, which the caller owns (and may
// return to the pool with PutMapping once the request is finished).
func (r *Redactor) Redact(text string) (string, *Mapping) {
	mapping := GetMapping()

	var candidates []candidate
	for i, d := range r.detectors {
		for _, mt := range d.Detect(text) {
			// Ignore out-of-range or empty matches so a buggy detector cannot
			// panic the proxy.
			if mt.Start < 0 || mt.End > len(text) || mt.Start >= mt.End {
				continue
			}
			candidates = append(candidates, candidate{match: mt, order: i})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.match.Start != b.match.Start {
			return a.match.Start < b.match.Start
		}
		la := a.match.End - a.match.Start
		lb := b.match.End - b.match.Start
		if la != lb {
			return la > lb
		}
		return a.order < b.order
	})

	var b strings.Builder
	b.Grow(len(text))

	cursor := 0
	for _, c := range candidates {
		if c.match.Start < cursor {
			// Overlaps an already-accepted match.
			continue
		}
		b.WriteString(text[cursor:c.match.Start])
		b.WriteString(r.replacement(mapping, c.match.Category, text[c.match.Start:c.match.End]))
		cursor = c.match.End
	}
	b.WriteString(text[cursor:])

	return b.String(), mapping
}
