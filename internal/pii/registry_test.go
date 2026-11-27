package pii

import (
	"testing"

	"github.com/local/redactor/internal/config"
)

func TestNewRegistryWiresEnabledDetectorsInStableOrder(t *testing.T) {
	cfg := config.DefaultConfig()

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	want := []Category{
		CategoryEmail,
		CategoryPhone,
		CategoryCreditCard,
		CategorySSN,
		CategoryIP,
		CategoryPath,
		CategoryAPIKey,
		CategoryCredential,
		CategoryDomain,
	}
	if len(r.detectors) != len(want) {
		t.Fatalf("detector count = %d, want %d", len(r.detectors), len(want))
	}
	for i, w := range want {
		if got := r.detectors[i].Category(); got != w {
			t.Fatalf("detector %d category = %q, want %q", i, got, w)
		}
	}
}

func TestNewRegistryOrderUnchangedWithCustomization(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Email.Names = []string{"Team Blob"}
	cfg.PII.Phone.Patterns = []string{`\bINTERNAL-\d{4}\b`}
	cfg.PII.Domain.Denylist = []string{"example.com"}
	cfg.PII.Client.Names = []string{"Acme Corp"}
	cfg.PII.EnablePersonName = true

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	want := []Category{
		CategoryEmail,
		CategoryPhone,
		CategoryCreditCard,
		CategorySSN,
		CategoryIP,
		CategoryPath,
		CategoryAPIKey,
		CategoryCredential,
		CategoryDomain,
		CategoryClientName,
		CategoryPersonName,
	}
	if len(r.detectors) != len(want) {
		t.Fatalf("detector count = %d, want %d", len(r.detectors), len(want))
	}
	for i, w := range want {
		if got := r.detectors[i].Category(); got != w {
			t.Fatalf("detector %d category = %q, want %q", i, got, w)
		}
	}
}

func TestNewRegistryCustomizationEndToEnd(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Email.Names = []string{"Team Blob"}
	cfg.PII.Phone.Patterns = []string{`\bINTERNAL-\d{4}\b`}
	cfg.PII.Domain.Denylist = []string{"example.com"}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "Team Blob rang INTERNAL-1234 from acme.io, not example.com"
	redacted, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "<EMAIL_1> rang <PHONE_1> from <DOMAIN_1>, not example.com"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
	if restored := r.Unredactor().Unredact(redacted, mapping); restored != text {
		t.Fatalf("round trip = %q, want %q", restored, text)
	}
}

func TestNewRegistryClientNamesFromCategoryBlock(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Client.Names = []string{"Acme Corp"}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	got, mapping := r.Redactor().Redact("Acme Corp shipped it")
	defer PutMapping(mapping)
	if want := "<CLIENT_1> shipped it"; got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}

func TestNewRegistryWiresAdvancedDetectorsAfterAPIKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.EnableClientName = true
	cfg.PII.ClientNames.Names = []string{"Acme Corp", "Globex"}
	cfg.PII.EnablePersonName = true

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	want := []Category{
		CategoryEmail,
		CategoryPhone,
		CategoryCreditCard,
		CategorySSN,
		CategoryIP,
		CategoryPath,
		CategoryAPIKey,
		CategoryCredential,
		CategoryDomain,
		CategoryClientName,
		CategoryPersonName,
	}
	if len(r.detectors) != len(want) {
		t.Fatalf("detector count = %d, want %d", len(r.detectors), len(want))
	}
	for i, w := range want {
		if got := r.detectors[i].Category(); got != w {
			t.Fatalf("detector %d category = %q, want %q", i, got, w)
		}
	}
}

func TestNewRegistryClientNameNeedsConfiguredNames(t *testing.T) {
	for _, names := range [][]string{nil, {}} {
		cfg := config.DefaultConfig()
		cfg.PII.EnableClientName = true
		cfg.PII.ClientNames.Names = names

		r, err := NewRegistry(cfg)
		if err != nil {
			t.Fatalf("NewRegistry: %v", err)
		}
		for _, d := range r.detectors {
			if d.Category() == CategoryClientName {
				t.Fatalf("client-name detector wired with names %v, want none", names)
			}
		}
	}
}

func TestNewRegistryPersonNameDisabledByDefault(t *testing.T) {
	r, err := NewRegistry(config.DefaultConfig())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	for _, d := range r.detectors {
		if d.Category() == CategoryPersonName {
			t.Fatal("person-name detector must not be wired by default")
		}
	}

	text := "I met John Smith yesterday"
	if got, _ := r.Redactor().Redact(text); got != text {
		t.Fatalf("Redact = %q, want unchanged %q", got, text)
	}
}

