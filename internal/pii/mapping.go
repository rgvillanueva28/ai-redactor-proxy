package pii

import (
	"strconv"
	"sync"
)

// Mapping stores placeholder -> original value for a single request.
//
// A Mapping is owned by the caller for the lifetime of one request. It is not
// safe for concurrent use; a request should keep its mapping on a single
// goroutine (or guard it externally).
type Mapping struct {
	entries map[string]string // placeholder -> original
	// per-category counters to generate <CATEGORY_N>
	counters map[Category]int
}

// NewMapping returns an empty, ready-to-use Mapping.
func NewMapping() *Mapping {
	return &Mapping{
		entries:  make(map[string]string, 16),
		counters: make(map[Category]int, 10),
	}
}

// Reset clears the mapping so it can be reused for another request. The
// backing maps are retained (via clear) so that repeated request cycles do not
// reallocate.
func (m *Mapping) Reset() {
	if m == nil {
		return
	}
	if m.entries == nil {
		m.entries = make(map[string]string, 16)
	} else {
		clear(m.entries)
	}
	if m.counters == nil {
		m.counters = make(map[Category]int, 10)
	} else {
		clear(m.counters)
	}
}

// Add registers original under a fresh placeholder for cat and returns the
// placeholder, e.g. "<EMAIL_1>". Counters are per-category and start at 1 for a
// freshly reset Mapping.
func (m *Mapping) Add(cat Category, original string) string {
	if m.entries == nil {
		m.entries = make(map[string]string, 16)
	}
	if m.counters == nil {
		m.counters = make(map[Category]int, 10)
	}
	m.counters[cat]++
	placeholder := "<" + string(cat) + "_" + strconv.Itoa(m.counters[cat]) + ">"
	m.entries[placeholder] = original
	return placeholder
}

// Lookup returns the original value registered for placeholder.
func (m *Mapping) Lookup(placeholder string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m.entries[placeholder]
	return v, ok
}

// All returns a copy of the placeholder -> original map.
func (m *Mapping) All() map[string]string {
	out := make(map[string]string, 0)
	if m == nil {
		return out
	}
	for k, v := range m.entries {
		out[k] = v
	}
	return out
}

// mappingPool reuses Mapping values across requests.
var mappingPool = sync.Pool{New: func() any { return NewMapping() }}

// GetMapping returns a reset Mapping from the pool.
func GetMapping() *Mapping {
	m := mappingPool.Get().(*Mapping)
	m.Reset()
	return m
}

// PutMapping resets m and returns it to the pool.
func PutMapping(m *Mapping) {
	if m == nil {
		return
	}
	m.Reset()
	mappingPool.Put(m)
}
