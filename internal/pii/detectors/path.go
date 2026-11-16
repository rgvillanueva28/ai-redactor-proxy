package detectors

import (
	"regexp"

	"github.com/local/redactor/internal/pii/core"
)

const (
	// windowsPathPattern matches a drive-qualified Windows path, e.g.
	// `C:\Users\foo\Documents\file.txt`.
	windowsPathPattern = `[A-Za-z]:\\(?:[^\\\s]+\\)*[^\\\s]+`
	// uncPathPattern matches a UNC share, e.g. `\\server\share\dir\file`.
	uncPathPattern = `\\\\[^\s\\]+\\[^\s\\]+(?:\\[^\s\\]+)*`
	// unixPathPattern matches absolute Unix paths below commonly sensitive
	// roots. Restricting the prefix keeps false positives down: a bare
	// "/just/a/slash/path" is not treated as PII.
	unixPathPattern = `(?:/home/|/Users/|/root/|/tmp/|/var/|/etc/)[^\s]+`
)

// Path detects filesystem paths that leak usernames or project layout.
type Path struct {
	patterns []*regexp.Regexp
}

// NewPath returns a Path detector with all patterns compiled once.
func NewPath() *Path {
	return &Path{patterns: []*regexp.Regexp{
		regexp.MustCompile(windowsPathPattern),
		regexp.MustCompile(uncPathPattern),
		regexp.MustCompile(unixPathPattern),
	}}
}

// Category implements core.Detector.
func (d *Path) Category() core.Category { return core.CategoryPath }

// Detect returns every filesystem path in text, in document order.
func (d *Path) Detect(text string) []core.Match {
	var matches []core.Match
	for _, re := range d.patterns {
		for _, loc := range re.FindAllStringIndex(text, -1) {
			matches = append(matches, core.Match{
				Start:    loc[0],
				End:      loc[1],
				Category: core.CategoryPath,
				Value:    text[loc[0]:loc[1]],
			})
		}
	}
	return sortAndDedupe(matches)
}
