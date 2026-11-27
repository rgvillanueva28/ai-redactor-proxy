package pii

import (
	"fmt"
	"strings"
	"testing"

	"github.com/local/redactor/internal/config"
)

// roundTripRedactor returns a Redactor with a single detector that matches the
// given literal values wherever they appear in the input.
func roundTripRedactor(cat Category, values ...string) *Redactor {
	return NewRedactor([]Detector{literalDetector{cat: cat, values: values}}, nil)
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
	}, nil)
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

// TestUnredact_10KBRoundTrip redacts the checked-in 10 KB mixed-PII fixture
// with every detector enabled, then restores it and asserts byte equality. It
// is the end-to-end contract the proxy relies on: whatever the detectors take
// out must come back exactly as it went in.
func TestUnredact_10KBRoundTrip(t *testing.T) {
	original := loadMixedPII10KB(t)
	reg := mixedPIIRegistry(t)

	redacted, mapping := reg.Redactor().Redact(original)
	defer PutMapping(mapping)

	if redacted == original {
		t.Fatal("redaction made no change to the mixed-PII fixture")
	}
	if n := len(mapping.All()); n < 70 {
		t.Fatalf("mapping length = %d, want at least 70", n)
	}

	// Spot-check a value from several categories so the round trip is not the
	// only evidence that redaction actually happened.
	for _, secret := range []string{
		"alice.wong@northwind-trading.com",
		"4111 1111 1111 1111",
		"/home/alice/projects/ledger/main.go",
		"sk-proj-8fJ2kLm9QpR4tUvW6xYzA1bC3dE5fG7hI9jK",
		"2001:db8::1",
	} {
		if strings.Contains(redacted, secret) {
			t.Errorf("redacted fixture still contains %q", secret)
		}
	}

	if restored := NewUnredactor().Unredact(redacted, mapping); restored != original {
		t.Fatalf("10 KB round trip mismatch: got %d bytes, want %d", len(restored), len(original))
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

// -----------------------------------------------------------------------------
// Replacement modes: placeholder (default) and custom text
// -----------------------------------------------------------------------------

// TestUnredactReplacementModes drives the full Redact -> Unredact path for all
// placeholder, all custom, and mixed mode configurations. Custom mode collapses
// every value it replaces onto one mapping entry, so the cases that must
// round-trip exactly use at most one value per custom category; the collapse
// itself is covered by TestUnredactCustomCollapsesDistinctOriginals.
func TestUnredactReplacementModes(t *testing.T) {
	det := func(cat Category, values ...string) Detector {
		return literalDetector{cat: cat, values: values}
	}
	unredactor := NewUnredactor()

	cases := []struct {
		name         string
		detectors    []Detector
		modes        map[Category]modeConfig
		input        string
		wantRedacted string
		wantRestored string
	}{
		{
			name: "all placeholder with nil modes",
			detectors: []Detector{
				det(CategoryEmail, "alice@example.com", "bob@example.com"),
				det(CategoryIP, "10.0.0.1"),
			},
			input:        "mail alice@example.com or bob@example.com from 10.0.0.1",
			wantRedacted: "mail <EMAIL_1> or <EMAIL_2> from <IP_1>",
			wantRestored: "mail alice@example.com or bob@example.com from 10.0.0.1",
		},
		{
			name: "all placeholder with explicit modes",
			detectors: []Detector{
				det(CategoryEmail, "alice@example.com"),
				det(CategoryPath, `C:\Users\alice\notes.txt`),
			},
			modes: map[Category]modeConfig{
				CategoryEmail: {mode: modePlaceholder},
				CategoryPath:  {mode: modePlaceholder, text: "[ignored]"},
			},
			input:        `alice@example.com saved to C:\Users\alice\notes.txt`,
			wantRedacted: `<EMAIL_1> saved to <PATH_1>`,
			wantRestored: `alice@example.com saved to C:\Users\alice\notes.txt`,
		},
		{
			name: "all custom",
			detectors: []Detector{
				det(CategoryEmail, "alice@example.com"),
				det(CategoryIP, "10.0.0.1"),
				det(CategoryPath, `C:\Users\alice\notes.txt`),
			},
			modes: map[Category]modeConfig{
				CategoryEmail: {mode: modeCustom, text: "[EMAIL]"},
				CategoryIP:    {mode: modeCustom, text: "[IP]"},
				CategoryPath:  {mode: modeCustom, text: "[PATH]"},
			},
			input:        `mail alice@example.com from 10.0.0.1 file C:\Users\alice\notes.txt`,
			wantRedacted: `mail [EMAIL] from [IP] file [PATH]`,
			wantRestored: `mail alice@example.com from 10.0.0.1 file C:\Users\alice\notes.txt`,
		},
		{
			name: "mixed custom and placeholder modes",
			detectors: []Detector{
				det(CategoryEmail, "alice@example.com"),
				det(CategoryIP, "10.0.0.1", "10.0.0.2"),
				det(CategoryPhone, "+1 555 010 9999"),
			},
			modes: map[Category]modeConfig{
				CategoryEmail: {mode: modeCustom, text: "[EMAIL]"},
				CategoryPhone: {mode: modeCustom, text: "[PHONE]"},
			},
			input:        "mail alice@example.com from 10.0.0.1 or 10.0.0.2 call +1 555 010 9999",
			wantRedacted: "mail [EMAIL] from <IP_1> or <IP_2> call [PHONE]",
			wantRestored: "mail alice@example.com from 10.0.0.1 or 10.0.0.2 call +1 555 010 9999",
		},
		{
			// The custom text embeds the placeholder another category
			// generates. Longest-first replacement must restore the wrapper
			// whole instead of substituting the placeholder inside it.
			name: "custom text wraps a placeholder",
			detectors: []Detector{
				det(CategoryEmail, "alice@example.com"),
				det(CategoryIP, "10.0.0.1"),
			},
			modes: map[Category]modeConfig{
				CategoryEmail: {mode: modeCustom, text: "[<IP_1>]"},
			},
			input:        "alice@example.com 10.0.0.1",
			wantRedacted: "[<IP_1>] <IP_1>",
			wantRestored: "alice@example.com 10.0.0.1",
		},
		{
			// Custom text may be indistinguishable from the placeholder its
			// own category would have generated; no counter is consumed, so
			// the mapping has exactly one entry for it.
			name: "custom text shaped like a placeholder",
			detectors: []Detector{
				det(CategoryEmail, "alice@example.com"),
				det(CategoryIP, "10.0.0.1"),
			},
			modes: map[Category]modeConfig{
				CategoryEmail: {mode: modeCustom, text: "<EMAIL_1>"},
			},
			input:        "alice@example.com 10.0.0.1",
			wantRedacted: "<EMAIL_1> <IP_1>",
			wantRestored: "alice@example.com 10.0.0.1",
		},
		{
			// One custom text is a prefix of another, so longest-first
			// ordering decides which entry "[REDACTED!]" restores from.
			name: "one custom text is a prefix of another",
			detectors: []Detector{
				det(CategoryEmail, "alice@example.com"),
				det(CategoryPhone, "+1 555 010 9999"),
			},
			modes: map[Category]modeConfig{
				CategoryEmail: {mode: modeCustom, text: "REDACTED"},
				CategoryPhone: {mode: modeCustom, text: "REDACTED!"},
			},
			input:        "alice@example.com +1 555 010 9999",
			wantRedacted: "REDACTED REDACTED!",
			wantRestored: "alice@example.com +1 555 010 9999",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redacted, mapping := NewRedactor(tc.detectors, tc.modes).Redact(tc.input)
			defer PutMapping(mapping)

			if redacted != tc.wantRedacted {
				t.Fatalf("Redact = %q, want %q", redacted, tc.wantRedacted)
			}
			if restored := unredactor.Unredact(redacted, mapping); restored != tc.wantRestored {
				t.Fatalf("Unredact = %q, want %q", restored, tc.wantRestored)
			}
		})
	}
}

// TestUnredactCustomMappingEntries exercises the unredactor against mappings
// built without the Redactor, so each edge case pins the exact key set. Custom
// replacement text is arbitrary, so it may embed, prefix, or equal the shape of
// a generated placeholder.
func TestUnredactCustomMappingEntries(t *testing.T) {
	cases := []struct {
		name  string
		build func(*Mapping)
		text  string
		want  string
	}{
		{
			name: "placeholder keys only",
			build: func(m *Mapping) {
				m.Add(CategoryEmail, "alice@example.com")
			},
			text: "<EMAIL_1>",
			want: "alice@example.com",
		},
		{
			name: "custom keys only",
			build: func(m *Mapping) {
				m.AddReplacement(CategoryEmail, "[EMAIL]", "alice@example.com")
			},
			text: "x [EMAIL] y",
			want: "x alice@example.com y",
		},
		{
			name: "mixed placeholder and custom keys",
			build: func(m *Mapping) {
				m.Add(CategoryEmail, "alice@example.com")
				m.AddReplacement(CategoryIP, "[IP]", "10.0.0.1")
			},
			text: "<EMAIL_1> [IP]",
			want: "alice@example.com 10.0.0.1",
		},
		{
			name: "custom text wraps a placeholder key",
			build: func(m *Mapping) {
				m.Add(CategoryIP, "10.0.0.1")
				m.AddReplacement(CategoryEmail, "[<IP_1>]", "alice@example.com")
			},
			text: "[<IP_1>] <IP_1>",
			want: "alice@example.com 10.0.0.1",
		},
		{
			name: "custom text embeds a placeholder mid-string",
			build: func(m *Mapping) {
				m.Add(CategoryIP, "10.0.0.1")
				m.AddReplacement(CategoryEmail, "at <IP_1>", "alice@example.com")
			},
			text: "at <IP_1> | <IP_1>",
			want: "alice@example.com | 10.0.0.1",
		},
		{
			name: "custom text is a prefix of another custom text",
			build: func(m *Mapping) {
				m.AddReplacement(CategoryEmail, "REDACTED", "alice@example.com")
				m.AddReplacement(CategoryPhone, "REDACTED!", "+1 555 010 9999")
			},
			text: "REDACTED REDACTED!",
			want: "alice@example.com +1 555 010 9999",
		},
		{
			name: "repeated custom text restores every occurrence",
			build: func(m *Mapping) {
				m.AddReplacement(CategoryEmail, "[EMAIL]", "alice@example.com")
			},
			text: "[EMAIL] and [EMAIL]",
			want: "alice@example.com and alice@example.com",
		},
		{
			name: "unknown custom text is left as is",
			build: func(m *Mapping) {
				m.AddReplacement(CategoryEmail, "[EMAIL]", "alice@example.com")
			},
			text: "[EMAIL] [PHONE]",
			want: "alice@example.com [PHONE]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMapping()
			tc.build(m)
			defer PutMapping(m)

			if got := NewUnredactor().Unredact(tc.text, m); got != tc.want {
				t.Fatalf("Unredact = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnredactCustomCollapsesDistinctOriginals documents the one lossy case in
// custom mode: every original replaced by the same text shares a single mapping
// entry, which retains the most recently registered original. The unredactor is
// not at fault; the mapping cannot distinguish the values the redactor collapsed.
func TestUnredactCustomCollapsesDistinctOriginals(t *testing.T) {
	unredactor := NewUnredactor()

	t.Run("distinct values in one category", func(t *testing.T) {
		redactor := NewRedactor([]Detector{
			literalDetector{cat: CategoryEmail, values: []string{"a@x.io", "b@y.io"}},
		}, map[Category]modeConfig{
			CategoryEmail: {mode: modeCustom, text: "[EMAIL]"},
		})

		redacted, mapping := redactor.Redact("a@x.io and b@y.io")
		defer PutMapping(mapping)

		if want := "[EMAIL] and [EMAIL]"; redacted != want {
			t.Fatalf("Redact = %q, want %q", redacted, want)
		}
		if n := len(mapping.All()); n != 1 {
			t.Fatalf("mapping length = %d, want 1", n)
		}
		if got := unredactor.Unredact(redacted, mapping); got != "b@y.io and b@y.io" {
			t.Fatalf("Unredact = %q, want %q", got, "b@y.io and b@y.io")
		}
	})

	t.Run("same text across categories", func(t *testing.T) {
		redactor := NewRedactor([]Detector{
			literalDetector{cat: CategoryEmail, values: []string{"a@x.io"}},
			literalDetector{cat: CategoryIP, values: []string{"10.0.0.1"}},
		}, map[Category]modeConfig{
			CategoryEmail: {mode: modeCustom, text: "[REDACTED]"},
			CategoryIP:    {mode: modeCustom, text: "[REDACTED]"},
		})

		redacted, mapping := redactor.Redact("a@x.io 10.0.0.1")
		defer PutMapping(mapping)

		if want := "[REDACTED] [REDACTED]"; redacted != want {
			t.Fatalf("Redact = %q, want %q", redacted, want)
		}
		if got := unredactor.Unredact(redacted, mapping); got != "10.0.0.1 10.0.0.1" {
			t.Fatalf("Unredact = %q, want %q", got, "10.0.0.1 10.0.0.1")
		}
	})
}

// TestUnredactRestoredValueNotRescanned guards the single-pass contract: an
// original value that itself contains placeholder-shaped text must come back
// verbatim, not be expanded a second time.
func TestUnredactRestoredValueNotRescanned(t *testing.T) {
	m := NewMapping()
	m.Add(CategoryEmail, "alice@example.com") // <EMAIL_1>
	m.Add(CategoryEmail, "bob<EMAIL_1>@x.io") // <EMAIL_2>

	got := NewUnredactor().Unredact("<EMAIL_2> and <EMAIL_1>", m)
	want := "bob<EMAIL_1>@x.io and alice@example.com"

	if got != want {
		t.Fatalf("Unredact = %q, want %q", got, want)
	}
}

// TestUnredactRegistryMixedModesRoundTrip covers the configuration path: a
// custom-mode category and a placeholder-mode category resolved from config must
// both restore through the registry's unredactor.
func TestUnredactRegistryMixedModesRoundTrip(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Email.Mode = config.ModeCustom
	cfg.PII.Email.Text = "[EMAIL]"
	cfg.PII.IP.Mode = config.ModePlaceholder

	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	original := "mail alice@example.com from 10.0.0.1"
	redacted, mapping := reg.Redactor().Redact(original)
	defer PutMapping(mapping)

	if want := "mail [EMAIL] from <IP_1>"; redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
	if restored := reg.Unredactor().Unredact(redacted, mapping); restored != original {
		t.Fatalf("Unredact = %q, want %q", restored, original)
	}
}
