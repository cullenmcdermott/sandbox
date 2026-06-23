package cli

import (
	"io"
	"log/slog"
	"os"
)

// Structured debug logging. When --debug (or SANDBOX_DEBUG) is set, the CLI
// emits JSON-line records to stderr — one object per line, greppable and
// jq-pipeable — so a run can be inspected after the fact. The schema is
// documented in docs/runner-api.md ("Debug logging"); the runner uses the same
// shape so CLI and runner traces interleave consistently.

// debugEnabled is bound to the root --debug persistent flag.
var debugEnabled bool

// debugOut is the sink for debug records (overridable in tests).
var debugOut io.Writer = os.Stderr

// debugLogger starts as a no-op and is replaced by configureDebugLogging when
// debug output is requested.
var debugLogger = slog.New(slog.NewJSONHandler(io.Discard, nil))

// configureDebugLogging installs the JSON-line debug logger when --debug or
// SANDBOX_DEBUG is set, and is a no-op (discard) otherwise. Called from the root
// command's PersistentPreRun so every subcommand honors the flag.
func configureDebugLogging() {
	if !debugEnabled && os.Getenv("SANDBOX_DEBUG") == "" {
		debugLogger = slog.New(slog.NewJSONHandler(io.Discard, nil))
		return
	}
	h := slog.NewJSONHandler(debugOut, &slog.HandlerOptions{Level: slog.LevelDebug})
	debugLogger = slog.New(h).With("component", "cli")
}

// dbg emits a structured debug record. No-op (and cheap) when debug is off.
func dbg(msg string, args ...any) {
	debugLogger.Debug(msg, args...)
}
