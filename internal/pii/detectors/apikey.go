package detectors

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/local/redactor/internal/pii/core"
)

// apiKeySpec describes one credential prefix and how much trailing material is
// required before the match is trusted.
type apiKeySpec struct {
	prefix  string // regex fragment, already anchored via \b by the compiler
	minBody int    // minimum number of credential characters after the prefix
}

// apiKeySpecs lists the recognized credential prefixes. Longer prefixes come
// first so that "sk-ant-…" is preferred over the generic "sk-…" prefix; Go's
// regexp engine resolves a shared start position with leftmost-first semantics,
// so this order is load-bearing for the compiled alternation.
var apiKeySpecs = []apiKeySpec{
	{prefix: `sk-proj-`, minBody: 20},    // OpenAI project key
	{prefix: `sk-ant-`, minBody: 20},     // Anthropic API key
	{prefix: `glpat-`, minBody: 20},      // GitLab personal access token
	{prefix: `xox[baprs]-`, minBody: 16}, // Slack token
	{prefix: `ghp_`, minBody: 20},        // GitHub personal access token
	{prefix: `gho_`, minBody: 20},        // GitHub OAuth token
	{prefix: `ghu_`, minBody: 20},        // GitHub user-to-server token
	{prefix: `ghs_`, minBody: 20},        // GitHub server-to-server token
	{prefix: `ghr_`, minBody: 20},        // GitHub refresh token
	{prefix: `pat_`, minBody: 20},        // generic / GitHub PAT form
	{prefix: `AKIA`, minBody: 16},        // AWS access key id (20 chars total)
	{prefix: `sk-`, minBody: 20},         // generic "sk-" style secret key
}

// apiKeyPattern is every prefix spec collapsed into one alternation. Compiling
// it once keeps the hot path to a single FindAllStringIndex pass instead of one
// full-document scan per prefix.
var apiKeyPattern = compileAPIKeyPattern(apiKeySpecs)

// compileAPIKeyPattern builds `\b(?:prefix[body]{n,}|…)`. Each branch keeps the
// exact prefix, charset and minimum body length of the spec it came from.
func compileAPIKeyPattern(specs []apiKeySpec) *regexp.Regexp {
	branches := make([]string, 0, len(specs))
	for _, spec := range specs {
		branches = append(branches, `(?:`+spec.prefix+`)[A-Za-z0-9_\-]{`+
			strconv.Itoa(spec.minBody)+`,}`)
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(branches, "|") + `)`)
}

// APIKey detects provider API keys and access tokens by prefix plus a minimum
// body length, so that bare fragments such as "sk-" are not redacted.
type APIKey struct {
	pattern *regexp.Regexp
}

// NewAPIKey returns an APIKey detector backed by the package-level compiled
// alternation.
func NewAPIKey() *APIKey {
	return &APIKey{pattern: apiKeyPattern}
}

// Category implements core.Detector.
func (d *APIKey) Category() core.Category { return core.CategoryAPIKey }

// Detect returns every API key in text, in document order.
func (d *APIKey) Detect(text string) []core.Match {
	locs := d.pattern.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}

	// FindAllStringIndex already yields leftmost, non-overlapping matches in
	// document order, so no sort or de-duplication pass is needed.
	matches := make([]core.Match, len(locs))
	for i, loc := range locs {
		matches[i] = core.Match{
			Start:    loc[0],
			End:      loc[1],
			Category: core.CategoryAPIKey,
			Value:    text[loc[0]:loc[1]],
		}
	}
	return matches
}
