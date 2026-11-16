// Package detectors holds the concrete PII detectors used by the redaction
// registry: regex-backed recognizers for emails, phone numbers, credit cards,
// SSNs, IP addresses, filesystem paths and API keys.
package detectors

import (
	"sort"

	"github.com/local/redactor/internal/pii/core"
)

// sortAndDedupe orders matches by start offset (longest first on a tie at the
// same offset) and drops any match that overlaps one already accepted. Several
// detectors run more than one pattern over the same text, so the same span can
// be reported twice, or a shorter pattern can report a span nested inside a
// longer one.
func sortAndDedupe(matches []core.Match) []core.Match {
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
	cursor := 0 // end offset of the last accepted match
	for _, m := range matches {
		if m.Start < cursor {
			continue
		}
		out = append(out, m)
		cursor = m.End
	}
	return out
}
