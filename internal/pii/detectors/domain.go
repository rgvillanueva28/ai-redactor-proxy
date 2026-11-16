package detectors

import (
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/local/redactor/internal/pii/core"
)

// domainCandidatePattern matches host-like tokens: one or more dot separated
// labels followed by an alphabetic top level domain. Labels may contain
// hyphens but must start and end with an alphanumeric character. Matches are
// only candidates; publicsuffix decides which part of them is registrable.
const domainCandidatePattern = `(?i)\b(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}\b`

// Domain detects domain names and reports only the registrable domain (the
// "eTLD+1"), leaving any subdomain prefix in place: for api.acme.com the match
// covers acme.com, so the redactor turns the text into "api.<DOMAIN_1>".
type Domain struct {
	re *regexp.Regexp
}

// NewDomain returns a Domain detector with its candidate pattern compiled once.
func NewDomain() *Domain {
	return &Domain{re: regexp.MustCompile(domainCandidatePattern)}
}

// Category implements core.Detector.
func (d *Domain) Category() core.Category { return core.CategoryDomain }

// Detect returns the registrable domain of every host-like token in text, in
// document order.
func (d *Domain) Detect(text string) []core.Match {
	locs := d.re.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}

	matches := make([]core.Match, 0, len(locs))
	for _, loc := range locs {
		// A candidate directly preceded by "@" is the domain part of an email
		// address. Emails are the more specific category, so the domain
		// detector stays out of the way instead of reporting a nested span.
		if loc[0] > 0 && text[loc[0]-1] == '@' {
			continue
		}

		start, end, ok := registrableSpan(text[loc[0]:loc[1]])
		if !ok {
			continue
		}
		matches = append(matches, core.Match{
			Start:    loc[0] + start,
			End:      loc[0] + end,
			Category: core.CategoryDomain,
			Value:    text[loc[0]+start : loc[0]+end],
		})
	}
	return matches
}

// registrableSpan returns the byte offsets of the registrable domain inside
// candidate, or ok == false when candidate is not a registrable domain (a bare
// top level domain, a host whose top level domain is not in the public suffix
// list such as a file name like "main.go", and so on).
func registrableSpan(candidate string) (start, end int, ok bool) {
	// publicsuffix matches against a lower-case table, so upper-case input
	// would silently fall through to the "*" default rule. Domain names are
	// ASCII, so lowering the candidate cannot change its byte length.
	lower := strings.ToLower(candidate)

	registrable, err := publicsuffix.EffectiveTLDPlusOne(lower)
	if err != nil {
		return 0, 0, false
	}

	// Require a TLD that the public suffix list actually manages. Without this
	// check every "name.ext" token (main.go, report.pdf) would look like a
	// registrable domain, because the list applies the "*" rule to unknown
	// suffixes.
	tld := lower[strings.LastIndexByte(lower, '.')+1:]
	if tld == "" {
		return 0, 0, false
	}
	if _, icann := publicsuffix.PublicSuffix(tld); !icann {
		return 0, 0, false
	}

	start = suffixStart(candidate, registrable)
	if start < 0 {
		return 0, 0, false
	}
	return start, len(candidate), true
}

// suffixStart returns the offset at which suffix (a registrable domain derived
// from candidate and therefore a case-insensitive suffix of it) begins, or -1
// when suffix is not a suffix of candidate.
func suffixStart(candidate, suffix string) int {
	if len(suffix) > len(candidate) {
		return -1
	}
	if !strings.EqualFold(candidate[len(candidate)-len(suffix):], suffix) {
		return -1
	}

	// Walk back over one label per dot in suffix, plus the label before the
	// first dot, to find where the registrable part starts.
	labels := strings.Count(suffix, ".")
	idx := len(candidate)
	for i := 0; i <= labels; i++ {
		dot := strings.LastIndexByte(candidate[:idx], '.')
		if dot < 0 {
			// The registrable domain is the whole candidate.
			return 0
		}
		idx = dot
	}
	return idx + 1
}
