package detectors

import (
	"testing"

	"github.com/local/redactor/internal/pii/core"
)

func TestPathDetector(t *testing.T) {
	d := NewPath()
	if got, want := d.Category(), core.CategoryPath; got != want {
		t.Fatalf("Category() = %q, want %q", got, want)
	}

	positives := []struct {
		name string
		text string
	}{
		{"windows", `C:\Users\foo\Documents\file.txt`},
		{"unc", `\\server\share\dir\file`},
		{"unix_home", `/home/alice/project/main.go`},
		{"unix_users", `/Users/bob/Desktop/x.pdf`},
		{"unix_var", `/var/log/redactor/proxy.log`},
		{"unix_tmp", `/tmp/session-cache.json`},
	}
	for _, tc := range positives {
		t.Run("positive/"+tc.name, func(t *testing.T) {
			matches := d.Detect(tc.text)
			if len(matches) != 1 {
				t.Fatalf("Detect(%q) returned %d matches, want 1 (%v)", tc.text, len(matches), matches)
			}
			m := matches[0]
			if m.Category != core.CategoryPath {
				t.Fatalf("category = %q, want %q", m.Category, core.CategoryPath)
			}
			if m.Value != tc.text {
				t.Fatalf("value = %q, want %q", m.Value, tc.text)
			}
		})
	}

	negatives := []struct {
		name string
		text string
	}{
		{"non_sensitive_unix", `/just/a/slash/path`},
		{"relative", `relative/path/file`},
		{"bare_windows_drive", `C:file.txt`},
	}
	for _, tc := range negatives {
		t.Run("negative/"+tc.name, func(t *testing.T) {
			if matches := d.Detect(tc.text); len(matches) != 0 {
				t.Fatalf("Detect(%q) = %v, want no matches", tc.text, matches)
			}
		})
	}
}

func TestPathDetectorEmbedded(t *testing.T) {
	d := NewPath()

	text := `open C:\Users\foo\file.txt and /home/alice/x.go`
	matches := d.Detect(text)
	if len(matches) != 2 {
		t.Fatalf("Detect(%q) returned %d matches, want 2 (%v)", text, len(matches), matches)
	}
	want := []string{`C:\Users\foo\file.txt`, `/home/alice/x.go`}
	for i, w := range want {
		if matches[i].Value != w {
			t.Fatalf("match %d = %q, want %q", i, matches[i].Value, w)
		}
	}
}
