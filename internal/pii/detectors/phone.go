package detectors

import (
	"regexp"

	"github.com/local/redactor/internal/pii/core"
)

// Phone pattern candidates, from most specific/international to most local.
const (
	// e164Pattern matches a compact international number with no separators.
	e164Pattern = `\+[1-9]\d{9,14}`
	// intlFormattedPattern matches an international number split into grouped
	// digits, e.g. "+44 20 7946 0958".
	intlFormattedPattern = `\+[1-9]\d{0,3}(?:[\s\-]\d{1,5}){2,5}`
	// usParenPattern matches the "(415) 555-2671" style.
	usParenPattern = `\(\d{3}\)\s*\d{3}[\s\-]?\d{4}`
	// usSeparatedPattern matches 3-3-4 groupings such as "415-555-2671" or
	// "415.555.2671".
	usSeparatedPattern = `\b\d{3}[\s\-.]\d{3}[\s\-.]\d{4}\b`
)

// Phone length bounds, in digits, for an accepted match. Anything shorter is
// almost certainly a date fragment, an order number or part of a longer id.
const (
	minPhoneDigits = 10
	maxPhoneDigits = 15 // E.164 upper bound
)

// Phone detects phone numbers in E.164 and US national formats.
type Phone struct {
	patterns []*regexp.Regexp
}

// NewPhone returns a Phone detector with all patterns compiled up front.
func NewPhone() *Phone {
	return &Phone{patterns: []*regexp.Regexp{
		regexp.MustCompile(e164Pattern),
		regexp.MustCompile(intlFormattedPattern),
		regexp.MustCompile(usParenPattern),
		regexp.MustCompile(usSeparatedPattern),
	}}
}

// Category implements core.Detector.
func (d *Phone) Category() core.Category { return core.CategoryPhone }

// Detect returns every phone number in text, in document order.
func (d *Phone) Detect(text string) []core.Match {
	var matches []core.Match
	for _, re := range d.patterns {
		for _, loc := range re.FindAllStringIndex(text, -1) {
			n := countDigits(text[loc[0]:loc[1]])
			if n < minPhoneDigits || n > maxPhoneDigits {
				continue
			}
			matches = append(matches, core.Match{
				Start:    loc[0],
				End:      loc[1],
				Category: core.CategoryPhone,
				Value:    text[loc[0]:loc[1]],
			})
		}
	}
	return sortAndDedupe(matches)
}

// countDigits returns the number of ASCII digits in s.
func countDigits(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			n++
		}
	}
	return n
}
