package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript writes an executable /bin/sh script under t.TempDir and returns
// its path. No network/port is involved — these scripts just print and exit.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-mutagen")
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return p
}

// A nonzero exit must surface stderr in the returned error so a failed mutagen
// invocation is diagnosable.
func TestExecRunnerNonzeroExitIncludesStderr(t *testing.T) {
	bin := writeScript(t, `echo "boom on stderr" 1>&2; exit 3`)
	r := NewExecRunner(bin)
	_, err := r.Output(context.Background(), nil, "sync", "list")
	if err == nil {
		t.Fatal("expected error for nonzero exit")
	}
	if !strings.Contains(err.Error(), "boom on stderr") {
		t.Errorf("error should include stderr, got %v", err)
	}
	// The args should be echoed into the message for context.
	if !strings.Contains(err.Error(), "sync") || !strings.Contains(err.Error(), "list") {
		t.Errorf("error should include the args, got %v", err)
	}
}

// When stderr is empty, the runner falls back to stdout as the message so a
// tool that reports failures on stdout is still diagnosable.
func TestExecRunnerStdoutFallbackWhenStderrEmpty(t *testing.T) {
	bin := writeScript(t, `echo "failure on stdout"; exit 1`)
	r := NewExecRunner(bin)
	_, err := r.Output(context.Background(), nil, "sync", "flush")
	if err == nil {
		t.Fatal("expected error for nonzero exit")
	}
	if !strings.Contains(err.Error(), "failure on stdout") {
		t.Errorf("error should fall back to stdout when stderr empty, got %v", err)
	}
}

// A zero exit returns stdout and no error.
func TestExecRunnerSuccessReturnsStdout(t *testing.T) {
	bin := writeScript(t, `printf "ok-output"; exit 0`)
	r := NewExecRunner(bin)
	out, err := r.Output(context.Background(), nil, "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok-output" {
		t.Errorf("stdout: got %q, want %q", out, "ok-output")
	}
}

// An empty bin must default to "mutagen" (resolved via PATH), not "".
func TestNewExecRunnerEmptyBinDefaults(t *testing.T) {
	r := NewExecRunner("")
	if r.bin != "mutagen" {
		t.Errorf("empty bin should default to \"mutagen\", got %q", r.bin)
	}
	// A non-empty bin is preserved.
	if got := NewExecRunner("/usr/local/bin/mutagen").bin; got != "/usr/local/bin/mutagen" {
		t.Errorf("non-empty bin should be preserved, got %q", got)
	}
}
