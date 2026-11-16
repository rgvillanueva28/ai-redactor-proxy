package detectors

import (
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestEmailDetector(t *testing.T) {
	d := NewEmail()
	if got, want := d.Category(), core.CategoryEmail; got != want {
		t.Fatalf("Category() = %q, want %q", got, want)
	}

	positives := []string{
		"foo@bar.com",
		"user.name+tag@sub.example.co.uk",
		"a@b.co",
		"foo_bar@example.org",
		"first.last%test@example.io",
	}
	for _, tc := range positives {
		t.Run("positive/"+tc, func(t *testing.T) {
			matches := d.Detect(tc)
			if len(matches) != 1 {
				t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", tc, len(matches), matches)
			}
			m := matches[0]
			if m.Category != core.CategoryEmail {
				t.Fatalf("category = %q, want %q", m.Category, core.CategoryEmail)
			}
			if m.Value != tc {
				t.Fatalf("value = %q, want %q", m.Value, tc)
			}
			if got := tc[m.Start:m.End]; got != tc {
				t.Fatalf("text[Start:End] = %q, want %q", got, tc)
			}
		})
	}

	negatives := []string{
		"not-an-email",
		"@missing.com",
		"foo@",
		"foo@.com",
	}
	for _, tc := range negatives {
		t.Run("negative/"+tc, func(t *testing.T) {
			if matches := d.Detect(tc); len(matches) != 0 {
				t.Fatalf("Detect(%q) = %v, want no matches", tc, matches)
			}
		})
	}
}

func TestEmailDetectorMultipleAndEmbedded(t *testing.T) {
	d := NewEmail()

	text := "mail alice@example.com or bob@sub.example.org, not foo@"
	matches := d.Detect(text)
	if len(matches) != 2 {
		t.Fatalf("Detect(%q) returned %d matches, want 2 (%v)", text, len(matches), matches)
	}
	want := []string{"alice@example.com", "bob@sub.example.org"}
	for i, w := range want {
		if matches[i].Value != w {
			t.Fatalf("match %d = %q, want %q", i, matches[i].Value, w)
		}
		if matches[i].Start >= matches[i].End {
			t.Fatalf("match %d has invalid offsets: %+v", i, matches[i])
		}
	}
}
