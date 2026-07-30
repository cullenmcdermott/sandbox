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

// SyncAdvisory is the non-blocking half of AwaitSync. It exists for the surface
// that has to show these without blocking a render, and — more importantly — for
// the [R5] advisories that land AFTER the task settles, which no blocking call
// will ever return because there is nothing left to block on.
func TestSyncAdvisoryReadsWithoutWaitingForTheTaskToSettle(t *testing.T) {
	task := &syncTask{done: make(chan struct{})}
	s := &Session{syncTask: task}

	// Still in flight: the advisory reads clean and, crucially, returns.
	if got := s.SyncAdvisory(); got != "" {
		t.Errorf("SyncAdvisory on an unsettled task = %q, want empty", got)
	}
	task.addWarning("file sync unavailable: mutagen refused")
	if got := s.SyncAdvisory(); !strings.Contains(got, "mutagen refused") {
		t.Errorf("SyncAdvisory = %q, want the in-flight advisory", got)
	}

	task.finish("file sync unavailable: mutagen refused", nil)
	task.addWarning("file sync is stalled after reconnect")
	got := s.SyncAdvisory()
	if !strings.Contains(got, "mutagen refused") || !strings.Contains(got, "stalled after reconnect") {
		t.Errorf("SyncAdvisory = %q, want both the settled and the late advisory", got)
	}
}

// A session that never connected has no task; the advisory is empty, not a panic.
func TestSyncAdvisoryIsEmptyWithNoBackgroundWork(t *testing.T) {
	if got := (&Session{}).SyncAdvisory(); got != "" {
		t.Errorf("SyncAdvisory with no syncTask = %q, want empty", got)
	}
}

// The FATAL outcome is AwaitSync's alone: it is a hard gate that refuses a turn,
// not a line of status-row text. SyncAdvisory must never be the thing a caller
// checks for it.
func TestSyncAdvisoryDoesNotLeakTheFatalError(t *testing.T) {
	task := &syncTask{done: make(chan struct{})}
	task.finish("", ErrInitialSyncFailed)
	s := &Session{syncTask: task}

	if got := s.SyncAdvisory(); got != "" {
		t.Errorf("SyncAdvisory = %q, want empty (a fatal outcome is not an advisory)", got)
	}
	if _, err := s.AwaitSync(context.Background()); !errors.Is(err, ErrInitialSyncFailed) {
		t.Errorf("AwaitSync err = %v, want ErrInitialSyncFailed still reported there", err)
	}
}
