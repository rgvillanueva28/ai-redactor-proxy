package detectors

import (
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestCreditCardDetector(t *testing.T) {
	d := NewCreditCard()
	if got, want := d.Category(), core.CategoryCreditCard; got != want {
		t.Fatalf("Category() = %q, want %q", got, want)
	}

	positives := []string{
		"4111111111111111",    // Visa
		"4111 1111 1111 1111", // Visa, grouped
		"5500000000000004",    // Mastercard
		"378282246310005",     // American Express
		"6011111111111117",    // Discover
	}
	for _, tc := range positives {
		t.Run("positive/"+tc, func(t *testing.T) {
			matches := d.Detect(tc)
			if len(matches) != 1 {
				t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", tc, len(matches), matches)
			}
			m := matches[0]
			if m.Category != core.CategoryCreditCard {
				t.Fatalf("category = %q, want %q", m.Category, core.CategoryCreditCard)
			}
			if m.Value != tc {
				t.Fatalf("value = %q, want %q", m.Value, tc)
			}
		})
	}

	negatives := []string{
		"1234567890123456",    // right length, bad checksum
		"1234",                // too short
		"4111-1111-1111-1112", // bad checksum, grouped
	}
	for _, tc := range negatives {
		t.Run("negative/"+tc, func(t *testing.T) {
			if matches := d.Detect(tc); len(matches) != 0 {
				t.Fatalf("Detect(%q) = %v, want no matches", tc, matches)
			}
		})
	}
}

func TestLuhnValid(t *testing.T) {
	valid := []string{
		"4111111111111111",
		"5500000000000004",
		"378282246310005",
		"6011111111111117",
		"4222222222222", // 13-digit Visa
	}
	for _, n := range valid {
		if !luhnValid(n) {
			t.Errorf("luhnValid(%q) = false, want true", n)
		}
	}

	invalid := []string{
		"1234567890123456",
		"4111111111111112",
		"",      // empty
		"4111a", // non-digit
	}
	for _, n := range invalid {
		if luhnValid(n) {
			t.Errorf("luhnValid(%q) = true, want false", n)
		}
	}
}

func TestCreditCardDetectorGroupsWithHyphens(t *testing.T) {
	d := NewCreditCard()

	text := "card 4111-1111-1111-1111 exp 12/29"
	matches := d.Detect(text)
	if len(matches) != 1 {
		t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", text, len(matches), matches)
	}
	if want := "4111-1111-1111-1111"; matches[0].Value != want {
		t.Fatalf("value = %q, want %q", matches[0].Value, want)
	}
}
