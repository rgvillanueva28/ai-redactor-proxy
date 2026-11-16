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

// Unredact replaces all placeholders in text with their original values from
// the mapping. Placeholders are applied longest-first so that "<EMAIL_1>" is
// never matched inside "<EMAIL_10>". Unknown placeholders are left as-is.
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
