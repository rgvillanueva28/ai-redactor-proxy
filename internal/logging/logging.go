// Package logging configures the process-wide structured logger.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Setup initializes the default slog logger at the given level.
//
// Logs are emitted as JSON on stderr so that stdout stays free for the proxy's
// own output and for pipelines that consume it. level is case-insensitive:
// "debug", "info", "warn" or "error" ("warning" is accepted as a synonym for
// "warn"). Unknown or empty values fall back to "info" rather than failing, so
// a typo in the config file cannot stop the proxy from starting.
func Setup(level string) {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(h))
}
