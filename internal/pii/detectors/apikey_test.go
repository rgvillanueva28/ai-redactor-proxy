package detectors

import (
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestAPIKeyDetector(t *testing.T) {
	d := NewAPIKey()
	if got, want := d.Category(), core.CategoryAPIKey; got != want {
		t.Fatalf("Category() = %q, want %q", got, want)
	}

	positives := []string{
		"sk-proj-abc123def456ghi789jkl012mno345pqr678",
		"sk-ant-api02-abc123def456ghi789jkl012mno345pqr678",
		"ghp_ABCDEFghijklmnop1234567890abcdef",
		"AKIA1234567890ABCDEF",
		"xoxb-123456789012-1234567890123-ABCDEFghijklmnop123456",
	}
	for _, tc := range positives {
		t.Run("positive/"+tc, func(t *testing.T) {
			matches := d.Detect(tc)
			if len(matches) != 1 {
				t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", tc, len(matches), matches)
			}
			m := matches[0]
			if m.Category != core.CategoryAPIKey {
				t.Fatalf("category = %q, want %q", m.Category, core.CategoryAPIKey)
			}
			if m.Value != tc {
				t.Fatalf("value = %q, want %q", m.Value, tc)
			}
		})
	}

	negatives := []string{
		"sk-",         // prefix with no body
		"password123", // no recognized prefix
		"ghp_short",   // prefix but body too short
		"AKIA1234",    // prefix but body too short
	}
	for _, tc := range negatives {
		t.Run("negative/"+tc, func(t *testing.T) {
			if matches := d.Detect(tc); len(matches) != 0 {
				t.Fatalf("Detect(%q) = %v, want no matches", tc, matches)
			}
		})
	}
}

func TestAPIKeyDetectorEmbedded(t *testing.T) {
	d := NewAPIKey()

	text := "export OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl012mno345pqr678 done"
	matches := d.Detect(text)
	if len(matches) != 1 {
		t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", text, len(matches), matches)
	}
	if want := "sk-proj-abc123def456ghi789jkl012mno345pqr678"; matches[0].Value != want {
		t.Fatalf("value = %q, want %q", matches[0].Value, want)
	}
}
