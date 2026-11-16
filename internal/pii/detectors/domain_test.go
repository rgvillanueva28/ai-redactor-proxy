package detectors

import (
	"strings"
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestDomainDetectRegistrableDomain(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string // expected match values, in document order
	}{
		{
			name: "subdomain_prefix_preserved",
			text: "api.acme.com",
			want: []string{"acme.com"},
		},
		{
			name: "multi_part_tld",
			text: "acme.co.uk",
			want: []string{"acme.co.uk"},
		},
		{
			name: "deep_subdomain",
			text: "sub.api.example.org",
			want: []string{"example.org"},
		},
		{
			name: "bare_registrable_domain",
			text: "connect to acme.com now",
			want: []string{"acme.com"},
		},
		{
			name: "multi_part_tld_with_subdomain",
			text: "www.acme.co.uk",
			want: []string{"acme.co.uk"},
		},
		{
			name: "case_preserved",
			text: "Api.Acme.Com",
			want: []string{"Acme.Com"},
		},
		{
			name: "email_not_matched",
			text: "foo@bar.com",
			want: nil,
		},
		{
			name: "email_with_subdomain_not_matched",
			text: "foo@mail.acme.com",
			want: nil,
		},
		{
			name: "bare_tld_rejected",
			text: "the com tld",
			want: nil,
		},
		{
			name: "hostname_without_dot_rejected",
			text: "localhost",
			want: nil,
		},
		{
			name: "hyphenated_word_rejected",
			text: "just-a-word",
			want: nil,
		},
		{
			name: "file_name_not_a_domain",
			text: "open main.go now",
			want: nil,
		},
		{
			name: "ip_address_rejected",
			text: "host 10.0.0.1 now",
			want: nil,
		},
		{
			name: "several_domains",
			text: "a.acme.com then globex.co.uk",
			want: []string{"acme.com", "globex.co.uk"},
		},
	}

	d := NewDomain()
	if got := d.Category(); got != core.CategoryDomain {
		t.Fatalf("Category() = %q, want %q", got, core.CategoryDomain)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matches := d.Detect(tc.text)
			if len(matches) != len(tc.want) {
				t.Fatalf("Detect(%q) returned %d matches (%v), want %d (%v)",
					tc.text, len(matches), matchValues(matches), len(tc.want), tc.want)
			}
			for i, want := range tc.want {
				if matches[i].Value != want {
					t.Fatalf("match %d value = %q, want %q", i, matches[i].Value, want)
				}
				if matches[i].Category != core.CategoryDomain {
					t.Fatalf("match %d category = %q, want %q", i, matches[i].Category, core.CategoryDomain)
				}
				// Offsets must point at the reported value.
				if got := tc.text[matches[i].Start:matches[i].End]; got != want {
					t.Fatalf("match %d spans %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestDomainDetectOffsetsSkipSubdomain(t *testing.T) {
	text := "call api.acme.com now"
	matches := NewDomain().Detect(text)
	if len(matches) != 1 {
		t.Fatalf("got %d matches (%v), want 1", len(matches), matchValues(matches))
	}
	wantStart := strings.Index(text, "acme.com")
	if matches[0].Start != wantStart || matches[0].End != wantStart+len("acme.com") {
		t.Fatalf("match = [%d,%d), want [%d,%d)", matches[0].Start, matches[0].End, wantStart, wantStart+len("acme.com"))
	}
}

// matchValues extracts the Value of each match, for test failure messages.
func matchValues(matches []core.Match) []string {
	values := make([]string, len(matches))
	for i, m := range matches {
		values[i] = m.Value
	}
	return values
}
