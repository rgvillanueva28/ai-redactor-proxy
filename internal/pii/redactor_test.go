package pii

import (
	"strconv"
	"strings"
	"testing"
)

// mockDetector is a hand-rolled detector used to test the redaction pipeline
// before the real detectors land in phases 2-3.
type mockDetector struct {
	cat     Category
	matches []Match
}

func (m mockDetector) Category() Category { return m.cat }

func (m mockDetector) Detect(text string) []Match { return m.matches }

func TestRedactSingleMatch(t *testing.T) {
	text := "contact alice@example.com now"
	start := strings.Index(text, "alice@example.com")
	d := mockDetector{
		cat:     CategoryEmail,
		matches: []Match{{Start: start, End: start + len("alice@example.com"), Category: CategoryEmail, Value: "alice@example.com"}},
	}

	got, mapping := NewRedactor([]Detector{d}).Redact(text)

	if want := "contact <EMAIL_1> now"; got != want {
		t.Fatalf("redacted = %q, want %q", got, want)
	}
	original, ok := mapping.Lookup("<EMAIL_1>")
	if !ok || original != "alice@example.com" {
		t.Fatalf("Lookup(<EMAIL_1>) = (%q, %v), want (alice@example.com, true)", original, ok)
	}
	if n := len(mapping.All()); n != 1 {
		t.Fatalf("mapping length = %d, want 1", n)
	}
}

func TestRedactMultipleMatchesSameCategory(t *testing.T) {
	text := "a@x.io b@y.io c@z.io"
	d := mockDetector{
		cat: CategoryEmail,
		matches: []Match{
			{Start: 0, End: 6, Category: CategoryEmail},
			{Start: 7, End: 13, Category: CategoryEmail},
			{Start: 14, End: 20, Category: CategoryEmail},
		},
	}

	got, mapping := NewRedactor([]Detector{d}).Redact(text)

	if want := "<EMAIL_1> <EMAIL_2> <EMAIL_3>"; got != want {
		t.Fatalf("redacted = %q, want %q", got, want)
	}
	for i, want := range []string{"a@x.io", "b@y.io", "c@z.io"} {
		ph := "<EMAIL_" + strconv.Itoa(i+1) + ">"
		if original, ok := mapping.Lookup(ph); !ok || original != want {
			t.Fatalf("Lookup(%s) = (%q, %v), want (%q, true)", ph, original, ok, want)
		}
	}
}

func TestRedactCountersAreIndependentPerCategory(t *testing.T) {
	text := "a@x.io 10.0.0.1 b@y.io"
	email := mockDetector{
		cat: CategoryEmail,
		matches: []Match{
			{Start: 0, End: 6, Category: CategoryEmail},
			{Start: 16, End: 22, Category: CategoryEmail},
		},
	}
	ip := mockDetector{
		cat:     CategoryIP,
		matches: []Match{{Start: 7, End: 15, Category: CategoryIP}},
	}

	got, mapping := NewRedactor([]Detector{email, ip}).Redact(text)

	if want := "<EMAIL_1> <IP_1> <EMAIL_2>"; got != want {
		t.Fatalf("redacted = %q, want %q", got, want)
	}
	if n := len(mapping.All()); n != 3 {
		t.Fatalf("mapping length = %d, want 3", n)
	}
}

func TestRedactOverlapLeftmostWins(t *testing.T) {
	text := "abcdefghij"
	first := mockDetector{
		cat:     CategoryEmail,
		matches: []Match{{Start: 2, End: 6, Category: CategoryEmail}},
	}
	second := mockDetector{
		cat:     CategoryIP,
		matches: []Match{{Start: 4, End: 8, Category: CategoryIP}},
	}

	got, mapping := NewRedactor([]Detector{first, second}).Redact(text)

	if want := "ab<EMAIL_1>ghij"; got != want {
		t.Fatalf("redacted = %q, want %q", got, want)
	}
	if _, ok := mapping.Lookup("<IP_1>"); ok {
		t.Fatal("overlapping later match was accepted")
	}
	if n := len(mapping.All()); n != 1 {
		t.Fatalf("mapping length = %d, want 1", n)
	}
}

func TestRedactLongerMatchWinsAtSameStart(t *testing.T) {
	text := "abcdefghij"
	short := mockDetector{
		cat:     CategoryIP,
		matches: []Match{{Start: 0, End: 4, Category: CategoryIP}},
	}
	long := mockDetector{
		cat:     CategoryEmail,
		matches: []Match{{Start: 0, End: 7, Category: CategoryEmail}},
	}

	// The shorter match comes first in the detector list, but the longer one
	// must still win.
	got, mapping := NewRedactor([]Detector{short, long}).Redact(text)

	if want := "<EMAIL_1>hij"; got != want {
		t.Fatalf("redacted = %q, want %q", got, want)
	}
	if original, ok := mapping.Lookup("<EMAIL_1>"); !ok || original != "abcdefg" {
		t.Fatalf("Lookup(<EMAIL_1>) = (%q, %v), want (abcdefg, true)", original, ok)
	}
}

func TestRedactSameStartSameLengthFirstDetectorWins(t *testing.T) {
	text := "abcdefghij"
	nameDet := mockDetector{
		cat:     CategoryPersonName,
		matches: []Match{{Start: 0, End: 5, Category: CategoryPersonName}},
	}
	emailDet := mockDetector{
		cat:     CategoryEmail,
		matches: []Match{{Start: 0, End: 5, Category: CategoryEmail}},
	}

	got, mapping := NewRedactor([]Detector{nameDet, emailDet}).Redact(text)

	if want := "<NAME_1>fghij"; got != want {
		t.Fatalf("redacted = %q, want %q", got, want)
	}
	if _, ok := mapping.Lookup("<EMAIL_1>"); ok {
		t.Fatal("later detector won an exact tie")
	}
}

func TestRedactEmptyInput(t *testing.T) {
	d := mockDetector{cat: CategoryEmail, matches: []Match{{Start: 0, End: 5, Category: CategoryEmail}}}

	got, mapping := NewRedactor([]Detector{d}).Redact("")

	if got != "" {
		t.Fatalf("redacted = %q, want empty", got)
	}
	if n := len(mapping.All()); n != 0 {
		t.Fatalf("mapping length = %d, want 0", n)
	}
}

func TestRedactNoMatchesReturnsInput(t *testing.T) {
	text := "nothing sensitive here"
	d := mockDetector{cat: CategoryEmail}

	got, mapping := NewRedactor([]Detector{d}).Redact(text)

	if got != text {
		t.Fatalf("redacted = %q, want %q", got, text)
	}
	if n := len(mapping.All()); n != 0 {
		t.Fatalf("mapping length = %d, want 0", n)
	}
}

func TestRedactNoDetectors(t *testing.T) {
	text := "untouched"

	got, mapping := NewRedactor(nil).Redact(text)

	if got != text {
		t.Fatalf("redacted = %q, want %q", got, text)
	}
	if n := len(mapping.All()); n != 0 {
		t.Fatalf("mapping length = %d, want 0", n)
	}
}
