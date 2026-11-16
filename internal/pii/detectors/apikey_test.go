package detectors

import (
	"strings"
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

// TestAPIKeyEveryPrefixMatchesAtMinimumBody guards the single-alternation
// build: every spec in apiKeySpecs must still fire at exactly its minimum body
// length, and the match must cover the whole token (not a shorter generic
// prefix branch).
func TestAPIKeyEveryPrefixMatchesAtMinimumBody(t *testing.T) {
	d := NewAPIKey()

	for _, spec := range apiKeySpecs {
		// apiKeySpecs stores regex fragments; xox[baprs]- is the one prefix
		// that is not already a literal, so pick a concrete Slack variant.
		prefix := spec.prefix
		if prefix == `xox[baprs]-` {
			prefix = "xoxb-"
		}
		token := prefix + strings.Repeat("a", spec.minBody)

		t.Run(token, func(t *testing.T) {
			matches := d.Detect(token)
			if len(matches) != 1 {
				t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", token, len(matches), matches)
			}
			if matches[0].Start != 0 || matches[0].End != len(token) || matches[0].Value != token {
				t.Fatalf("Detect(%q) = %v, want one match covering the whole token", token, matches[0])
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
