package client

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// [R5] Two best-effort sync calls on the reconnect path discarded their errors,
// and both discard the SAME failure mode: files silently stop moving while the
// connect reports clean. SyncForwardAlive() cannot notice — the SSH forward is
// healthy — so if these do not speak up, nothing does.

// A failed un-pause is the one the code comment already described: CreateProject
// is idempotent and SKIPS an existing sync without un-pausing it, so a failed
// ResumeAll leaves the agent attached to a frozen workspace.
func TestStartProjectSyncReportsFailedResume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	be := newFakeBackend()
	c, spy, _ := fakeClient(t, be)
	boom := errors.New("mutagen resume refused")
	spy.failVerb("resume", boom)

	_, _, resumeWarn, err := c.startProjectSync(
		context.Background(), "claude-sdk-abc123", "/work/repo", "/keys/id_ed25519", 12345)
	if err != nil {
		t.Fatalf("startProjectSync: %v", err)
	}
	if resumeWarn == nil {
		t.Fatal("a failed ResumeAll was swallowed; the sync may be paused with no error surfaced")
	}
	if !errors.Is(resumeWarn, boom) {
		t.Errorf("resumeWarn = %v, want it to wrap the ResumeAll failure", resumeWarn)
	}
}

// The un-pause failure must remain ADVISORY: the project sync may well have been
// created, and the rest of the connect is sound, so it is a warning and not an
// error return.
func TestStartProjectSyncFailedResumeIsNotFatal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	be := newFakeBackend()
	c, spy, _ := fakeClient(t, be)
	spy.failVerb("resume", errors.New("mutagen resume refused"))

	_, spec, resumeWarn, err := c.startProjectSync(
		context.Background(), "claude-sdk-abc123", "/work/repo", "/keys/id_ed25519", 12345)
	if err != nil {
		t.Fatalf("a failed un-pause must not fail the connect: %v", err)
	}
	if resumeWarn == nil {
		t.Fatal("expected an advisory")
	}
	if spec.ProjectPath != "/work/repo" {
		t.Errorf("spec still expected to be built, got ProjectPath %q", spec.ProjectPath)
	}
}

// The reconnect flush is deliberately DETACHED (a healthy mutagen session
// reconciles on its own, so the turn gate must not wait on it), which means a
// failure can only be discovered after the task has already settled. addWarning
// is the late-advisory path that makes such a discovery observable at all.
func TestSyncTaskLateWarningReachesAwaitSync(t *testing.T) {
	task := &syncTask{done: make(chan struct{})}
	task.finish("", nil)

	s := &Session{syncTask: task}
	warn, err := s.AwaitSync(context.Background())
	if err != nil || warn != "" {
		t.Fatalf("baseline AwaitSync = (%q, %v), want clean", warn, err)
	}

	task.addWarning("file sync is stalled after reconnect (boom); edits may not be propagating")

	warn, err = s.AwaitSync(context.Background())
	if err != nil {
		t.Fatalf("AwaitSync err = %v, want nil (a late discovery is advisory)", err)
	}
	if !strings.Contains(warn, "stalled after reconnect") {
		t.Errorf("AwaitSync warning = %q, want the late advisory", warn)
	}
}

// A late advisory must never retroactively turn a completed connect fatal, and
// must join rather than clobber an advisory already recorded.
func TestSyncTaskLateWarningJoinsAndKeepsErr(t *testing.T) {
	task := &syncTask{done: make(chan struct{})}
	task.finish("first advisory", nil)
	task.addWarning("second advisory")

	warn, err := task.result()
	if err != nil {
		t.Errorf("result err = %v, want nil", err)
	}
	if !strings.Contains(warn, "first advisory") || !strings.Contains(warn, "second advisory") {
		t.Errorf("warning = %q, want both advisories joined", warn)
	}

	// An empty late warning is a no-op, not an empty join artifact.
	before, _ := task.result()
	task.addWarning("")
	after, _ := task.result()
	if before != after {
		t.Errorf("addWarning(\"\") changed the advisory: %q → %q", before, after)
	}
}
