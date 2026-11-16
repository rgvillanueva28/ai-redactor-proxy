package detectors

import (
	"regexp"
	"strings"

	"github.com/local/redactor/internal/pii/core"
)

// cardCandidatePattern finds runs of 13-19 digits that may be split into groups
// by single spaces or hyphens. Luhn validation decides whether a candidate is
// really a card number.
const cardCandidatePattern = `\b\d(?:[ \-]?\d){12,18}\b`

const (
	minCardDigits = 13
	maxCardDigits = 19
)

// CreditCard detects payment card numbers (Visa, Mastercard, Amex, Discover and
// anything else) by candidate extraction plus a Luhn checksum.
type CreditCard struct {
	re *regexp.Regexp
}

// NewCreditCard returns a CreditCard detector with its pattern compiled once.
func NewCreditCard() *CreditCard {
	return &CreditCard{re: regexp.MustCompile(cardCandidatePattern)}
}

// Category implements core.Detector.
func (d *CreditCard) Category() core.Category { return core.CategoryCreditCard }

// Detect returns every Luhn-valid card number in text, in document order.
func (d *CreditCard) Detect(text string) []core.Match {
	locs := d.re.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}

	matches := make([]core.Match, 0, len(locs))
	for _, loc := range locs {
		digits := onlyDigits(text[loc[0]:loc[1]])
		if len(digits) < minCardDigits || len(digits) > maxCardDigits {
			continue
		}
		if !luhnValid(digits) {
			continue
		}
		matches = append(matches, core.Match{
			Start:    loc[0],
			End:      loc[1],
			Category: core.CategoryCreditCard,
			Value:    text[loc[0]:loc[1]],
		})
	}
	return matches
}

// onlyDigits strips every non-digit byte from s.
func onlyDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// luhnValid reports whether number (digits only) satisfies the Luhn checksum.
func luhnValid(number string) bool {
	sum := 0
	double := false
	for i := len(number) - 1; i >= 0; i-- {
		c := number[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return len(number) > 0 && sum%10 == 0
}
