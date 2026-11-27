package pii

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/pii/detectors"
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

// -----------------------------------------------------------------------------
// Phase 4: 10 KB mixed-PII fixture, overlap precedence, benchmarks, edge cases
// -----------------------------------------------------------------------------

// minMixedPIIFixtureBytes is the "10 KB" the phase-4 benchmark and round-trip
// tests are specified against.
const minMixedPIIFixtureBytes = 10 * 1024

// mixedPIIClientNames is the small client-name dictionary used by the
// phase-4 registry, fixture and benchmarks.
var mixedPIIClientNames = []string{
	"Acme Corp",
	"Globex Industries",
	"Initech Systems",
	"Northwind Trading",
	"Umbrella Health",
}

// mixedPIIFixturePath is the checked-in 10 KB mixed-PII document used by the
// phase-4 integration tests and benchmarks. It is a realistic, interleaved
// business thread: ten email addresses, ten phone numbers, ten card numbers,
// five SSNs, ten IP addresses (nine IPv4 plus one IPv6), ten filesystem paths,
// five API keys, ten domains, five client names and two person names, i.e. 77
// PII instances, the ~80 the phase plan asks for, surrounded by PII-free prose.
const mixedPIIFixturePath = "testdata/mixed_pii_10kb.txt"

// loadMixedPII10KB reads the fixture and asserts the size the phase-4 budget
// (10 KB, i.e. 10240 bytes) is specified against.
func loadMixedPII10KB(tb testing.TB) string {
	tb.Helper()

	raw, err := os.ReadFile(mixedPIIFixturePath)
	if err != nil {
		tb.Fatalf("read fixture %s: %v", mixedPIIFixturePath, err)
	}
	if len(raw) < minMixedPIIFixtureBytes {
		tb.Fatalf("fixture is %d bytes, want at least %d", len(raw), minMixedPIIFixtureBytes)
	}
	return string(raw)
}

