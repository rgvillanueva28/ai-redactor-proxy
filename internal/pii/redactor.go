package pii

import (
	"sort"
	"strings"
	"sync"
)

// Redactor applies a set of detectors to text and replaces every accepted match
// with a tagged placeholder.
//
// The detector set can be replaced at runtime with SetDetectors while Redact is
// called concurrently; each Redact call observes either the old list or the new
// one, never a partially built one.
type Redactor struct {
	mu        sync.RWMutex
	detectors []Detector
}

// NewRedactor returns a Redactor that runs detectors in the given order.
func NewRedactor(detectors []Detector) *Redactor {
	return &Redactor{detectors: detectors}
}

// SetDetectors replaces the detector set used by future Redact calls. It is
// safe to call concurrently with Redact: the swap is guarded by a write lock,
// so in-flight calls finish against the list they started with.
func (r *Redactor) SetDetectors(detectors []Detector) {
	r.mu.Lock()
	r.detectors = detectors
	r.mu.Unlock()
}

// candidate is a Match plus the index of the detector that produced it, used
// as the final tie-breaker during sorting.
type candidate struct {
	match Match
	order int
}

// Redact applies detectors to text. Overlapping matches: leftmost wins; on
// tie, longer wins; on tie, first detector in list wins. It returns the
// redacted text and the populated Mapping, which the caller owns (and may
// return to the pool with PutMapping once the request is finished).
func (r *Redactor) Redact(text string) (string, *Mapping) {
	mapping := GetMapping()

	// Take a snapshot of the slice header under the read lock and release it
	// before running detection. Detector implementations are immutable, so the
	// detector list captured here stays valid even if SetDetectors swaps in a
	// new one mid-flight.
	r.mu.RLock()
	detectors := r.detectors
	r.mu.RUnlock()

	var candidates []candidate
	for i, d := range detectors {
		for _, mt := range d.Detect(text) {
			// Ignore out-of-range or empty matches so a buggy detector cannot
			// panic the proxy.
			if mt.Start < 0 || mt.End > len(text) || mt.Start >= mt.End {
				continue
			}
			candidates = append(candidates, candidate{match: mt, order: i})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.match.Start != b.match.Start {
			return a.match.Start < b.match.Start
		}
		la := a.match.End - a.match.Start
		lb := b.match.End - b.match.Start
		if la != lb {
			return la > lb
		}
		return a.order < b.order
	})

	var b strings.Builder
	b.Grow(len(text))

	cursor := 0
	for _, c := range candidates {
		if c.match.Start < cursor {
			// Overlaps an already-accepted match.
			continue
		}
		b.WriteString(text[cursor:c.match.Start])
		b.WriteString(mapping.Add(c.match.Category, text[c.match.Start:c.match.End]))
		cursor = c.match.End
	}
	b.WriteString(text[cursor:])

	return b.String(), mapping
}
