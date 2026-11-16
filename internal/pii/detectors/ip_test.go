package detectors

import (
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestIPDetector(t *testing.T) {
	d := NewIP()
	if got, want := d.Category(), core.CategoryIP; got != want {
		t.Fatalf("Category() = %q, want %q", got, want)
	}

	positives := []string{
		"192.168.1.1",
		"10.0.0.1",
		"::1",
		"2001:db8::1",
		"fe80::1ff:fe23:4567:890a",
	}
	for _, tc := range positives {
		t.Run("positive/"+tc, func(t *testing.T) {
			matches := d.Detect(tc)
			if len(matches) != 1 {
				t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", tc, len(matches), matches)
			}
			m := matches[0]
			if m.Category != core.CategoryIP {
				t.Fatalf("category = %q, want %q", m.Category, core.CategoryIP)
			}
			if m.Value != tc {
				t.Fatalf("value = %q, want %q", m.Value, tc)
			}
		})
	}

	negatives := []string{
		"999.999.999.999", // octets out of range
		"1.2.3",           // too few octets
		"dead:beef",       // hex-looking word, not an address
	}
	for _, tc := range negatives {
		t.Run("negative/"+tc, func(t *testing.T) {
			if matches := d.Detect(tc); len(matches) != 0 {
				t.Fatalf("Detect(%q) = %v, want no matches", tc, matches)
			}
		})
	}
}

func TestIPDetectorEmbedded(t *testing.T) {
	d := NewIP()

	text := "host 192.168.1.10 listens on [::1]:8080"
	matches := d.Detect(text)
	if len(matches) != 2 {
		t.Fatalf("Detect(%q) returned %d matches, want 2 (%v)", text, len(matches), matches)
	}
	want := []string{"192.168.1.10", "::1"}
	for i, w := range want {
		if matches[i].Value != w {
			t.Fatalf("match %d = %q, want %q", i, matches[i].Value, w)
		}
	}
}

func TestIPDetectorTrailingPeriod(t *testing.T) {
	d := NewIP()

	text := "see 2001:db8::1."
	matches := d.Detect(text)
	if len(matches) != 1 {
		t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", text, len(matches), matches)
	}
	if want := "2001:db8::1"; matches[0].Value != want {
		t.Fatalf("value = %q, want %q", matches[0].Value, want)
	}
}