func TestNewRegistryAdvancedDetectorRedaction(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.EnableClientName = true
	cfg.PII.ClientNames.Names = []string{"Acme Corp"}
	cfg.PII.EnablePersonName = true

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "Acme Corp uses api.acme.com, John Smith mailed foo@bar.com"
	redacted, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "<CLIENT_1> uses api.<DOMAIN_1>, <NAME_1> mailed <EMAIL_1>"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
	if restored := r.Unredactor().Unredact(redacted, mapping); restored != text {
		t.Fatalf("round trip = %q, want %q", restored, text)
	}
}

func TestNewRegistryEmailWinsOverDomain(t *testing.T) {
	r, err := NewRegistry(config.DefaultConfig())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "mail foo@bar.com now"
	redacted, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	if want := "mail <EMAIL_1> now"; redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
}

func TestNewRegistryRespectsToggles(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*config.Config)
		text       string
		wantOutput string
	}{
		{
			name:       "email_disabled",
			configure:  func(c *config.Config) { c.PII.EnableEmail = false },
			text:       "mail foo@bar.com now",
			wantOutput: "mail foo@bar.com now",
		},
		{
			name:       "phone_disabled",
			configure:  func(c *config.Config) { c.PII.EnablePhone = false },
			text:       "call +14155552671 now",
			wantOutput: "call +14155552671 now",
		},
		{
			name:       "creditcard_disabled",
			configure:  func(c *config.Config) { c.PII.EnableCreditCard = false },
			text:       "card 4111111111111111 ok",
			wantOutput: "card 4111111111111111 ok",
		},
		{
			name:       "ssn_disabled",
			configure:  func(c *config.Config) { c.PII.EnableSSN = false },
			text:       "ssn 123-45-6789 ok",
			wantOutput: "ssn 123-45-6789 ok",
		},
		{
			name:       "ip_disabled",
			configure:  func(c *config.Config) { c.PII.EnableIP = false },
			text:       "host 10.0.0.1 ok",
			wantOutput: "host 10.0.0.1 ok",
		},
		{
			name:       "path_disabled",
			configure:  func(c *config.Config) { c.PII.EnablePath = false },
			text:       `open /home/alice/x.go ok`,
			wantOutput: `open /home/alice/x.go ok`,
		},
		{
			name:       "apikey_disabled",
			configure:  func(c *config.Config) { c.PII.EnableAPIKey = false },
			text:       "key sk-proj-abc123def456ghi789jkl012mno345pqr678 ok",
			wantOutput: "key sk-proj-abc123def456ghi789jkl012mno345pqr678 ok",
		},
		{
			name:       "credential_disabled",
			configure:  func(c *config.Config) { c.PII.EnableCredential = false },
			text:       "password=hunter2 ok",
			wantOutput: "password=hunter2 ok",
		},
		{
			name:       "credential_enabled",
			configure:  func(c *config.Config) {},
			text:       "password=hunter2 ok",
			wantOutput: "password=<CRED_1> ok",
		},
		{
			name:       "email_enabled",
			configure:  func(c *config.Config) {},
			text:       "mail foo@bar.com now",
			wantOutput: "mail <EMAIL_1> now",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			tc.configure(cfg)

			r, err := NewRegistry(cfg)
			if err != nil {
				t.Fatalf("NewRegistry: %v", err)
			}

			got, mapping := r.Redactor().Redact(tc.text)
			if got != tc.wantOutput {
				t.Fatalf("Redact(%q) = %q, want %q", tc.text, got, tc.wantOutput)
			}
			PutMapping(mapping)
		})
	}
}

func TestNewRegistryAllDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.EnableEmail = false
	cfg.PII.EnablePhone = false
	cfg.PII.EnableCreditCard = false
	cfg.PII.EnableSSN = false
	cfg.PII.EnableIP = false
	cfg.PII.EnablePath = false
	cfg.PII.EnableAPIKey = false
	cfg.PII.EnableCredential = false
	cfg.PII.EnableDomain = false
	cfg.PII.EnableClientName = false
	cfg.PII.EnablePersonName = false

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(r.detectors) != 0 {
		t.Fatalf("detector count = %d, want 0", len(r.detectors))
	}

	text := "foo@bar.com +14155552671 4111111111111111 123-45-6789 10.0.0.1 /home/a/b"
	if got, _ := r.Redactor().Redact(text); got != text {
		t.Fatalf("Redact = %q, want unchanged %q", got, text)
	}
}

