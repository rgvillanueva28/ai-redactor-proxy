package pii

import (
	"fmt"
	"strings"
	"testing"
)

// roundTripRedactor returns a Redactor with a single detector that matches the
// given literal values wherever they appear in the input.
func roundTripRedactor(cat Category, values ...string) *Redactor {
	return NewRedactor([]Detector{literalDetector{cat: cat, values: values}})
}

// literalDetector finds each value in the text, left to right.
type literalDetector struct {
	cat    Category
	values []string
}

func (d literalDetector) Category() Category { return d.cat }

func (d literalDetector) Detect(text string) []Match {
	var out []Match
	for _, value := range d.values {
		offset := 0
		for {
			idx := strings.Index(text[offset:], value)
			if idx < 0 {
				break
			}
			start := offset + idx
			out = append(out, Match{Start: start, End: start + len(value), Category: d.cat, Value: value})
			offset = start + len(value)
		}
	}
	return out
}

func TestRoundTripRedactUnredact(t *testing.T) {
	original := "mail alice@example.com or bob@example.com from 10.0.0.1 today"
	redactor := NewRedactor([]Detector{
		literalDetector{cat: CategoryEmail, values: []string{"alice@example.com", "bob@example.com"}},
		literalDetector{cat: CategoryIP, values: []string{"10.0.0.1"}},
	})
	unredactor := NewUnredactor()

	redacted, mapping := redactor.Redact(original)
	if redacted == original {
		t.Fatal("redaction made no change")
	}
	if strings.Contains(redacted, "alice@example.com") {
		t.Fatalf("redacted text still contains original email: %q", redacted)
	}

	restored := unredactor.Unredact(redacted, mapping)
	if restored != original {
		t.Fatalf("round trip = %q, want %q", restored, original)
	}
}

func TestUnredactMultiplePlaceholders(t *testing.T) {
	m := NewMapping()
	phEmail := m.Add(CategoryEmail, "alice@example.com")
	phIP := m.Add(CategoryIP, "10.0.0.1")
	phPath := m.Add(CategoryPath, `C:\Users\alice\notes.txt`)

	text := fmt.Sprintf("a=%s b=%s c=%s", phEmail, phIP, phPath)
	want := `a=alice@example.com b=10.0.0.1 c=C:\Users\alice\notes.txt`

	if got := NewUnredactor().Unredact(text, m); got != want {
		t.Fatalf("unredacted = %q, want %q", got, want)
	}
}

func TestUnredactUnknownPlaceholderLeftAsIs(t *testing.T) {
	m := NewMapping()
	m.Add(CategoryEmail, "alice@example.com")

	text := "known <EMAIL_1> unknown <IP_9> and <BOGUS>"
	want := "known alice@example.com unknown <IP_9> and <BOGUS>"

	if got := NewUnredactor().Unredact(text, m); got != want {
		t.Fatalf("unredacted = %q, want %q", got, want)
	}
}

func TestUnredactEmptyText(t *testing.T) {
	m := NewMapping()
	m.Add(CategoryEmail, "alice@example.com")

	if got := NewUnredactor().Unredact("", m); got != "" {
		t.Fatalf("unredacted = %q, want empty", got)
	}
}

func TestUnredactNilMapping(t *testing.T) {
	text := "nothing <EMAIL_1> here"

	if got := NewUnredactor().Unredact(text, nil); got != text {
		t.Fatalf("unredacted = %q, want %q", got, text)
	}
}

func TestUnredactEmptyMapping(t *testing.T) {
	text := "nothing <EMAIL_1> here"

	if got := NewUnredactor().Unredact(text, NewMapping()); got != text {
		t.Fatalf("unredacted = %q, want %q", got, text)
	}
}

// TestUnredactPrefersLongerPlaceholder ensures <EMAIL_10> is not partially
// matched as <EMAIL_1> + "0".
func TestUnredactPrefersLongerPlaceholder(t *testing.T) {
	m := NewMapping()
	m.Add(CategoryEmail, "a@example.com") // <EMAIL_1>
	m.Add(CategoryEmail, "b@example.com") // <EMAIL_2>
	for i := 3; i <= 10; i++ {
		m.Add(CategoryEmail, fmt.Sprintf("user%d@example.com", i)) // ... <EMAIL_10>
	}

	got := NewUnredactor().Unredact("<EMAIL_10> <EMAIL_1>", m)
	want := "user10@example.com a@example.com"

	if got != want {
		t.Fatalf("unredacted = %q, want %q", got, want)
	}
}

func TestUnredactLargeMapping(t *testing.T) {
	const n = 120

	m := NewMapping()
	var text strings.Builder
	var want strings.Builder
	for i := 1; i <= n; i++ {
		original := fmt.Sprintf("user%d@example.com", i)
		text.WriteString(m.Add(CategoryEmail, original))
		if i != n {
			text.WriteString(" ")
		}
		want.WriteString(original)
		if i != n {
			want.WriteString(" ")
		}
	}

	got := NewUnredactor().Unredact(text.String(), m)
	if got != want.String() {
		t.Fatalf("large round trip mismatch:\n got = %q\nwant = %q", got, want.String())
	}
}

func TestRedactUnredactLargeMapping(t *testing.T) {
	// Build an input with n distinct emails and verify the full pipeline
	// redacts and restores it.
	const n = 100

	values := make([]string, 0, n)
	var input strings.Builder
	for i := 0; i < n; i++ {
		value := fmt.Sprintf("user%d@example.com", i)
		values = append(values, value)
		input.WriteString(value)
		input.WriteString(" ")
	}
	original := strings.TrimSuffix(input.String(), " ")

	redactor := roundTripRedactor(CategoryEmail, values...)
	redacted, mapping := redactor.Redact(original)
	if strings.Contains(redacted, "user0@example.com") {
		t.Fatalf("redacted text still contains original values: %q", redacted)
	}
	if got := len(mapping.All()); got != n {
		t.Fatalf("mapping length = %d, want %d", got, n)
	}

	if got := NewUnredactor().Unredact(redacted, mapping); got != original {
		t.Fatalf("round trip mismatch for %d entries", n)
	}
}
