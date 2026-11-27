package pii

import (
	"sort"
	"strings"
)

// Unredactor restores original values from a Mapping in text that came back
// from the upstream model.
type Unredactor struct{}

// NewUnredactor returns a stateless Unredactor.
func NewUnredactor() *Unredactor {
	return &Unredactor{}
}

// Unredact replaces every mapping key in text with its original value. Keys are
// either generated placeholders ("<EMAIL_1>") or the fixed text a category was
// configured with in custom replacement mode ("[EMAIL]"); the unredactor treats
// both identically, so mixed-mode mappings restore transparently.
//
// Keys are applied longest-first, so "<EMAIL_1>" is never matched inside
// "<EMAIL_10>" and a custom replacement that embeds placeholder-shaped text
// (for example "[<EMAIL_1>]") is restored whole. Replacement happens in a single
// pass, so a restored original is never scanned again even if it contains text
// that looks like a mapping key. Unknown keys are left as-is.
func (u *Unredactor) Unredact(text string, m *Mapping) string {
	if text == "" || m == nil {
		return text
	}

	entries := m.All()
	if len(entries) == 0 {
		return text
	}

	placeholders := make([]string, 0, len(entries))
	for ph := range entries {
		placeholders = append(placeholders, ph)
	}
	sort.Slice(placeholders, func(i, j int) bool {
		if len(placeholders[i]) != len(placeholders[j]) {
			return len(placeholders[i]) > len(placeholders[j])
		}
		return placeholders[i] < placeholders[j]
	})

	pairs := make([]string, 0, 2*len(placeholders))
	for _, ph := range placeholders {
		pairs = append(pairs, ph, entries[ph])
	}

	return strings.NewReplacer(pairs...).Replace(text)
}
