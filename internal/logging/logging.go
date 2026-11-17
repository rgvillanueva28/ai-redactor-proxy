// Package logging configures the process-wide structured logger and exposes the
// resulting log stream to the browser.
package logging

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// defaultBroadcaster fans log records out to SSE clients. It exists from
// package initialization so an HTTP endpoint can be mounted before Setup runs.
var defaultBroadcaster = NewBroadcaster(DefaultBufferSize)

// Setup initializes the default slog logger at the given level.
//
// Logs are emitted as JSON on stderr so that stdout stays free for the proxy's
// own output and for pipelines that consume it. level is case-insensitive:
// "debug", "info", "warn" or "error" ("warning" is accepted as a synonym for
// "warn"). Unknown or empty values fall back to "info" rather than failing, so
// a typo in the config file cannot stop the proxy from starting.
//
// Every record is also broadcast to subscribers of DefaultBroadcaster, which is
// what the /logs stream serves. Broadcasting never blocks the logger.
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
	h := NewBroadcastHandler(os.Stderr, lvl, defaultBroadcaster)
	slog.SetDefault(slog.New(h))
}

// DefaultBroadcaster returns the process-wide broadcaster installed in the
// default logger by Setup. It is never nil.
func DefaultBroadcaster() *Broadcaster {
	return defaultBroadcaster
}

// SSEHandler returns an http.Handler that streams the default logger's records
// to the caller as Server-Sent Events. The handler keeps streaming until the
// client disconnects and never blocks the logger, so it is safe to mount on the
// proxy's own mux.
func SSEHandler() http.Handler {
	return defaultBroadcaster.SSEHandler()
}
