package detectors

import (
	"regexp"

	"github.com/local/redactor/internal/pii/core"
)

// emailPattern is a pragmatic RFC 5322-ish address matcher: a local part built
// from the common atom characters, and a dotted domain ending in a 2+ letter
// top level domain.
const emailPattern = `[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`

// Email detects email addresses.
type Email struct {
	re *regexp.Regexp
}

// NewEmail returns an Email detector. The pattern is compiled once here so that
// detection is allocation-light per request.
func NewEmail() *Email {
	return &Email{re: regexp.MustCompile(emailPattern)}
}

// Category implements core.Detector.
func (d *Email) Category() core.Category { return core.CategoryEmail }

// Detect returns every email address in text, in document order.
func (d *Email) Detect(text string) []core.Match {
	locs := d.re.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}

	matches := make([]core.Match, 0, len(locs))
	for _, loc := range locs {
		matches = append(matches, core.Match{
			Start:    loc[0],
			End:      loc[1],
			Category: core.CategoryEmail,
			Value:    text[loc[0]:loc[1]],
		})
	}
	return matches
}
