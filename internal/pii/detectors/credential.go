package detectors

import (
	"regexp"
	"strings"

	"github.com/local/redactor/internal/pii/core"
)

// credentialPattern describes one credential shape and which submatch groups
// delimit the sensitive part of a match. Only the value (or the URL userinfo)
// is reported, never the surrounding key label, so the redactor can leave
// "password=<CRED_1>" readable.
type credentialPattern struct {
	re         *regexp.Regexp
	start      int  // submatch index whose start opens the reported span
	end        int  // submatch index whose end closes the reported span
	standalone bool // user:password literal, so extra false-positive guards apply
}

// credentialKeyLabels are the field names used by the key=value, JSON and YAML
// patterns. They are also rejected by the standalone user:pass pattern so that
// "password:hunter2" (no space) is reported as the value only rather than as a
// whole "password:hunter2" credential.
var credentialKeyLabels = []string{
	"password", "passwd", "pwd", "secret", "token",
	"api_key", "apikey", "access_key",
}

// credentialPatterns holds every credential shape. They are compiled once at
// package init and scanned in order; sortAndDedupe collapses overlaps when one
// pattern reports a span nested inside another.
var credentialPatterns = []credentialPattern{
	// URL userinfo: the optional scheme is consumed so it is not mistaken for
	// the username, and the reported span is user:password (groups 1-2), not
	// the trailing @host.
	{
		re:    regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.\-]*://)?([a-zA-Z0-9._%+\-]+):([^\s@/]+)@([a-zA-Z0-9.\-]+)`),
		start: 1,
		end:   2,
	},
	// Query string / form data: password=hunter2, api_key=..., and so on. Only
	// the value (group 1) is reported.
	{
		re:    regexp.MustCompile(`(?i)(?:password|passwd|pwd|secret|token|api_key|apikey|access_key)=([^\s&"']+)`),
		start: 1,
		end:   1,
	},
	// JSON object field: {"password": "s3cret"}. Only the value (group 1) is
	// reported.
	{
		re:    regexp.MustCompile(`(?i)"(?:password|passwd|pwd|secret|token|api_key|apikey|access_key)"\s*:\s*"([^"]+)"`),
		start: 1,
		end:   1,
	},
	// YAML / prose: "password: hunter2". Only the value (group 1) is reported.
	{
		re:    regexp.MustCompile(`(?i)(?:password|passwd|pwd|secret|token)\s*:\s*(\S+)`),
		start: 1,
		end:   1,
	},
	// Standalone user:password literal with no host. Both groups are reported.
	// The value charset excludes '@' and '/' so a URL scheme or path is never
	// captured as the password.
	{
		re:         regexp.MustCompile(`(?i)\b([a-zA-Z][a-zA-Z0-9._\-]*):([^\s:@/]+)\b`),
		start:      1,
		end:        2,
		standalone: true,
	},
}

// Credential detects credentials in URL userinfo, query strings and form data,
// JSON fields, YAML/prose, and bare user:password literals. It reports only the
// secret portion of a keyed field so the label stays in the redacted text.
type CredentialDetector struct{}

// NewCredential returns a Credential detector backed by the package-level
// compiled patterns.
func NewCredential() *CredentialDetector {
	return &CredentialDetector{}
}

// Category implements core.Detector.
func (d *CredentialDetector) Category() core.Category { return core.CategoryCredential }

// Detect returns every credential in text, in document order, with overlapping
// spans collapsed.
func (d *CredentialDetector) Detect(text string) []core.Match {
	var matches []core.Match
	for _, p := range credentialPatterns {
		for _, loc := range p.re.FindAllStringSubmatchIndex(text, -1) {
			start, end := loc[p.start*2], loc[p.end*2+1]
			if start < 0 || end < 0 || start >= end {
				continue
			}
			if p.standalone {
				keyStart, keyEnd := loc[2], loc[3]
				if keyStart >= 0 && keyEnd > keyStart && isCredentialKey(text[keyStart:keyEnd]) {
					continue
				}
				// A purely numeric right-hand side is more likely a host:port
				// pair or a timestamp than a literal credential.
				valueStart, valueEnd := loc[4], loc[5]
				if valueStart >= 0 && valueEnd > valueStart && isAllDigits(text[valueStart:valueEnd]) {
					continue
				}
			}
			matches = append(matches, core.Match{
				Start:    start,
				End:      end,
				Category: core.CategoryCredential,
				Value:    text[start:end],
			})
		}
	}
	return sortAndDedupe(matches)
}

// isCredentialKey reports whether label names a credential field.
func isCredentialKey(label string) bool {
	for _, key := range credentialKeyLabels {
		if strings.EqualFold(label, key) {
			return true
		}
	}
	return false
}

// isAllDigits reports whether s is non-empty and made only of ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
