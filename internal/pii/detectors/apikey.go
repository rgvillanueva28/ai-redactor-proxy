package detectors

import (
	"regexp"
	"strconv"

	"github.com/local/redactor/internal/pii/core"
)

// apiKeySpec describes one credential prefix and how much trailing material is
// required before the match is trusted.
type apiKeySpec struct {
	prefix  string // regex fragment, already anchored via \b by the caller
	minBody int    // minimum number of credential characters after the prefix
}

// apiKeySpecs lists the recognized credential prefixes. Longer prefixes come
// first so that "sk-ant-…" is preferred over the generic "sk-…" prefix; the
// final de-duplication keeps a single match per token either way.
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

// APIKey detects provider API keys and access tokens by prefix plus a minimum
// body length, so that bare fragments such as "sk-" are not redacted.
type APIKey struct {
	patterns []*regexp.Regexp
}

// NewAPIKey returns an APIKey detector with every prefix pattern compiled once.
func NewAPIKey() *APIKey {
	patterns := make([]*regexp.Regexp, 0, len(apiKeySpecs))
	for _, spec := range apiKeySpecs {
		patterns = append(patterns, regexp.MustCompile(
			`\b(?:`+spec.prefix+`)[A-Za-z0-9_\-]{`+strconv.Itoa(spec.minBody)+`,}`,
		))
	}
	return &APIKey{patterns: patterns}
}

// Category implements core.Detector.
func (d *APIKey) Category() core.Category { return core.CategoryAPIKey }

// Detect returns every API key in text, in document order.
func (d *APIKey) Detect(text string) []core.Match {
	var matches []core.Match
	for _, re := range d.patterns {
		for _, loc := range re.FindAllStringIndex(text, -1) {
			matches = append(matches, core.Match{
				Start:    loc[0],
				End:      loc[1],
				Category: core.CategoryAPIKey,
				Value:    text[loc[0]:loc[1]],
			})
		}
	}
	return sortAndDedupe(matches)
}
