// Package logger centralizes structured logging for the FinTrak backend. It
// builds an slog.Logger whose level and format follow the runtime environment:
// development defaults to debug level with human-friendly text output (so HTTP
// request/response bodies are captured), while production uses JSON output at
// info level.
package logger

import (
	"log"
	"log/slog"
	"os"
	"strings"
)

// New configures the process-wide slog.Logger for the given environment and
// log level, returns it, and routes the standard library logger (used by the
// db package, handlers, and seeders) through it so every existing log line
// becomes a structured record.
//
// env is "development" or "production" and only selects the output format
// (text vs JSON). level is one of "debug", "info", "warn", "error"; when empty
// it defaults to "debug" for development and "info" for production.
func New(env, level string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level, env)}
	var handler slog.Handler
	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	l := slog.New(handler)
	slog.SetDefault(l)

	// Route legacy log.* call sites (db package, handlers, seeders) through
	// slog so all output is structured and level-aware.
	log.SetOutput(logBridge{})

	return l
}

// parseLevel maps a LOG_LEVEL value to a slog.Level, falling back to the
// environment default when the value is empty or unrecognized.
func parseLevel(level, env string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info":
		return slog.LevelInfo
	default:
		if env == "production" {
			return slog.LevelInfo
		}
		return slog.LevelDebug
	}
}

// logBridge adapts the stdlib logger to slog so existing log.Print*/Fatal*
// call sites emit structured records instead of plain text.
type logBridge struct{}

func (logBridge) Write(p []byte) (int, error) {
	slog.Info(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// SetMaxBodyLog overrides the byte cap for request/response bodies written to
// the log at debug level. A value <= 0 disables truncation so full bodies are
// captured; the default (when unset) is 8192.
func SetMaxBodyLog(n int) {
	maxBodyLog = n
}