// mixedPIIRegistry returns the Registry the phase-4 integration tests and
// benchmarks run against: every detector enabled, including the opt-in
// person-name heuristic, plus the small client-name dictionary.
func mixedPIIRegistry(tb testing.TB) *Registry {
	tb.Helper()

	cfg := config.DefaultConfig()
	cfg.PII.ClientNames.Names = mixedPIIClientNames
	cfg.PII.EnablePersonName = true

	reg, err := NewRegistry(cfg)
	if err != nil {
		tb.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

// placeholderCategory returns the category encoded in a placeholder, e.g.
// "<EMAIL_12>" -> "EMAIL", or "" when placeholder is not shaped like one.
func placeholderCategory(placeholder string) string {
	end := strings.IndexByte(placeholder, '_')
	if len(placeholder) < 3 || placeholder[0] != '<' || end < 0 {
		return ""
	}
	return placeholder[1:end]
}

// TestMixedPIIFixtureShape pins the fixture the benchmark and round-trip tests
// rely on: it is at least 10 KB and every PII category is represented in
// roughly the planned quantity. It doubles as the "every category" edge case.
func TestMixedPIIFixtureShape(t *testing.T) {
	original := loadMixedPII10KB(t)
	if len(original) < minMixedPIIFixtureBytes {
		t.Fatalf("fixture is %d bytes, want at least %d", len(original), minMixedPIIFixtureBytes)
	}

	reg := mixedPIIRegistry(t)
	_, mapping := reg.Redactor().Redact(original)
	defer PutMapping(mapping)

	counts := make(map[string]int, 10)
	for ph := range mapping.All() {
		counts[placeholderCategory(ph)]++
	}

	want := map[string]int{
		string(CategoryEmail):      10,
		string(CategoryPhone):      10,
		string(CategoryCreditCard): 10,
		string(CategorySSN):        5,
		string(CategoryIP):         10,
		string(CategoryPath):       10,
		string(CategoryAPIKey):     5,
		string(CategoryDomain):     10,
		string(CategoryClientName): 5,
		string(CategoryPersonName): 2,
	}
	total := 0
	for category, minimum := range want {
		if counts[category] < minimum {
			t.Errorf("fixture has %d %s instances, want at least %d", counts[category], category, minimum)
		}
		total += counts[category]
	}
	if total < 70 {
		t.Errorf("fixture produced %d placeholders, want at least 70", total)
	}
}

// TestRedactEmptyInputRegistry covers the empty-input edge case through the
// full registry configuration rather than a mock detector.
func TestRedactEmptyInputRegistry(t *testing.T) {
	reg := mixedPIIRegistry(t)

	got, mapping := reg.Redactor().Redact("")
	defer PutMapping(mapping)

	if got != "" {
		t.Fatalf("redacted = %q, want empty", got)
	}
	if n := len(mapping.All()); n != 0 {
		t.Fatalf("mapping length = %d, want 0", n)
	}
}

// TestRedactNoPIIText verifies that a document without PII passes through the
// full detector set untouched.
func TestRedactNoPIIText(t *testing.T) {
	reg := mixedPIIRegistry(t)

	text := "the quarterly report looks fine and nothing here needs escalation\n"
	got, mapping := reg.Redactor().Redact(text)
	defer PutMapping(mapping)

	if got != text {
		t.Fatalf("redacted = %q, want unchanged %q", got, text)
	}
	if n := len(mapping.All()); n != 0 {
		t.Fatalf("mapping length = %d, want 0", n)
	}
}

// TestRedactVeryLongInput runs the full registry over ~64 KB of benign prose
// with three PII values buried in it, guarding against quadratic behaviour or
// offset bugs on long inputs.
func TestRedactVeryLongInput(t *testing.T) {
	const unit = "the team reviewed the report and moved the ticket forward without comment. "

	var b strings.Builder
	for b.Len() < 64*1024 {
		b.WriteString(unit)
	}
	b.WriteString("mail foo@bar.com host 10.0.0.1 path /home/alice/id_rsa\n")
	text := b.String()

	reg := mixedPIIRegistry(t)
	redacted, mapping := reg.Redactor().Redact(text)
	defer PutMapping(mapping)

	if n := len(mapping.All()); n != 3 {
		t.Fatalf("mapping length = %d, want 3", n)
	}
	for _, ph := range []string{"<EMAIL_1>", "<IP_1>", "<PATH_1>"} {
		if !strings.Contains(redacted, ph) {
			t.Fatalf("redacted output is missing %s", ph)
		}
	}
	if restored := reg.Unredactor().Unredact(redacted, mapping); restored != text {
		t.Fatal("long input did not round trip")
	}
}

// TestRedactEveryCategory puts one instance of every supported category into a
// single line and checks both the placeholder numbering and the round trip.
func TestRedactEveryCategory(t *testing.T) {
	const key = "sk-proj-abc123def456ghi789jkl012mno345pqr678"
	text := "mail foo@bar.com, call +14155552671, card 4111111111111111, " +
		"ssn 123-45-6789, host 10.0.0.1, path /home/alice/x.go then key " + key +
		" password=hunter2 and site acme.com, client Acme Corp, person John Smith"

	reg := mixedPIIRegistry(t)
	redacted, mapping := reg.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "mail <EMAIL_1>, call <PHONE_1>, card <CC_1>, ssn <SSN_1>, " +
		"host <IP_1>, path <PATH_1> then key <APIKEY_1> password=<CRED_1> " +
		"and site <DOMAIN_1>, client <CLIENT_1>, person <NAME_1>"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}

	if got, ok := mapping.Lookup("<NAME_1>"); !ok || got != "John Smith" {
		t.Fatalf("Lookup(<NAME_1>) = (%q, %v), want (John Smith, true)", got, ok)
	}
	if got, ok := mapping.Lookup("<APIKEY_1>"); !ok || got != key {
		t.Fatalf("Lookup(<APIKEY_1>) = (%q, %v), want (%q, true)", got, ok, key)
	}
	if restored := reg.Unredactor().Unredact(redacted, mapping); restored != text {
		t.Fatalf("round trip = %q, want %q", restored, text)
	}
}

// genericSecretCategory stands in for a hypothetical broad credential detector.
// It is not a production category; the test only needs a distinct placeholder
// name to prove which detector won the span.
const genericSecretCategory = Category("GENERIC")

// TestRedactOverlap_EmailVsDomain covers the most common nested span in real
// traffic: the domain inside an email address. The email detector must claim
// the whole address and the domain detector's span must be dropped.
func TestRedactOverlap_EmailVsDomain(t *testing.T) {
	email := detectors.NewEmail()
	domain := detectors.NewDomain()
	r := NewRedactor([]Detector{email, domain}) // registry order

	t.Run("address_domain_not_claimed_separately", func(t *testing.T) {
		text := "contact user@acme.com today"

		// The domain detector deliberately stays out of an address, so the only
		// candidate is the full email span.
		if got := domain.Detect(text); len(got) != 0 {
			t.Fatalf("domain detector reported %v inside an email address", got)
		}

		redacted, mapping := r.Redact(text)
		if want := "contact <EMAIL_1> today"; redacted != want {
			t.Fatalf("Redact = %q, want %q", redacted, want)
		}
		if _, ok := mapping.Lookup("<DOMAIN_1>"); ok {
			t.Fatal("domain placeholder was created inside an email address")
		}
		if n := len(mapping.All()); n != 1 {
			t.Fatalf("mapping length = %d, want 1", n)
		}
	})

	t.Run("nested_domain_loses_to_address", func(t *testing.T) {
		// "acme.com@user.io" has a domain as its local part, so the domain
		// detector really does report a span nested inside the email. This is
		// the overlap case proper: leftmost-longest keeps the email.
		text := "mail acme.com@user.io now"
		nestedStart := strings.Index(text, "acme.com")

		got := domain.Detect(text)
		if len(got) != 1 || got[0].Start != nestedStart || got[0].End != nestedStart+len("acme.com") {
			t.Fatalf("domain.Detect = %v, want one span %q at %d", got, "acme.com", nestedStart)
		}

		redacted, mapping := r.Redact(text)
		if want := "mail <EMAIL_1> now"; redacted != want {
			t.Fatalf("Redact = %q, want %q", redacted, want)
		}
		if original, ok := mapping.Lookup("<EMAIL_1>"); !ok || original != "acme.com@user.io" {
			t.Fatalf("Lookup(<EMAIL_1>) = (%q, %v), want (acme.com@user.io, true)", original, ok)
		}
		if _, ok := mapping.Lookup("<DOMAIN_1>"); ok {
			t.Fatal("nested domain span was accepted over the enclosing email span")
		}
	})
}

// TestRedactOverlap_PathVsDomain covers file names that look like domains and
// domains embedded in a path. The path detector claims the whole path and any
// nested domain span is dropped.
func TestRedactOverlap_PathVsDomain(t *testing.T) {
	path := detectors.NewPath()
	domain := detectors.NewDomain()
	r := NewRedactor([]Detector{path, domain}) // registry order

	t.Run("code_file_name_is_not_a_domain", func(t *testing.T) {
		text := "edit /home/alice/main.go now"

		// "main.go" has no managed TLD, so the domain detector must not report
		// it at all; only the path can win.
		if got := domain.Detect(text); len(got) != 0 {
			t.Fatalf("domain detector reported %v for a file name", got)
		}

		redacted, mapping := r.Redact(text)
		if want := "edit <PATH_1> now"; redacted != want {
			t.Fatalf("Redact = %q, want %q", redacted, want)
		}
		if original, ok := mapping.Lookup("<PATH_1>"); !ok || original != "/home/alice/main.go" {
			t.Fatalf("Lookup(<PATH_1>) = (%q, %v), want (/home/alice/main.go, true)", original, ok)
		}
		if _, ok := mapping.Lookup("<DOMAIN_1>"); ok {
			t.Fatal("domain placeholder was created for a non-domain file name")
		}
	})

	t.Run("domain_nested_inside_path", func(t *testing.T) {
		// The path contains a real registrable domain, so the domain detector
		// reports a genuinely overlapping span that the path must displace.
		text := "open /home/alice/acme.com/notes.txt now"
		nestedStart := strings.Index(text, "acme.com")

		got := domain.Detect(text)
		if len(got) != 1 || got[0].Start != nestedStart || got[0].End != nestedStart+len("acme.com") {
			t.Fatalf("domain.Detect = %v, want one span %q at %d", got, "acme.com", nestedStart)
		}

		redacted, mapping := r.Redact(text)
		if want := "open <PATH_1> now"; redacted != want {
			t.Fatalf("Redact = %q, want %q", redacted, want)
		}
		if original, ok := mapping.Lookup("<PATH_1>"); !ok || original != "/home/alice/acme.com/notes.txt" {
			t.Fatalf("Lookup(<PATH_1>) = (%q, %v), want (/home/alice/acme.com/notes.txt, true)", original, ok)
		}
		if _, ok := mapping.Lookup("<DOMAIN_1>"); ok {
			t.Fatal("nested domain span was accepted over the enclosing path span")
		}
	})
}

// TestRedactOverlap_APIKeyVsGeneric covers credential tokens, where several
// patterns see the same bytes: the API-key detector must claim the full token
// and a broader "generic secret" detector must lose.
func TestRedactOverlap_APIKeyVsGeneric(t *testing.T) {
	const key = "sk-proj-8fJ2kLm9QpR4tUvW6xYzA1bC3dE5fG7hI9jK"
	apiKey := detectors.NewAPIKey()

	t.Run("detector_claims_whole_token", func(t *testing.T) {
		text := "key " + key + " end"

		// Both the specific "sk-proj-" pattern and the generic "sk-" pattern
		// match; they must collapse into one match spanning the whole key.
		got := apiKey.Detect(text)
		start := strings.Index(text, key)
		if len(got) != 1 || got[0].Start != start || got[0].End != start+len(key) {
			t.Fatalf("APIKey.Detect = %v, want one span covering %q", got, key)
		}
	})

	t.Run("shorter_generic_prefix_loses", func(t *testing.T) {
		text := "key " + key + " end"
		start := strings.Index(text, key)

		generic := mockDetector{
			cat: genericSecretCategory,
			matches: []Match{{
				Start:    start,
				End:      start + len("sk-proj-"),
				Category: genericSecretCategory,
			}},
		}
		r := NewRedactor([]Detector{apiKey, generic})

		redacted, mapping := r.Redact(text)
		if want := "key <APIKEY_1> end"; redacted != want {
			t.Fatalf("Redact = %q, want %q", redacted, want)
		}
		if original, ok := mapping.Lookup("<APIKEY_1>"); !ok || original != key {
			t.Fatalf("Lookup(<APIKEY_1>) = (%q, %v), want (%q, true)", original, ok, key)
		}
		if _, ok := mapping.Lookup("<GENERIC_1>"); ok {
			t.Fatal("generic prefix span was accepted over the full API key")
		}
	})

	t.Run("exact_tie_first_detector_wins", func(t *testing.T) {
		text := "key " + key + " end"
		start := strings.Index(text, key)
		generic := mockDetector{
			cat: genericSecretCategory,
			matches: []Match{{
				Start:    start,
				End:      start + len(key),
				Category: genericSecretCategory,
			}},
		}

		// API key first in the detector list: it wins the exact tie.
		redacted, mapping := NewRedactor([]Detector{apiKey, generic}).Redact(text)
		if want := "key <APIKEY_1> end"; redacted != want {
			t.Fatalf("Redact = %q, want %q", redacted, want)
		}
		if _, ok := mapping.Lookup("<GENERIC_1>"); ok {
			t.Fatal("later detector won an exact tie")
		}

		// Reversed order: the generic detector now wins, which shows the
		// tie-break is purely positional and not category-specific.
		redacted, mapping = NewRedactor([]Detector{generic, apiKey}).Redact(text)
		if want := "key <GENERIC_1> end"; redacted != want {
			t.Fatalf("Redact (reversed) = %q, want %q", redacted, want)
		}
	})

	t.Run("registry_end_to_end", func(t *testing.T) {
		reg := mixedPIIRegistry(t)
		redacted, mapping := reg.Redactor().Redact("key " + key + " end")
		defer PutMapping(mapping)

		if want := "key <APIKEY_1> end"; redacted != want {
			t.Fatalf("Redact = %q, want %q", redacted, want)
		}
	})
}

// TestRedactCredentialPlaceholderInMixedOutput checks that a credential in a
// mixed-PII sentence is replaced by a CRED placeholder and still restores, with
// the surrounding email redacted independently.
func TestRedactCredentialPlaceholderInMixedOutput(t *testing.T) {
	reg := mixedPIIRegistry(t)

	text := "login with password=hunter2 then mail foo@bar.com"
	redacted, mapping := reg.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "login with password=<CRED_1> then mail <EMAIL_1>"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
	if !strings.Contains(redacted, "<CRED_1>") {
		t.Fatalf("redacted output is missing the CRED placeholder: %q", redacted)
	}
	if got, ok := mapping.Lookup("<CRED_1>"); !ok || got != "hunter2" {
		t.Fatalf("Lookup(<CRED_1>) = (%q, %v), want (hunter2, true)", got, ok)
	}
	if restored := reg.Unredactor().Unredact(redacted, mapping); restored != text {
		t.Fatalf("round trip = %q, want %q", restored, text)
	}
}

// TestRedactDenylistSuppressesDomainInPipeline checks the full redaction
// pipeline (registry configuration through the redactor) leaves a denylisted
// domain alone while continuing to redact other domains.
func TestRedactDenylistSuppressesDomainInPipeline(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PII.Domain.Denylist = []string{"example.com"}

	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	text := "see example.com and acme.io but not example.org"
	redacted, mapping := reg.Redactor().Redact(text)
	defer PutMapping(mapping)

	want := "see example.com and <DOMAIN_1> but not <DOMAIN_2>"
	if redacted != want {
		t.Fatalf("Redact = %q, want %q", redacted, want)
	}
	for placeholder, original := range mapping.All() {
		if original == "example.com" {
			t.Fatalf("denylisted domain kept a placeholder: %s", placeholder)
		}
	}
}

// BenchmarkRedactor measures one full redact cycle (detect, resolve overlaps,
// build placeholders) over the 10 KB mixed-PII fixture and returns the mapping
// to the pool, so the figure reflects steady-state request handling.
//
// Reference hardware: Intel Core i5-9500T (6 logical cores, 2.20 GHz base),
// Windows, Go 1.26.1. The phase-4 plan targeted < 5 ms/op; measured when this
// benchmark landed it reports about 6.6 ms/op and 2570 allocs/op (~1.5 MB/s).
// Go's regexp engine sustains only ~25-40 MB/s per pass on this CPU and the
// full detector set makes roughly 17 passes over the document; the API key
// prefix table (12 sequential patterns) accounts for ~3.2 ms of that on its
// own, and the IP detector for ~1.0 ms. Batching the API key prefixes into a
// single alternation is the obvious follow-up and should bring the cycle to
// ~3.7 ms/op; it is a source change outside this test-only phase.
func BenchmarkRedactor(b *testing.B) {
	input := loadMixedPII10KB(b)
	reg := mixedPIIRegistry(b)
	redactor := reg.Redactor()

	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		out, mapping := redactor.Redact(input)
		if out == "" || mapping == nil {
			b.Fatal("Redact returned an empty result")
		}
		PutMapping(mapping)
	}
}
