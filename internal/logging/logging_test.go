package logging

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what was
// written to it. Tests using it must not run in parallel: os.Stderr is global.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return string(out)
}

func TestSetup_Levels(t *testing.T) {
	tests := []struct {
		level   string
		debugOn bool
		infoOn  bool
		warnOn  bool
		errorOn bool
	}{
		{"debug", true, true, true, true},
		{"DEBUG", true, true, true, true},
		{" info ", false, true, true, true},
		{"warn", false, false, true, true},
		{"warning", false, false, true, true},
		{"Warning", false, false, true, true},
		{"error", false, false, false, true},
		{"", false, true, true, true},
		{"verbose", false, true, true, true},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			Setup(tt.level)
			l := slog.Default()
			for _, c := range []struct {
				level slog.Level
				want  bool
			}{
				{slog.LevelDebug, tt.debugOn},
				{slog.LevelInfo, tt.infoOn},
				{slog.LevelWarn, tt.warnOn},
				{slog.LevelError, tt.errorOn},
			} {
				if got := l.Enabled(ctx, c.level); got != c.want {
					t.Errorf("level %q: Enabled(%s) = %v, want %v", tt.level, c.level, got, c.want)
				}
			}
		})
	}
}

func TestSetup_WritesJSONToStderr(t *testing.T) {
	out := captureStderr(t, func() {
		Setup("info")
		slog.Info("hello", "key", "value")
		slog.Debug("should be filtered")
	})

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("stderr lines = %d (%q), want 1 (debug must be filtered)", len(lines), out)
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, lines[0])
	}
	if record["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", record["msg"])
	}
	if record["key"] != "value" {
		t.Errorf("key = %v, want value", record["key"])
	}
	if record["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", record["level"])
	}

	// Restore the default logger to a discard sink so later tests in this
	// package (and -v output) are not polluted.
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
}
