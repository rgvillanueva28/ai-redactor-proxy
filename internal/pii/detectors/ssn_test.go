package detectors

import (
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestSSNDetector(t *testing.T) {
	d := NewSSN()
	if got, want := d.Category(), core.CategorySSN; got != want {
		t.Fatalf("Category() = %q, want %q", got, want)
	}

	positives := []string{
		"123-45-6789",
		"123 45 6789",
		"123456789",
		"987-65-4321",
		"078-05-1120",
	}
	for _, tc := range positives {
		t.Run("positive/"+tc, func(t *testing.T) {
			matches := d.Detect(tc)
			if len(matches) != 1 {
				t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", tc, len(matches), matches)
			}
			m := matches[0]
			if m.Category != core.CategorySSN {
				t.Fatalf("category = %q, want %q", m.Category, core.CategorySSN)
			}
			if m.Value != tc {
				t.Fatalf("value = %q, want %q", m.Value, tc)
			}
		})
	}

	negatives := []string{
		"12-345-6789",
		"12345",
		"1234567890", // 10 bare digits: boundary check rejects it
	}
	for _, tc := range negatives {
		t.Run("negative/"+tc, func(t *testing.T) {
			if matches := d.Detect(tc); len(matches) != 0 {
				t.Fatalf("Detect(%q) = %v, want no matches", tc, matches)
			}
		})
	}
}

func TestSSNDetectorEmbedded(t *testing.T) {
	d := NewSSN()

	text := "ssn: 123-45-6789 (verified)"
	matches := d.Detect(text)
	if len(matches) != 1 {
		t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", text, len(matches), matches)
	}
	if want := "123-45-6789"; matches[0].Value != want {
		t.Fatalf("value = %q, want %q", matches[0].Value, want)
	}
}
