package pii

import (
	"fmt"
	"sync"
	"testing"
)

func TestMappingAddGeneratesPerCategoryPlaceholders(t *testing.T) {
	m := NewMapping()

	if got := m.Add(CategoryEmail, "a@example.com"); got != "<EMAIL_1>" {
		t.Fatalf("first email placeholder = %q, want <EMAIL_1>", got)
	}
	if got := m.Add(CategoryEmail, "b@example.com"); got != "<EMAIL_2>" {
		t.Fatalf("second email placeholder = %q, want <EMAIL_2>", got)
	}
	if got := m.Add(CategoryPhone, "+15551234567"); got != "<PHONE_1>" {
		t.Fatalf("first phone placeholder = %q, want <PHONE_1>", got)
	}
	if got := m.Add(CategoryCreditCard, "4111111111111111"); got != "<CC_1>" {
		t.Fatalf("first cc placeholder = %q, want <CC_1>", got)
	}
	if got := m.Add(CategoryEmail, "c@example.com"); got != "<EMAIL_3>" {
		t.Fatalf("third email placeholder = %q, want <EMAIL_3>", got)
	}
}

func TestMappingDeduplicatesSameCategoryAndValue(t *testing.T) {
	m := NewMapping()

	first := m.Add(CategoryEmail, "alice@example.com")
	if first != "<EMAIL_1>" {
		t.Fatalf("first add = %q, want <EMAIL_1>", first)
	}

	// Same (category, original) pair returns the same placeholder.
	for i := 0; i < 5; i++ {
		if got := m.Add(CategoryEmail, "alice@example.com"); got != first {
			t.Fatalf("dup add %d = %q, want %q", i, got, first)
		}
	}

	// The duplicate adds must not have advanced the counter.
	if got := m.Add(CategoryEmail, "bob@example.com"); got != "<EMAIL_2>" {
		t.Fatalf("add after duplicates = %q, want <EMAIL_2>", got)
	}

	// The same value under a different category gets its own placeholder.
	if got := m.Add(CategoryCreditCard, "alice@example.com"); got != "<CC_1>" {
		t.Fatalf("same value, different category = %q, want <CC_1>", got)
	}

	// Only the two distinct emails plus the CC entry are registered.
	if got := len(m.All()); got != 3 {
		t.Fatalf("All() length = %d, want 3", got)
	}

	// Reset clears the reverse index so a subsequent add is fresh again.
	m.Reset()
	if got := m.Add(CategoryEmail, "alice@example.com"); got != "<EMAIL_1>" {
		t.Fatalf("add after Reset = %q, want <EMAIL_1>", got)
	}
}

func TestMappingAddReplacement(t *testing.T) {
	m := NewMapping()

	first := m.AddReplacement(CategoryEmail, "[EMAIL]", "alice@example.com")
	if first != "[EMAIL]" {
		t.Fatalf("first replacement = %q, want [EMAIL]", first)
	}
	if got, ok := m.Lookup("[EMAIL]"); !ok || got != "alice@example.com" {
		t.Fatalf("Lookup([EMAIL]) = (%q, %v), want (alice@example.com, true)", got, ok)
	}

	// A duplicate (category, original) pair returns the same replacement
	// without touching the mapping.
	if got := m.AddReplacement(CategoryEmail, "[EMAIL]", "alice@example.com"); got != first {
		t.Fatalf("duplicate replacement = %q, want %q", got, first)
	}
	if n := len(m.All()); n != 1 {
		t.Fatalf("mapping length = %d, want 1", n)
	}

	// Custom replacements never consume placeholder counters.
	if got := m.Add(CategoryEmail, "bob@example.com"); got != "<EMAIL_1>" {
		t.Fatalf("placeholder after custom add = %q, want <EMAIL_1>", got)
	}

	// A different category using the same replacement text reuses the entry,
	// keeping the most recently registered original.
	if got := m.AddReplacement(CategoryIP, "[EMAIL]", "10.0.0.1"); got != "[EMAIL]" {
		t.Fatalf("cross-category replacement = %q, want [EMAIL]", got)
	}
	if got, ok := m.Lookup("[EMAIL]"); !ok || got != "10.0.0.1" {
		t.Fatalf("Lookup([EMAIL]) after category reuse = (%q, %v), want (10.0.0.1, true)", got, ok)
	}
}

func TestMappingLookupRoundTrip(t *testing.T) {
	m := NewMapping()

	values := map[Category][]string{
		CategoryEmail: {"alice@example.com", "bob@example.com"},
		CategoryIP:    {"10.0.0.1"},
		CategoryPath:  {`C:\Users\alice\secret.txt`},
	}

	var placeholders []string
	for cat, originals := range values {
		for _, original := range originals {
			placeholders = append(placeholders, m.Add(cat, original))
		}
	}

	if len(m.All()) != 4 {
		t.Fatalf("All() length = %d, want 4", len(m.All()))
	}
	for _, ph := range placeholders {
		if _, ok := m.Lookup(ph); !ok {
			t.Fatalf("Lookup(%q) missing", ph)
		}
	}

	got, ok := m.Lookup("<EMAIL_2>")
	if !ok || got != "bob@example.com" {
		t.Fatalf("Lookup(<EMAIL_2>) = (%q, %v), want (bob@example.com, true)", got, ok)
	}
	if _, ok := m.Lookup("<EMAIL_99>"); ok {
		t.Fatal("Lookup(<EMAIL_99>) unexpectedly found")
	}
}

