package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
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
