package pii

import (
	"testing"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/pii/detectors"
)

func boolPtr(v bool) *bool { return &v }

// findDetector returns the first wired detector for category, or nil.
func findDetector(r *Registry, category Category) Detector {
	for _, d := range r.detectors {
		if d.Category() == category {
			return d
		}
	}
	return nil
}

func TestCustomNamesAddMatchesToCategory(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Email.Names = []string{"Team Blob"}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "Team Blob emailed foo@bar.com"
	got, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "<EMAIL_1> emailed <EMAIL_2>"
	if got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}

func TestCustomPatternsAddMatchesToCategory(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Phone.Patterns = []string{`\bINTERNAL-\d{4}\b`}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "call INTERNAL-1234 then +14155552671"
	got, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "call <PHONE_1> then <PHONE_2>"
	if got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}

func TestDenylistSuppressesMatches(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Domain.Denylist = []string{"example.com"}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "visit example.com and acme.io"
	got, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "visit example.com and <DOMAIN_1>"
	if got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}

func TestCustomizingDetectorKeepsBaseCategory(t *testing.T) {
	wrapped, err := wrapDetector(detectors.NewPhone(), config.CategoryConfig{
		Names:    []string{"Acme"},
		Patterns: []string{`INTERNAL-\d+`},
	})
	if err != nil {
		t.Fatalf("wrapDetector: %v", err)
	}

	if got := wrapped.Category(); got != CategoryPhone {
		t.Fatalf("Category = %q, want %q", got, CategoryPhone)
	}

	matches := wrapped.Detect("Acme rang INTERNAL-42 from +14155552671")
	if len(matches) != 3 {
		t.Fatalf("match count = %d, want 3: %#v", len(matches), matches)
	}
	for _, m := range matches {
		if m.Category != CategoryPhone {
			t.Fatalf("match %q category = %q, want %q", m.Value, m.Category, CategoryPhone)
		}
	}
}

func TestDenylistAppliesToCustomMatches(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Phone.Patterns = []string{`\bINTERNAL-\d{4}\b`}
	cfg.PII.Phone.Denylist = []string{"INTERNAL-0000"}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "call INTERNAL-0000 or INTERNAL-1234"
	got, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "call INTERNAL-0000 or <PHONE_1>"
	if got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}

func TestClientNamesOnlyAreNotDuplicated(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Client.Names = []string{"Acme Corp"}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	d := findDetector(r, CategoryClientName)
	if d == nil {
		t.Fatal("client-name detector not wired")
	}
	if matches := d.Detect("Acme Corp met Acme Corp"); len(matches) != 2 {
		t.Fatalf("match count = %d, want 2: %#v", len(matches), matches)
	}
}

func TestClientPatternsWorkWithoutNames(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Client.Patterns = []string{`\bProject-\d{3}\b`}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	d := findDetector(r, CategoryClientName)
	if d == nil {
		t.Fatal("client-name detector not wired for patterns without names")
	}

	got, mapping := r.Redactor().Redact("open Project-123 now")
	defer PutMapping(mapping)
	if want := "open <CLIENT_1> now"; got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}

func TestPerCategoryEnabledOverrideWins(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Email.Names = []string{"Team Blob"}
	cfg.PII.Email.Enabled = boolPtr(false)

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if d := findDetector(r, CategoryEmail); d != nil {
		t.Fatal("email detector wired despite [pii.email].enabled = false")
	}

	text := "Team Blob emailed foo@bar.com"
	got, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)
	if got != text {
		t.Fatalf("Redact = %q, want unchanged %q", got, text)
	}
}

func TestInvalidCustomPatternIsIgnored(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Phone.Patterns = []string{"[unclosed", `\bINTERNAL-\d{4}\b`}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	got, mapping := r.Redactor().Redact("call INTERNAL-1234")
	defer PutMapping(mapping)
	if want := "call <PHONE_1>"; got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}
