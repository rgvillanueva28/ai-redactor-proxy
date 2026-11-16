package detectors

import (
	"strings"

	aho "github.com/petar-dambovaliev/aho-corasick"

	"github.com/local/redactor/internal/pii/core"
)

// ClientName detects a configured list of literal client names, building the
// Aho-Corasick automaton once so per-request detection is a single pass over
// the text.
//
// The WASI build of BurntSushi's automaton (github.com/wasilibs/go-aho-corasick)
// was measured first: importing it cost roughly 230ms of process start-up
// (246ms vs 13ms for a hello-world binary) because it compiles a WebAssembly
// module in package init, and that cost is paid even when client-name
// detection is disabled. That exceeds the 100ms budget for a proxy start-up,
// so this uses the pure-Go implementation instead.
type ClientName struct {
	// names holds the configured patterns in automaton order.
	names []string
	ac    aho.AhoCorasick
}

// NewClientName builds a case-insensitive, leftmost-longest automaton over
// names. Blank entries are ignored; a list with no usable entries yields a
// detector that never matches.
func NewClientName(names []string) (*ClientName, error) {
	cleaned := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		cleaned = append(cleaned, name)
	}
	if len(cleaned) == 0 {
		return &ClientName{}, nil
	}

	// The NFA build keeps construction cheap and memory bounded for long lists;
	// the DFA variant would allocate a transition table per state.
	builder := aho.NewAhoCorasickBuilder(aho.Opts{
		AsciiCaseInsensitive: true,
		MatchKind:            aho.LeftMostLongestMatch,
	})
	return &ClientName{names: cleaned, ac: builder.Build(cleaned)}, nil
}

// Category implements core.Detector.
func (d *ClientName) Category() core.Category { return core.CategoryClientName }

// Detect returns every configured client name in text, case-insensitively, in
// document order. The reported value keeps the original casing.
func (d *ClientName) Detect(text string) []core.Match {
	if len(d.names) == 0 {
		return nil
	}

	found := d.ac.FindAll(text)
	if len(found) == 0 {
		return nil
	}

	matches := make([]core.Match, 0, len(found))
	for _, m := range found {
		start, end := m.Start(), m.End()
		if start < 0 || end > len(text) || start >= end {
			continue
		}
		matches = append(matches, core.Match{
			Start:    start,
			End:      end,
			Category: core.CategoryClientName,
			Value:    text[start:end],
		})
	}
	return matches
}
