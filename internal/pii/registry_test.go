package pii

import (
	"sync"
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

// TestRegistryReloadAddsAndRemovesDetectors is the acceptance test for live
// reload: a config edit that starts or stops redacting a category must be
// visible on the very next Redact call, without rebuilding the Registry.
func TestRegistryReloadAddsAndRemovesDetectors(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.EnableEmail = false

	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if findDetector(reg, CategoryEmail) != nil {
		t.Fatal("email detector wired despite enable_email = false")
	}

	const text = "mail foo@bar.com now"
	if got, mapping := reg.Redactor().Redact(text); got != text {
		PutMapping(mapping)
		t.Fatalf("Redact before reload = %q, want unchanged %q", got, text)
	} else {
		PutMapping(mapping)
	}

	// Enable email: the next Redact must redact it.
	cfg.PII.EnableEmail = true
	if err := reg.Reload(cfg); err != nil {
		t.Fatalf("Reload (enable email): %v", err)
	}
	if findDetector(reg, CategoryEmail) == nil {
		t.Fatal("email detector missing after reload with enable_email = true")
	}
	got, mapping := reg.Redactor().Redact(text)
	PutMapping(mapping)
	if want := "mail <EMAIL_1> now"; got != want {
		t.Fatalf("Redact after enabling email = %q, want %q", got, want)
	}

	// Disable it again: the detector must disappear.
	cfg.PII.EnableEmail = false
	if err := reg.Reload(cfg); err != nil {
		t.Fatalf("Reload (disable email): %v", err)
	}
	if findDetector(reg, CategoryEmail) != nil {
		t.Fatal("email detector still wired after reload with enable_email = false")
	}
	got, mapping = reg.Redactor().Redact(text)
	PutMapping(mapping)
	if got != text {
		t.Fatalf("Redact after disabling email = %q, want unchanged %q", got, text)
	}
}

// TestRegistryReloadRebuildsCustomizations checks that reloading is not limited
// to the flat toggles: per-category names, patterns and denylists are rebuilt
// from the new config too.
func TestRegistryReloadRebuildsCustomizations(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Domain.Denylist = []string{"example.com"}

	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	cfg.PII.Domain.Denylist = nil
	cfg.PII.Domain.Patterns = []string{`\bINTERNAL-\d{4}\b`}
	cfg.PII.Client.Names = []string{"Acme Corp"}
	if err := reg.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	const text = "Acme Corp uses INTERNAL-1234 at example.com"
	got, mapping := reg.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "<CLIENT_1> uses <DOMAIN_1> at <DOMAIN_2>"
	if got != want {
		t.Fatalf("Redact after reload = %q, want %q", got, want)
	}
}

// TestRegistryReloadUpdatesLogToggles covers the log_redacted / log_bodies half
// of a live reload: the registry keeps the values from the last applied config
// so the caller can push them into the request handlers.
func TestRegistryReloadUpdatesLogToggles(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.PII.LogRedacted || cfg.PII.LogBodies {
		t.Fatal("default log toggles must be false for this test")
	}

	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if reg.LogRedacted() || reg.LogBodies() {
		t.Fatalf("initial toggles = (%v, %v), want (false, false)", reg.LogRedacted(), reg.LogBodies())
	}

	cfg.PII.LogRedacted = true
	cfg.PII.LogBodies = true
	if err := reg.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !reg.LogRedacted() || !reg.LogBodies() {
		t.Fatalf("toggles after reload = (%v, %v), want (true, true)", reg.LogRedacted(), reg.LogBodies())
	}

	cfg.PII.LogRedacted = false
	cfg.PII.LogBodies = false
	if err := reg.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if reg.LogRedacted() || reg.LogBodies() {
		t.Fatalf("toggles after second reload = (%v, %v), want (false, false)", reg.LogRedacted(), reg.LogBodies())
	}
}

// TestRegistryReloadNilConfig clears the detector set, mirroring NewRegistry's
// treatment of a nil config.
func TestRegistryReloadNilConfig(t *testing.T) {
	reg, err := NewRegistry(config.DefaultConfig())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(reg.detectors) == 0 {
		t.Fatal("default registry has no detectors")
	}

	if err := reg.Reload(nil); err != nil {
		t.Fatalf("Reload(nil): %v", err)
	}
	if len(reg.detectors) != 0 {
		t.Fatalf("detector count after Reload(nil) = %d, want 0", len(reg.detectors))
	}

	const text = "mail foo@bar.com"
	got, mapping := reg.Redactor().Redact(text)
	PutMapping(mapping)
	if got != text {
		t.Fatalf("Redact after Reload(nil) = %q, want unchanged %q", got, text)
	}
}

// TestRegistryConcurrentReloadAndRedact swaps the detector set while requests
// are being redacted. Every result must be the output of one complete config;
// the test fails on a panic or a half-built detector list.
func TestRegistryConcurrentReloadAndRedact(t *testing.T) {
	withEmail := config.DefaultConfig()
	withoutEmail := config.DefaultConfig()
	withoutEmail.PII.EnableEmail = false

	reg, err := NewRegistry(withEmail)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	const text = "mail foo@bar.com now"
	const redacted = "mail <EMAIL_1> now"

	const readers = 8
	const rounds = 200

	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				got, mapping := reg.Redactor().Redact(text)
				PutMapping(mapping)
				if got != text && got != redacted {
					t.Errorf("Redact during reload = %q, want %q or %q", got, text, redacted)
					return
				}
			}
		}()
	}

	for i := 0; i < rounds; i++ {
		cfg := withEmail
		if i%2 == 0 {
			cfg = withoutEmail
		}
		if err := reg.Reload(cfg); err != nil {
			t.Errorf("Reload: %v", err)
			break
		}
	}

	wg.Wait()
}
