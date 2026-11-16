package detectors

import (
	"regexp"
	"strings"

	"github.com/local/redactor/internal/pii/core"
)

// personNamePattern matches runs of two to four capitalized words, the shape
// most personal names take in English prose.
const personNamePattern = `\b[A-Z][a-z]{1,20}(?:\s+[A-Z][a-z]{1,20}){1,3}\b`

// personNameStopwords are capitalized multi-word phrases that match the
// pattern but are overwhelmingly place names, countries or other proper nouns
// rather than people. Suppression is case-insensitive.
var personNameStopwords = []string{
	"United States",
	"New York",
	"Los Angeles",
	"San Francisco",
	"North America",
	"South America",
	"Great Britain",
	"Saudi Arabia",
	"Costa Rica",
	"Czech Republic",
	"Dominican Republic",
	"El Salvador",
	"Sri Lanka",
	"Trinidad And Tobago",
	"United Kingdom",
	"United Arab",
	"Hong Kong",
	"Sierra Leone",
	"Burkina Faso",
}

// PersonName detects personal names with a capitalization heuristic. It is
// deliberately opt-in: the heuristic cannot tell "John Smith" from "The Matrix",
// so it trades precision for recall and belongs behind its own config flag.
type PersonName struct {
	re        *regexp.Regexp
	stopwords []string
}

// NewPersonName returns a PersonName detector with its pattern compiled once.
func NewPersonName() *PersonName {
	return &PersonName{
		re:        regexp.MustCompile(personNamePattern),
		stopwords: personNameStopwords,
	}
}

// Category implements core.Detector.
func (d *PersonName) Category() core.Category { return core.CategoryPersonName }

// Detect returns every likely personal name in text, in document order.
func (d *PersonName) Detect(text string) []core.Match {
	locs := d.re.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}

	matches := make([]core.Match, 0, len(locs))
	for _, loc := range locs {
		candidate := text[loc[0]:loc[1]]
		if d.isStopword(candidate) {
			continue
		}
		matches = append(matches, core.Match{
			Start:    loc[0],
			End:      loc[1],
			Category: core.CategoryPersonName,
			Value:    candidate,
		})
	}
	return matches
}

// isStopword reports whether candidate is a configured stopword or starts with
// one. The prefix check covers longer place names such as "New York City",
// where the pattern would otherwise swallow the extra capitalized word.
func (d *PersonName) isStopword(candidate string) bool {
	for _, stop := range d.stopwords {
		if strings.EqualFold(candidate, stop) {
			return true
		}
		if len(candidate) > len(stop) &&
			strings.EqualFold(candidate[:len(stop)], stop) &&
			candidate[len(stop)] == ' ' {
			return true
		}
	}
	return false
}
