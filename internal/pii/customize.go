package pii

import (
	"regexp"
	"sort"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/pii/detectors"
)

// customizingDetector wraps a base detector with additional literal names,
// additional regex patterns, and a denylist that suppresses matches. Every
// match it reports keeps the wrapped detector's category, so a custom pattern
// on the phone category is still redacted as "<PHONE_n>".
type customizingDetector struct {
	base     Detector
	names    *detectors.ClientName // Aho-Corasick automaton for literal names
	patterns []*regexp.Regexp      // compiled custom patterns
	deny     map[string]struct{}   // denylist lookup
}

// Category implements Detector by delegating to the base detector.
func (d *customizingDetector) Category() Category { return d.base.Category() }

// Detect returns the base detector's matches plus every custom name and
// pattern hit, with denylisted values removed.
func (d *customizingDetector) Detect(text string) []Match {
	matches := d.base.Detect(text)
	category := d.base.Category()

	if d.names != nil {
		for _, m := range d.names.Detect(text) {
			m.Category = category
			matches = append(matches, m)
		}
	}

	for _, re := range d.patterns {
		for _, loc := range re.FindAllStringIndex(text, -1) {
			if loc[0] >= loc[1] {
				continue
			}
			matches = append(matches, Match{
				Start:    loc[0],
				End:      loc[1],
				Category: category,
				Value:    text[loc[0]:loc[1]],
			})
		}
	}

	if len(d.deny) > 0 {
		filtered := matches[:0]
		for _, m := range matches {
			if _, denied := d.deny[m.Value]; !denied {
				filtered = append(filtered, m)
			}
		}
		matches = filtered
	}

	return sortAndDedupe(matches)
}

// wrapDetector builds a customizingDetector around base.
//
// The client-name detector already consumes its names in NewClientName, so
// those names are not added a second time here; only patterns and the denylist
// are applied on top of it. Customisation for every other category adds the
// configured names to the base detector's matches.
func wrapDetector(base Detector, cfg config.CategoryConfig) (Detector, error) {
	cd := &customizingDetector{base: base}

	if len(cfg.Names) > 0 && base.Category() != CategoryClientName {
		names, err := detectors.NewClientName(cfg.Names)
		if err != nil {
			return nil, err
		}
		cd.names = names
	}

	if len(cfg.Patterns) > 0 {
		for _, p := range cfg.Patterns {
			re, err := regexp.Compile(p)
			if err != nil {
				// A malformed custom pattern must not take the proxy down;
				// skip it and keep the category's other matches working.
				continue
			}
			cd.patterns = append(cd.patterns, re)
		}
	}

	if len(cfg.Denylist) > 0 {
		cd.deny = make(map[string]struct{}, len(cfg.Denylist))
		for _, value := range cfg.Denylist {
			cd.deny[value] = struct{}{}
		}
	}

	return cd, nil
}

// needsCustomization reports whether cfg adds anything the base detector does
// not already handle. Names alone are already consumed by the client-name
// detector's constructor, so that case needs no wrapper.
func needsCustomization(base Detector, cfg config.CategoryConfig) bool {
	if len(cfg.Patterns) > 0 || len(cfg.Denylist) > 0 {
		return true
	}
	return len(cfg.Names) > 0 && base.Category() != CategoryClientName
}

// sortAndDedupe orders matches by start offset (longest first on a tie) and
// drops overlaps, mirroring the detectors package helper. Wrapping a detector
// can merge hits from several sources, so the combined list needs the same
// normalisation a single detector applies internally.
func sortAndDedupe(matches []Match) []Match {
	if len(matches) < 2 {
		return matches
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Start != matches[j].Start {
			return matches[i].Start < matches[j].Start
		}
		return matches[i].End > matches[j].End
	})

	out := matches[:0]
	cursor := 0
	for _, m := range matches {
		if m.Start < cursor {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Start == m.Start && out[n-1].End == m.End {
			// Same span reported by more than one source (for example a
			// custom pattern that duplicates the base detector's hit).
			continue
		}
		out = append(out, m)
		cursor = m.End
	}
	return out
}
