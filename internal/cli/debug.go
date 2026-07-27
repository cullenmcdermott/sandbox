package cli

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cullenmcdermott/sandbox/internal/index"
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
	if !debugActive() {
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

// debugFileSink is the open per-session debug.jsonl, if one was attached. Held
// so the caller can close it; nil when no sink is attached.
var debugFileSink *os.File

// debugSessionLogPath returns the per-session debug log path,
// ~/.local/share/sandbox/remote-sessions/<id>/debug.jsonl.
func debugSessionLogPath(root, sessionID string) string {
	return filepath.Join(root, sessionID, "debug.jsonl")
}

// attachDebugFileSink redirects (or tees) debug records to the session's
// debug.jsonl.
//
// [T1]: --debug writes to stderr, but the dashboard owns the alt-screen, so the
// primary workflow is precisely the one where stderr output is both unreadable
// and actively corrupting. `alsoStderr=false` (what the TUI paths pass) makes the
// file the ONLY sink, which is why a --debug dashboard run now leaves a
// parseable artifact behind instead of a scrambled screen.
//
// Best-effort by design: a debug log that cannot be opened must never break the
// command the user actually asked for, so failures return the error for an
// advisory and leave the existing sink in place. No-op when debug is off.
func attachDebugFileSink(sessionID string, alsoStderr bool) error {
	if !debugActive() || sessionID == "" {
		return nil
	}
	root, err := index.DefaultRoot()
	if err != nil {
		return err
	}
	path := debugSessionLogPath(root, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Append: a session is debugged across many commands (create, attach,
	// suspend), and each truncating the log would leave only the last one.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	debugFileSink = f
	var w io.Writer = f
	if alsoStderr {
		w = io.MultiWriter(debugOut, f)
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	debugLogger = slog.New(h).With("component", "cli", "session", sessionID)
	return nil
}

// closeDebugFileSink closes an attached per-session sink, if any.
func closeDebugFileSink() {
	if debugFileSink != nil {
		_ = debugFileSink.Close()
		debugFileSink = nil
	}
}

// debugActive reports whether debug logging is turned on by flag or env.
func debugActive() bool {
	return debugEnabled || os.Getenv("SANDBOX_DEBUG") != ""
}

// traceEnabledFlag is bound to the root --trace persistent flag. Connect/create
// timing spans (§10 observability) live in the public client package, gated on
// the SANDBOX_TRACE env var so the library has a single, dependency-free switch.
// The CLI flag is sugar over that env var: configureTracing sets it so `sandbox
// --trace …` and `SANDBOX_TRACE=1 sandbox …` behave identically.
var traceEnabledFlag bool

// configureTracing turns the --trace flag into the SANDBOX_TRACE env var the
// client package reads. Setting (never unsetting) it means either the flag or a
// pre-set env var enables tracing; neither leaves it off. Called from the root
// command's PersistentPreRun so every subcommand honors the flag.
func configureTracing() {
	if traceEnabledFlag {
		_ = os.Setenv("SANDBOX_TRACE", "1")
	}
}
