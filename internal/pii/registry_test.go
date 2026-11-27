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
