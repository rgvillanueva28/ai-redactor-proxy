package detectors

import (
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestPersonNameDetect(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "two_words",
			text: "John Smith",
			want: []string{"John Smith"},
		},
		{
			name: "three_words",
			text: "Mary Jane Watson",
			want: []string{"Mary Jane Watson"},
		},
		{
			name: "four_words",
			text: "John Ronald Reuel Tolkien",
			want: []string{"John Ronald Reuel Tolkien"},
		},
		{
			name: "in_sentence",
			text: "I met John Smith yesterday",
			want: []string{"John Smith"},
		},
		{
			name: "two_names",
			text: "John Smith and Mary Jones",
			want: []string{"John Smith", "Mary Jones"},
		},
		{
			name: "stopword_united_states",
			text: "United States",
			want: nil,
		},
		{
			name: "stopword_new_york",
			text: "New York",
			want: nil,
		},
		{
			name: "stopword_prefix_new_york_city",
			text: "New York City",
			want: nil,
		},
		{
			name: "stopword_in_sentence",
			text: "she lives in the United States now",
			want: nil,
		},
		{
			name: "abbreviation",
			text: "DR. SMITH",
			want: nil,
		},
		{
			name: "not_capitalized",
			text: "acme corp",
			want: nil,
		},
		{
			name: "single_word",
			text: "Smith",
			want: nil,
		},
		{
			name: "empty",
			text: "",
			want: nil,
		},
	}

	d := NewPersonName()
	if got := d.Category(); got != core.CategoryPersonName {
		t.Fatalf("Category() = %q, want %q", got, core.CategoryPersonName)
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
				if matches[i].Category != core.CategoryPersonName {
					t.Fatalf("match %d category = %q, want %q", i, matches[i].Category, core.CategoryPersonName)
				}
			}
		})
	}
}

func TestPersonNameStopwordsAreSuppressed(t *testing.T) {
	d := NewPersonName()
	for _, stop := range personNameStopwords {
		if matches := d.Detect(stop); len(matches) != 0 {
			t.Fatalf("Detect(%q) = %v, want no matches", stop, matchValues(matches))
		}
	}
}
