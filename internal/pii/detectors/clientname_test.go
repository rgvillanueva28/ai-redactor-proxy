package detectors

import (
	"sync"
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestClientNameMatchesConfiguredNames(t *testing.T) {
	d, err := NewClientName([]string{"Acme Corp", "Globex"})
	if err != nil {
		t.Fatalf("NewClientName: %v", err)
	}
	if got := d.Category(); got != core.CategoryClientName {
		t.Fatalf("Category() = %q, want %q", got, core.CategoryClientName)
	}

	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "exact_case",
			text: "we work with Acme Corp daily",
			want: []string{"Acme Corp"},
		},
		{
			name: "lower_case",
			text: "we work with acme corp daily",
			want: []string{"acme corp"},
		},
		{
			name: "upper_case",
			text: "WE WORK WITH ACME CORP DAILY",
			want: []string{"ACME CORP"},
		},
		{
			name: "mixed_case",
			text: "AcmE CoRp",
			want: []string{"AcmE CoRp"},
		},
		{
			name: "single_word_name",
			text: "globex is a client",
			want: []string{"globex"},
		},
		{
			name: "several_occurrences",
			text: "Globex, then ACME CORP, then Globex",
			want: []string{"Globex", "ACME CORP", "Globex"},
		},
		{
			name: "unconfigured_name",
			text: "initech is not configured",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matches := d.Detect(tc.text)
			if len(matches) != len(tc.want) {
				t.Fatalf("Detect(%q) = %v, want %v", tc.text, matchValues(matches), tc.want)
			}
			for i, want := range tc.want {
				if matches[i].Value != want {
					t.Fatalf("match %d value = %q, want %q", i, matches[i].Value, want)
				}
				if got := tc.text[matches[i].Start:matches[i].End]; got != want {
					t.Fatalf("match %d spans %q, want %q", i, got, want)
				}
				if matches[i].Category != core.CategoryClientName {
					t.Fatalf("match %d category = %q, want %q", i, matches[i].Category, core.CategoryClientName)
				}
			}
		})
	}
}

func TestClientNameEmptyListNeverMatches(t *testing.T) {
	for _, names := range [][]string{nil, {}, {"", "   "}} {
		d, err := NewClientName(names)
		if err != nil {
			t.Fatalf("NewClientName(%v): %v", names, err)
		}
		if matches := d.Detect("Acme Corp and Globex"); len(matches) != 0 {
			t.Fatalf("Detect with names %v = %v, want no matches", names, matchValues(matches))
		}
	}
}

func TestClientNameSpecialCharactersAreLiteral(t *testing.T) {
	names := []string{"Acme (Corp)+ Ltd.", "a.b*c"}
	d, err := NewClientName(names)
	if err != nil {
		t.Fatalf("NewClientName: %v", err)
	}

	text := "signed by Acme (Corp)+ Ltd. and a.b*c"
	matches := d.Detect(text)
	want := names
	if len(matches) != len(want) {
		t.Fatalf("Detect(%q) = %v, want %v", text, matchValues(matches), want)
	}
	for i := range want {
		if matches[i].Value != want[i] {
			t.Fatalf("match %d = %q, want %q", i, matches[i].Value, want[i])
		}
	}
}

func TestClientNameConcurrentDetect(t *testing.T) {
	d, err := NewClientName([]string{"Acme Corp", "Globex"})
	if err != nil {
		t.Fatalf("NewClientName: %v", err)
	}

	text := "Globex and ACME CORP are clients"
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if matches := d.Detect(text); len(matches) != 2 {
					t.Errorf("Detect returned %d matches, want 2", len(matches))
					return
				}
			}
		}()
	}
	wg.Wait()
}
