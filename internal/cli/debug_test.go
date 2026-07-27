package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugLoggingDisabledByDefault(t *testing.T) {
	// Restore globals after the test.
	t.Cleanup(func() {
		debugEnabled = false
		debugOut = io.Discard
		debugLogger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	})

	var buf bytes.Buffer
	debugOut = &buf
	debugEnabled = false
	t.Setenv("SANDBOX_DEBUG", "")
	configureDebugLogging()
	dbg("should not appear", "k", "v")
	if buf.Len() != 0 {
		t.Fatalf("debug output emitted while disabled: %q", buf.String())
	}
}

func TestDebugLoggingJSONLineSchema(t *testing.T) {
	t.Cleanup(func() {
		debugEnabled = false
		debugOut = io.Discard
		debugLogger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	})

	var buf bytes.Buffer
	debugOut = &buf
	debugEnabled = true
	configureDebugLogging()
	dbg("port-forward established", "session", "alpha", "count", 7)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("debug line is not valid JSON: %v\n%s", err, buf.String())
	}
	// Documented schema: time, level, msg, component + structured fields.
	for _, key := range []string{"time", "level", "msg", "component"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("debug record missing %q field: %v", key, rec)
		}
	}
	if rec["level"] != "DEBUG" {
		t.Errorf("level: got %v want DEBUG", rec["level"])
	}
	if rec["component"] != "cli" {
		t.Errorf("component: got %v want cli", rec["component"])
	}
	if rec["msg"] != "port-forward established" {
		t.Errorf("msg: got %v", rec["msg"])
	}
	if rec["session"] != "alpha" {
		t.Errorf("structured field session: got %v want alpha", rec["session"])
	}
	if rec["count"] != float64(7) {
		t.Errorf("structured field count: got %v want 7", rec["count"])
	}
}

// [T1]: --debug's stderr sink is unusable in the primary workflow — the
// dashboard owns the alt-screen, so records either scroll past invisibly or
// scribble over the UI. attachDebugFileSink gives a run a durable artifact.
func TestAttachDebugFileSinkWritesSessionLog(t *testing.T) {
	t.Cleanup(resetDebugGlobals)

	home := t.TempDir()
	t.Setenv("HOME", home)
	var stderr bytes.Buffer
	debugOut = &stderr
	debugEnabled = true
	configureDebugLogging()

	if err := attachDebugFileSink("sess-alpha", false); err != nil {
		t.Fatalf("attach: %v", err)
	}
	dbg("port-forward established", "endpoint", "127.0.0.1:8787")
	closeDebugFileSink()

	path := filepath.Join(home, ".local", "share", "sandbox", "remote-sessions", "sess-alpha", "debug.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("debug log is not JSONL: %v\n%s", err, data)
	}
	if rec["msg"] != "port-forward established" {
		t.Errorf("msg: got %v", rec["msg"])
	}
	// The session id is pinned on every record, so a log found on disk is
	// attributable without reading the path it came from.
	if rec["session"] != "sess-alpha" {
		t.Errorf("session field: got %v", rec["session"])
	}
	// File-ONLY: a stderr write here is what corrupts the alt-screen.
	if stderr.Len() != 0 {
		t.Errorf("records reached stderr while the TUI owns the screen: %q", stderr.String())
	}
}

func TestAttachDebugFileSinkAppendsAcrossCommands(t *testing.T) {
	t.Cleanup(resetDebugGlobals)

	home := t.TempDir()
	t.Setenv("HOME", home)
	debugOut = io.Discard
	debugEnabled = true
	configureDebugLogging()

	// A session is debugged across several commands (create, attach, suspend);
	// truncating per attach would leave only the last one's records.
	for _, msg := range []string{"first run", "second run"} {
		if err := attachDebugFileSink("sess-beta", false); err != nil {
			t.Fatalf("attach: %v", err)
		}
		dbg(msg)
		closeDebugFileSink()
	}

	path := filepath.Join(home, ".local", "share", "sandbox", "remote-sessions", "sess-beta", "debug.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}
	if got := len(bytes.Split(bytes.TrimSpace(data), []byte("\n"))); got != 2 {
		t.Fatalf("expected 2 appended records, got %d:\n%s", got, data)
	}
}

func TestAttachDebugFileSinkNoOpWhenDebugOff(t *testing.T) {
	t.Cleanup(resetDebugGlobals)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SANDBOX_DEBUG", "")
	debugEnabled = false
	configureDebugLogging()

	if err := attachDebugFileSink("sess-gamma", false); err != nil {
		t.Fatalf("attach should be a silent no-op: %v", err)
	}
	dbg("nothing")
	closeDebugFileSink()

	// No debug run means no stray directory or file in the user's state dir.
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "sandbox", "remote-sessions", "sess-gamma")); !os.IsNotExist(err) {
		t.Errorf("debug off should create nothing, stat err = %v", err)
	}
}

func TestAttachDebugFileSinkCanTeeToStderr(t *testing.T) {
	t.Cleanup(resetDebugGlobals)

	home := t.TempDir()
	t.Setenv("HOME", home)
	var stderr bytes.Buffer
	debugOut = &stderr
	debugEnabled = true
	configureDebugLogging()

	if err := attachDebugFileSink("sess-delta", true); err != nil {
		t.Fatalf("attach: %v", err)
	}
	dbg("visible in both")
	closeDebugFileSink()

	if !bytes.Contains(stderr.Bytes(), []byte("visible in both")) {
		t.Errorf("tee mode should still write stderr, got %q", stderr.String())
	}
	path := filepath.Join(home, ".local", "share", "sandbox", "remote-sessions", "sess-delta", "debug.jsonl")
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("visible in both")) {
		t.Errorf("tee mode should still write the file: err=%v data=%s", err, data)
	}
}

// resetDebugGlobals restores the package-level debug state between tests.
func resetDebugGlobals() {
	closeDebugFileSink()
	debugEnabled = false
	debugOut = io.Discard
	debugLogger = slog.New(slog.NewJSONHandler(io.Discard, nil))
}
