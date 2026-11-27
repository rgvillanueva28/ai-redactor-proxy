package detectors

import (
	"regexp"
	"strings"

	"github.com/local/redactor/internal/pii/core"
)

// domainCandidatePattern matches host-like tokens: one or more dot separated
// labels followed by an alphabetic top level domain. Labels may contain
// hyphens but must start and end with an alphanumeric character. Matches are
// only candidates; the TLD allowlist decides whether one is a real host name.
const domainCandidatePattern = `(?i)\b(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}\b`

// defaultTLDAllowlist is the curated set of suffixes the domain detector treats
// as real. It replaces the ICANN public suffix list, which is exhaustive by
// design and therefore accepted code identifiers as host names: "androidx.work",
// "json.py", "0.zip", "MEMORY.md" and "HomeControlService.app" all reduced to a
// plausible eTLD+1. A deliberately small allowlist keeps those out of the
// redaction path.
//
// Entries are lower case. Multi-part suffixes (co.uk, com.au, ...) are listed
// alongside their parent TLD; registrableSpan prefers the longer match.
var defaultTLDAllowlist = map[string]struct{}{
	// Common generic TLDs. Notably absent: app, work, zip and mov, which are
	// just as often file names or code package names.
	"com":    {},
	"org":    {},
	"net":    {},
	"edu":    {},
	"gov":    {},
	"info":   {},
	"biz":    {},
	"me":     {},
	"tv":     {},
	"us":     {},
	"ai":     {},
	"dev":    {},
	"tech":   {},
	"cloud":  {},
	"xyz":    {},
	"online": {},
	"site":   {},
	"io":     {},
	"co":     {},

	// Major country code TLDs.
	"uk": {}, "de": {}, "fr": {}, "cn": {}, "jp": {}, "au": {}, "ca": {},
	"br": {}, "in": {}, "ru": {}, "es": {}, "it": {}, "nl": {}, "se": {},
	"no": {}, "kr": {}, "tw": {}, "hk": {}, "sg": {}, "mx": {}, "ar": {},
	"za": {}, "nz": {}, "at": {}, "ch": {}, "be": {}, "dk": {}, "fi": {},
	"ie": {}, "pt": {}, "pl": {}, "cz": {}, "hu": {}, "ro": {}, "bg": {},
	"hr": {}, "sk": {}, "si": {}, "lt": {}, "lv": {}, "ee": {}, "is": {},
	"lu": {}, "mt": {}, "cy": {}, "gr": {}, "tr": {}, "il": {}, "ae": {},
	"sa": {}, "qa": {}, "kw": {}, "bh": {}, "om": {}, "jo": {}, "lb": {},
	"iq": {}, "ir": {}, "pk": {}, "bd": {}, "lk": {}, "np": {}, "mm": {},
	"th": {}, "vn": {}, "my": {}, "ph": {}, "id": {}, "kh": {}, "la": {},
	"mn": {}, "kz": {}, "uz": {}, "ge": {}, "am": {}, "az": {},

	// Multi-part suffixes, where the label before the country code is part of
	// the public suffix rather than the registrable name.
	"co.uk": {}, "org.uk": {}, "ac.uk": {}, "gov.uk": {},
	"com.au": {}, "net.au": {}, "org.au": {}, "edu.au": {}, "gov.au": {},
	"co.nz": {},
	"co.jp": {}, "ac.jp": {}, "or.jp": {}, "ne.jp": {},
	"com.br": {},
	"co.in":  {},
	"com.mx": {},
	"com.ar": {},
	"com.cn": {},
	"com.tw": {},
	"com.hk": {},
	"com.sg": {},
	"co.kr":  {},
	"com.tr": {},
	"co.za":  {},
	"co.il":  {},
	"co.th":  {},
	"com.my": {},
	"com.ph": {},
	"com.vn": {},
	"co.id":  {},
}

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
// top level domain, a public suffix on its own, a host whose suffix is not on
// the curated allowlist such as a file name like "main.go", and so on).
func registrableSpan(candidate string) (start, end int, ok bool) {
	// The allowlist is lower case while the reported match must keep the
	// original case. Domain names are ASCII, so lowering the candidate cannot
	// change its byte length and the offsets found below stay valid.
	lower := strings.ToLower(candidate)
	labels := strings.Split(lower, ".")

	// suffixLabels is the number of trailing labels covered by the public
	// suffix. An unknown last label means the candidate is not a host name at
	// all; a known multi-part suffix wins over its parent TLD, so "acme.co.uk"
	// reduces to "acme.co.uk" instead of "co.uk".
	suffixLabels := 0
	if _, known := defaultTLDAllowlist[labels[len(labels)-1]]; known {
		suffixLabels = 1
	}
	if len(labels) >= 2 {
		if _, known := defaultTLDAllowlist[labels[len(labels)-2]+"."+labels[len(labels)-1]]; known {
			suffixLabels = 2
		}
	}
	// No suffix at all, or nothing to the left of it: a bare public suffix
	// such as "co.uk" is not itself registrable.
	if suffixLabels == 0 || len(labels) <= suffixLabels {
		return 0, 0, false
	}

	// Walk to the label immediately left of the public suffix.
	registrable := len(labels) - suffixLabels - 1
	for i := 0; i < registrable; i++ {
		start += len(labels[i]) + 1
	}
	return start, len(candidate), true
}
