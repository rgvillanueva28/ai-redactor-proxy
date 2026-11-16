package detectors

import (
	"net"
	"regexp"
	"strings"

	"github.com/local/redactor/internal/pii/core"
)

const (
	// ipv4Pattern finds dotted-quad candidates; the octets are validated
	// afterwards.
	ipv4Pattern = `\b(?:\d{1,3}\.){3}\d{1,3}\b`
	// ipv6RunPattern finds maximal runs of IPv6 characters. Runs are handed to
	// net.ParseIP, which does the real validation.
	ipv6RunPattern = `[0-9A-Fa-f:.]+`
)

// IP detects IPv4 and IPv6 addresses.
type IP struct {
	v4 *regexp.Regexp
	v6 *regexp.Regexp
}

// NewIP returns an IP detector with both patterns compiled once.
func NewIP() *IP {
	return &IP{
		v4: regexp.MustCompile(ipv4Pattern),
		v6: regexp.MustCompile(ipv6RunPattern),
	}
}

// Category implements core.Detector.
func (d *IP) Category() core.Category { return core.CategoryIP }

// Detect returns every IP address in text, in document order.
func (d *IP) Detect(text string) []core.Match {
	var matches []core.Match

	for _, loc := range d.v4.FindAllStringIndex(text, -1) {
		cand := text[loc[0]:loc[1]]
		if !validIPv4(cand) {
			continue
		}
		matches = append(matches, core.Match{
			Start:    loc[0],
			End:      loc[1],
			Category: core.CategoryIP,
			Value:    cand,
		})
	}

	for _, loc := range d.v6.FindAllStringIndex(text, -1) {
		run := text[loc[0]:loc[1]]
		// A colon is what makes a run a possible IPv6 literal; without one the
		// run is just a hex-looking word such as "deadbeef".
		if strings.Count(run, ":") < 2 {
			continue
		}
		cand, ok := parseIPv6Run(run)
		if !ok {
			continue
		}
		matches = append(matches, core.Match{
			Start:    loc[0],
			End:      loc[0] + len(cand),
			Category: core.CategoryIP,
			Value:    cand,
		})
	}

	return sortAndDedupe(matches)
}

// validIPv4 reports whether s is a dotted quad with every octet in range.
func validIPv4(s string) bool {
	if strings.Count(s, ".") != 3 {
		return false
	}
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

// parseIPv6Run returns the longest prefix of run that parses as an IPv6
// address. Trailing prose punctuation (a sentence-final period, a quoting
// colon) is trimmed first so that "2001:db8::1." still matches.
func parseIPv6Run(run string) (string, bool) {
	cand := run
	for cand != "" {
		if strings.Count(cand, ":") >= 2 && net.ParseIP(cand) != nil {
			return cand, true
		}
		last := cand[len(cand)-1]
		if last != '.' && last != ':' {
			return "", false
		}
		cand = cand[:len(cand)-1]
	}
	return "", false
}