func TestMappingAllReturnsCopy(t *testing.T) {
	m := NewMapping()
	m.Add(CategoryEmail, "alice@example.com")

	all := m.All()
	delete(all, "<EMAIL_1>")

	if _, ok := m.Lookup("<EMAIL_1>"); !ok {
		t.Fatal("mutating All() copy affected the Mapping")
	}
}

func TestMappingReset(t *testing.T) {
	m := NewMapping()
	ph := m.Add(CategoryEmail, "alice@example.com")

	m.Reset()

	if _, ok := m.Lookup(ph); ok {
		t.Fatalf("Lookup(%q) succeeded after Reset", ph)
	}
	if got := len(m.All()); got != 0 {
		t.Fatalf("All() length after Reset = %d, want 0", got)
	}
	if got := m.Add(CategoryEmail, "bob@example.com"); got != "<EMAIL_1>" {
		t.Fatalf("placeholder after Reset = %q, want <EMAIL_1>", got)
	}
}

// TestResetPreservesBackingMaps verifies Reset reuses the allocated maps
// instead of replacing them with fresh ones.
func TestResetPreservesBackingMaps(t *testing.T) {
	m := NewMapping()
	for i := 0; i < 64; i++ {
		m.Add(CategoryEmail, fmt.Sprintf("user%d@example.com", i))
	}

	entriesBefore := fmt.Sprintf("%p", m.entries)
	countersBefore := fmt.Sprintf("%p", m.counters)
	assignedBefore := fmt.Sprintf("%p", m.assigned)

	m.Reset()
	if m.entries == nil || m.counters == nil || m.assigned == nil {
		t.Fatal("Reset nilled a backing map; allocations were not preserved")
	}

	if got := fmt.Sprintf("%p", m.entries); got != entriesBefore {
		t.Fatalf("entries map reallocated on Reset: %s -> %s", entriesBefore, got)
	}
	if got := fmt.Sprintf("%p", m.counters); got != countersBefore {
		t.Fatalf("counters map reallocated on Reset: %s -> %s", countersBefore, got)
	}
	if got := fmt.Sprintf("%p", m.assigned); got != assignedBefore {
		t.Fatalf("assigned map reallocated on Reset: %s -> %s", assignedBefore, got)
	}
}

func TestMappingPoolGetPutCycle(t *testing.T) {
	for i := 0; i < 100; i++ {
		m := GetMapping()
		if got := len(m.All()); got != 0 {
			t.Fatalf("iteration %d: pool mapping not empty: %d entries", i, got)
		}
		ph := m.Add(CategoryEmail, "alice@example.com")
		if ph != "<EMAIL_1>" {
			t.Fatalf("iteration %d: placeholder = %q, want <EMAIL_1>", i, ph)
		}
		PutMapping(m)
	}

	// PutMapping must tolerate nil.
	PutMapping(nil)
}

// BenchmarkMappingPool measures rapid GetMapping/Add/PutMapping cycles against
// an unpooled NewMapping baseline, so `-benchmem` shows the allocation
// reduction the pool buys. Steady-state pooled cycles reuse the Mapping and
// both backing maps, leaving only the placeholder strings as garbage.
func BenchmarkMappingPool(b *testing.B) {
	cycle := func(m *Mapping) {
		m.Add(CategoryEmail, "alice@example.com")
		m.Add(CategoryIP, "10.0.0.1")
	}

	b.Run("pooled", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m := GetMapping()
			cycle(m)
			PutMapping(m)
		}
	})

	b.Run("unpooled", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m := NewMapping()
			cycle(m)
			// Mirror PutMapping's reset so the only difference is pooling.
			m.Reset()
		}
	})
}

// TestMappingPoolReducesAllocations asserts the pool's reason for existing:
// a pooled request cycle allocates strictly fewer objects than a fresh
// Mapping. BenchmarkMappingPool reports the same difference in allocs/op.
func TestMappingPoolReducesAllocations(t *testing.T) {
	cycle := func(m *Mapping) {
		m.Add(CategoryEmail, "alice@example.com")
		m.Add(CategoryIP, "10.0.0.1")
	}

	pooled := testing.AllocsPerRun(500, func() {
		m := GetMapping()
		cycle(m)
		PutMapping(m)
	})
	unpooled := testing.AllocsPerRun(500, func() {
		cycle(NewMapping())
	})

	if pooled >= unpooled {
		t.Fatalf("pooled cycle allocated %.1f objects/op, unpooled %.1f; want pooling to allocate fewer",
			pooled, unpooled)
	}
}

// TestMappingPoolConcurrent exercises the pool from many goroutines; run with
// -race to detect unsafe reuse.
func TestMappingPoolConcurrent(t *testing.T) {
	const goroutines = 50
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				original := fmt.Sprintf("user%d-%d@example.com", g, i)
				m := GetMapping()

				ph := m.Add(CategoryEmail, original)
				if got, ok := m.Lookup(ph); !ok || got != original {
					t.Errorf("goroutine %d: Lookup(%q) = (%q, %v), want (%q, true)", g, ph, got, ok, original)
					PutMapping(m)
					return
				}

				// Interleave a second category to exercise independent counters.
				ph2 := m.Add(CategoryIP, "10.0.0.1")
				if ph2 != "<IP_1>" {
					t.Errorf("goroutine %d: ip placeholder = %q, want <IP_1>", g, ph2)
					PutMapping(m)
					return
				}

				if got := len(m.All()); got != 2 {
					t.Errorf("goroutine %d: mapping length = %d, want 2", g, got)
				}

				PutMapping(m)
			}
		}(g)
	}
	wg.Wait()
}