func TestNewRegistryRedactsAndRestoresMixedText(t *testing.T) {
	r, err := NewRegistry(config.DefaultConfig())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "mail foo@bar.com, host 10.0.0.1, ssh /home/alice/id_rsa"
	redacted, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "mail <EMAIL_1>, host <IP_1>, ssh <PATH_1>"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
	if restored := r.Unredactor().Unredact(redacted, mapping); restored != text {
		t.Fatalf("round trip = %q, want %q", restored, text)
	}
}

// TestCredentialRedactionEndToEnd drives the default configuration through the
// registry and checks that a credential is reported as its own category and
// restored on the way back. The credential is interleaved with an email and a
// domain so the placeholder numbering is exercised per category.
func TestCredentialRedactionEndToEnd(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.EnableCredential = true
	if !cfg.PII.EnableCredential {
		t.Fatal("enable_credential = false, want true")
	}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if d := findDetector(r, CategoryCredential); d == nil {
		t.Fatal("credential detector not wired by default")
	}

	text := "mail ops@acme.io with password=hunter2 then visit acme.io"
	redacted, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "mail <EMAIL_1> with password=<CRED_1> then visit <DOMAIN_1>"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
	if got, ok := mapping.Lookup("<CRED_1>"); !ok || got != "hunter2" {
		t.Fatalf("Lookup(<CRED_1>) = (%q, %v), want (hunter2, true)", got, ok)
	}
	if restored := r.Unredactor().Unredact(redacted, mapping); restored != text {
		t.Fatalf("round trip = %q, want %q", restored, text)
	}
}

// TestDenylistOverride verifies that a per-category denylist removes a match the
// base detector would otherwise report, while other values in the same
// category are still redacted.
func TestDenylistOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Domain.Denylist = []string{"example.com"}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "visit example.com and acme.io"
	redacted, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "visit example.com and <DOMAIN_1>"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
	for placeholder, original := range mapping.All() {
		if original == "example.com" {
			t.Fatalf("denylisted value kept a placeholder: %s", placeholder)
		}
	}
	if got, ok := mapping.Lookup("<DOMAIN_1>"); !ok || got != "acme.io" {
		t.Fatalf("Lookup(<DOMAIN_1>) = (%q, %v), want (acme.io, true)", got, ok)
	}
}

// TestCustomNamesOnEmail verifies that [pii.email].names values are redacted as
// EMAIL placeholders: both a literal address the base detector also recognises
// and a non-address label that only the configured dictionary can supply.
func TestCustomNamesOnEmail(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Email.Names = []string{"specific@corp.io", "Corporate Mailbox"}

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	d := findDetector(r, CategoryEmail)
	if _, ok := d.(*customizingDetector); !ok {
		t.Fatalf("email detector = %T, want *customizingDetector", d)
	}

	text := "write to specific@corp.io or Corporate Mailbox today"
	redacted, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "write to <EMAIL_1> or <EMAIL_2> today"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
	if got, ok := mapping.Lookup("<EMAIL_1>"); !ok || got != "specific@corp.io" {
		t.Fatalf("Lookup(<EMAIL_1>) = (%q, %v), want (specific@corp.io, true)", got, ok)
	}
	if got, ok := mapping.Lookup("<EMAIL_2>"); !ok || got != "Corporate Mailbox" {
		t.Fatalf("Lookup(<EMAIL_2>) = (%q, %v), want (Corporate Mailbox, true)", got, ok)
	}
}

// TestPerCategoryEnabledOverride verifies that a per-category block wins over a
// flat toggle that is switched on.
func TestPerCategoryEnabledOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.EnableEmail = true
	cfg.PII.Email.Enabled = boolPtr(false)

	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if d := findDetector(r, CategoryEmail); d != nil {
		t.Fatalf("email detector wired despite [pii.email].enabled = false: %T", d)
	}

	text := "mail foo@bar.com with password=hunter2"
	redacted, mapping := r.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "mail foo@bar.com with password=<CRED_1>"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
}

func TestNewRegistryNilConfig(t *testing.T) {
	r, err := NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry(nil): %v", err)
	}
	if len(r.detectors) != 0 {
		t.Fatalf("detector count = %d, want 0", len(r.detectors))
	}
	if r.Redactor() == nil || r.Unredactor() == nil {
		t.Fatal("Redactor()/Unredactor() must not be nil")
	}
}
