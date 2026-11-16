package detectors

import (
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestPhoneDetector(t *testing.T) {
	d := NewPhone()
	if got, want := d.Category(), core.CategoryPhone; got != want {
		t.Fatalf("Category() = %q, want %q", got, want)
	}

	positives := []string{
		"+14155552671",
		"+44 20 7946 0958",
		"(415) 555-2671",
		"415-555-2671",
		"415.555.2671",
	}
	for _, tc := range positives {
		t.Run("positive/"+tc, func(t *testing.T) {
			matches := d.Detect(tc)
			if len(matches) != 1 {
				t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", tc, len(matches), matches)
			}
			m := matches[0]
			if m.Category != core.CategoryPhone {
				t.Fatalf("category = %q, want %q", m.Category, core.CategoryPhone)
			}
			if m.Value != tc {
				t.Fatalf("value = %q, want %q", m.Value, tc)
			}
		})
	}

	negatives := []string{
		"12345",
		"555-1234",
		"+1 234 567 8", // only 8 digits
		"1-800-FLOWERS",
	}
	for _, tc := range negatives {
		t.Run("negative/"+tc, func(t *testing.T) {
			if matches := d.Detect(tc); len(matches) != 0 {
				t.Fatalf("Detect(%q) = %v, want no matches", tc, matches)
			}
		})
	}
}

func TestPhoneDetectorEmbedded(t *testing.T) {
	d := NewPhone()

	text := "call (415) 555-2671 or +14155552671 for order 12345"
	matches := d.Detect(text)
	if len(matches) != 2 {
		t.Fatalf("Detect(%q) returned %d matches, want 2 (%v)", text, len(matches), matches)
	}
	want := []string{"(415) 555-2671", "+14155552671"}
	for i, w := range want {
		if matches[i].Value != w {
			t.Fatalf("match %d = %q, want %q", i, matches[i].Value, w)
		}
	}
}
