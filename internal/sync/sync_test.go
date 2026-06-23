package sync

import (
	"context"
	"io"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
}

func (f *fakeRunner) Output(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	return nil, nil
}

type errorRunner struct {
	msg string
}

func (e *errorRunner) Output(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
	return nil, &fakeError{msg: e.msg}
}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

func TestStatusUsesLabelSelector(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)
	if _, err := m.Status(context.Background(), "abc"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	got := strings.Join(r.calls[0], " ")
	if want := "sync list --label-selector=sandbox-session=abc"; got != want {
		t.Errorf("status args: got %q, want %q", got, want)
	}
}

func TestCreateAll(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)

	spec := Spec{
		SessionID:    "test-session",
		ProjectPath:  "/Users/cullen/git/homelab",
		RemotePath:   "/session/workspace/Users/cullen/git/homelab",
		HomeDir:      "/Users/cullen",
		SSHHost:      "127.0.0.1:22000",
		RemoteClaude: "/session/state/claude",
	}

	if err := m.CreateAll(context.Background(), spec); err != nil {
		t.Fatalf("createAll: %v", err)
	}

	// Should have created: 1 project + 4 config + 3 transcripts = 8 sessions
	if len(r.calls) != 8 {
		t.Fatalf("got %d calls, want 8", len(r.calls))
	}

	// First call should be the project sync with two-way-safe
	first := strings.Join(r.calls[0], " ")
	if !strings.Contains(first, "two-way-safe") {
		t.Errorf("project sync should be two-way-safe: %s", first)
	}
	if !strings.Contains(first, "test-session-project") {
		t.Errorf("project session name missing: %s", first)
	}

	// Verify config syncs use one-way-safe
	for i := 1; i <= 4; i++ {
		call := strings.Join(r.calls[i], " ")
		if !strings.Contains(call, "one-way-safe") {
			t.Errorf("config sync %d should be one-way-safe: %s", i, call)
		}
	}

	// Verify transcript syncs use one-way-safe
	for i := 5; i <= 7; i++ {
		call := strings.Join(r.calls[i], " ")
		if !strings.Contains(call, "one-way-safe") {
			t.Errorf("transcript sync %d should be one-way-safe: %s", i, call)
		}
	}
}

func TestCreateAllIdempotent(t *testing.T) {
	r := &errorRunner{msg: "session already exists"}
	m := New(r)

	spec := Spec{
		SessionID:    "test-session",
		ProjectPath:  "/tmp",
		RemotePath:   "/session/workspace/tmp",
		HomeDir:      "/Users/cullen",
		SSHHost:      "127.0.0.1:22000",
		RemoteClaude: "/session/state/claude",
	}

	// "already exists" errors should be swallowed
	if err := m.CreateAll(context.Background(), spec); err != nil {
		t.Fatalf("createAll should swallow already-exists: %v", err)
	}
}

func TestCreateAllRealError(t *testing.T) {
	r := &errorRunner{msg: "connection refused"}
	m := New(r)

	spec := Spec{
		SessionID:    "test-session",
		ProjectPath:  "/tmp",
		RemotePath:   "/session/workspace/tmp",
		HomeDir:      "/Users/cullen",
		SSHHost:      "127.0.0.1:22000",
		RemoteClaude: "/session/state/claude",
	}

	if err := m.CreateAll(context.Background(), spec); err == nil {
		t.Fatal("expected error for connection refused")
	}
}

func TestPauseResumeTerminate(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)

	if err := m.PauseAll(context.Background(), "test"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("pause: got %d calls, want 1", len(r.calls))
	}
	if !strings.Contains(strings.Join(r.calls[0], " "), "pause") {
		t.Errorf("expected pause command: %s", r.calls[0])
	}

	r.calls = nil
	if err := m.ResumeAll(context.Background(), "test"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(strings.Join(r.calls[0], " "), "resume") {
		t.Errorf("expected resume command: %s", r.calls[0])
	}

	r.calls = nil
	if err := m.TerminateAll(context.Background(), "test"); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if !strings.Contains(strings.Join(r.calls[0], " "), "terminate") {
		t.Errorf("expected terminate command: %s", r.calls[0])
	}
}

func TestTerminateAllNotFound(t *testing.T) {
	r := &errorRunner{msg: "session not found"}
	m := New(r)
	// "not found" should be swallowed
	if err := m.TerminateAll(context.Background(), "test"); err != nil {
		t.Fatalf("terminate should swallow not-found: %v", err)
	}
}
