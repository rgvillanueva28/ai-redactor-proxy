package detectors

import (
	"regexp"

	"github.com/local/redactor/internal/pii/core"
)

// ssnPattern matches a US Social Security Number in 3-2-4 digit form with
// optional single separators, e.g. "123-45-6789", "123 45 6789" or
// "123456789".
const ssnPattern = `\b\d{3}[-\s]?\d{2}[-\s]?\d{4}\b`

// SSN detects US Social Security Numbers.
type SSN struct {
	re *regexp.Regexp
}

// NewSSN returns an SSN detector with its pattern compiled once.
func NewSSN() *SSN {
	return &SSN{re: regexp.MustCompile(ssnPattern)}
}

// Category implements core.Detector.
func (d *SSN) Category() core.Category { return core.CategorySSN }

// Detect returns every SSN-shaped match in text, in document order.
func (d *SSN) Detect(text string) []core.Match {
	locs := d.re.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}

	matches := make([]core.Match, 0, len(locs))
	for _, loc := range locs {
		matches = append(matches, core.Match{
			Start:    loc[0],
			End:      loc[1],
			Category: core.CategorySSN,
			Value:    text[loc[0]:loc[1]],
		})
	}
	return matches
}
