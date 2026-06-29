package sync

import (
	"context"
	"io"
	"strings"
	"testing"
)

// stubRunner returns canned output (and records calls) so List/parse can be
// exercised without a real mutagen daemon.
type stubRunner struct {
	out   []byte
	err   error
	calls [][]string
}

func (s *stubRunner) Output(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
	s.calls = append(s.calls, args)
	return s.out, s.err
}

// List scopes to OUR syncs (the sandbox-session label, non-empty first field) and
// drops everything else: the lima sandbox-vm-id syncs (empty first field — they
// share the host daemon and must never be touched), blank lines, and malformed
// rows.
func TestList_FiltersToSandboxSessionLabel(t *testing.T) {
	out := strings.Join([]string{
		"claude-sdk-abc|sync_111|sandbox-claude-sdk-abc-project|Watching",
		"claude-sdk-abc|sync_222|sandbox-claude-sdk-abc-config-skills|ConnectingBeta",
		"|sync_lima|sandbox-vm-orch-project|Watching", // lima sync: no sandbox-session label → drop
		"opencode-server-xyz|sync_333|sandbox-opencode-server-xyz-project|Paused",
		"", // trailing blank
	}, "\n")
	r := &stubRunner{out: []byte(out)}
	m := New(r)

	sessions, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sandbox-session syncs (lima + blank dropped), got %d: %+v", len(sessions), sessions)
	}
	// It must NOT shell out with a label selector — a key-only selector returns
	// nothing on this mutagen; we list all + filter in Go.
	got := strings.Join(r.calls[0], " ")
	if !strings.HasPrefix(got, "sync list --template") {
		t.Errorf("List should be `sync list --template ...`, got %q", got)
	}
	if strings.Contains(got, "label-selector") {
		t.Errorf("List must not use a label selector (key-only returns nothing): %q", got)
	}
	// Fields parsed in order sessionID|identifier|name|status.
	if sessions[0] != (SyncSession{SessionID: "claude-sdk-abc", Identifier: "sync_111", Name: "sandbox-claude-sdk-abc-project", Status: "Watching"}) {
		t.Errorf("row 0 mis-parsed: %+v", sessions[0])
	}
	// The lima sync_lima must be absent.
	for _, s := range sessions {
		if s.Identifier == "sync_lima" {
			t.Fatal("lima sync (sandbox-vm-id) leaked into our List — would risk terminating a real session's sync")
		}
	}
}

func TestList_NotFoundIsEmpty(t *testing.T) {
	r := &stubRunner{err: &fakeError{msg: "no sessions found"}}
	sessions, err := New(r).List(context.Background())
	if err != nil {
		t.Fatalf("not-found should be empty, got err %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected empty, got %d", len(sessions))
	}
}

// IsOrphanStatus is the cluster-agnostic "the pod endpoint is gone" signal:
// Connecting/Disconnected = orphan; Watching, Paused (intentional suspend), and
// the connected working states = healthy (keep).
func TestIsOrphanStatus(t *testing.T) {
	cases := []struct {
		status string
		orphan bool
	}{
		{"Watching", false},
		{"Watching for changes", false},
		{"Paused", false},
		{"Scanning", false},
		{"Reconciling", false},
		{"Staging", false},
		{"Transitioning", false},
		{"ConnectingBeta", true},
		{"ConnectingAlpha", true},
		{"Connecting to beta", true},
		{"Disconnected", true},
		{"", false},
	}
	for _, c := range cases {
		if got := IsOrphanStatus(c.status); got != c.orphan {
			t.Errorf("IsOrphanStatus(%q) = %v, want %v", c.status, got, c.orphan)
		}
	}
}

// MF4: an empty ProjectPath must skip ONLY the project sync, not abort the whole
// CreateAll — the config-input + transcript groups need no project path and must
// still (re)create so agent-activity sync recovers (e.g. after a GC reap on a
// session whose State lost its local path).
func TestCreateAll_EmptyProjectPathStillSyncsConfigAndTranscripts(t *testing.T) {
	r := &fakeRunner{}
	created, err := New(r).CreateAll(context.Background(), Spec{
		SessionID:    "abc",
		ProjectPath:  "", // unknown local path
		HomeDir:      "/home/u",
		SSHHost:      "sandbox-abc",
		RemoteClaude: "/session/state/claude",
	})
	if err != nil {
		t.Fatalf("empty ProjectPath must not abort all syncs: %v", err)
	}
	if created {
		t.Error("no project sync was created, so created must be false")
	}
	var sawProject, sawConfig, sawTranscript bool
	for _, call := range r.calls {
		joined := strings.Join(call, " ")
		switch {
		case strings.Contains(joined, "--name=sandbox-abc-project"):
			sawProject = true
		case strings.Contains(joined, "--name=sandbox-abc-config-skills"):
			sawConfig = true
		case strings.Contains(joined, "--name=sandbox-abc-transcripts-projects"):
			sawTranscript = true
		}
	}
	if sawProject {
		t.Error("project sync must be SKIPPED when ProjectPath is empty (blank alpha URL)")
	}
	if !sawConfig {
		t.Error("config-input syncs must still be created (they need no project path)")
	}
	if !sawTranscript {
		t.Error("transcript syncs must still be created")
	}
}

func TestTerminateByIdentifier(t *testing.T) {
	r := &stubRunner{}
	m := New(r)
	if err := m.TerminateByIdentifier(context.Background(), "sync_a", "sync_b"); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	got := strings.Join(r.calls[0], " ")
	if want := "sync terminate sync_a sync_b"; got != want {
		t.Errorf("terminate args: got %q, want %q", got, want)
	}

	// Empty id list is a no-op (no mutagen call — avoids `sync terminate` with no
	// args, which mutagen would reject or misinterpret).
	r2 := &stubRunner{}
	if err := New(r2).TerminateByIdentifier(context.Background()); err != nil {
		t.Fatalf("empty terminate: %v", err)
	}
	if len(r2.calls) != 0 {
		t.Errorf("empty id list should make no call, got %v", r2.calls)
	}

	// "not found" (already gone) is success.
	r3 := &stubRunner{err: &fakeError{msg: "unable to locate requested sessions"}}
	if err := New(r3).TerminateByIdentifier(context.Background(), "sync_gone"); err != nil {
		t.Errorf("not-found terminate should succeed, got %v", err)
	}
}
