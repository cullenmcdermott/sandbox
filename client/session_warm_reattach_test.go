package client

import (
	"context"
	"errors"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// [R1a] Connect used to call StartWithProgress on EVERY full connect, whose
// first readiness poll repeats the exact two API round trips (Sandbox Get + pod
// List) that the Status call immediately above already made. On a warm reattach
// — a session the cluster just told us is Running with a Ready pod — that is a
// pure serialized round trip on the user-visible attach path.
//
// These tests drive Connect only as far as the port-forward (portErr stops it
// there), which is *after* the readiness gate, so whether the wait ran is
// observable without standing up a runner.

func TestConnectWarmReattachSkipsPodReadyWait(t *testing.T) {
	be := newFakeBackend()
	be.statusState = State{
		ID:          "claude-sdk-warm",
		Status:      session.StatusRunning,
		PodReady:    true,
		ProjectPath: "/w",
	}
	be.portErr = errors.New("stop here")
	c, _, _ := fakeClient(t, be)

	_, err := c.Open("claude-sdk-warm").Connect(context.Background(), ConnectOptions{})
	if err == nil || !errors.Is(err, be.portErr) {
		t.Fatalf("Connect = %v, want to have reached the port-forward", err)
	}
	if n := be.callCount("start"); n != 0 {
		t.Errorf("StartWithProgress called %d times on a warm reattach, want 0 "+
			"(Status already reported Running + PodReady)", n)
	}
}

// The skip is conditioned on BOTH signals. A session reported Running whose pod
// is not Ready still has to wait — this is the ordinary cold/booting case.
func TestConnectRunningButNotReadyStillWaits(t *testing.T) {
	be := newFakeBackend()
	be.statusState = State{
		ID:          "claude-sdk-cold",
		Status:      session.StatusRunning,
		PodReady:    false,
		ProjectPath: "/w",
	}
	be.portErr = errors.New("stop here")
	c, _, _ := fakeClient(t, be)

	_, _ = c.Open("claude-sdk-cold").Connect(context.Background(), ConnectOptions{})
	if n := be.callCount("start"); n != 1 {
		t.Errorf("StartWithProgress called %d times, want 1 (pod not ready)", n)
	}
}

// The stale-node trap: a pod on a dead node keeps reading Running+Ready for
// minutes, so Status downgrades it to StatusUnknown with PodReady=false
// (podStale). Such a session must NOT take the skip — this is the case the
// readiness wait exists to catch.
func TestConnectStaleNodeUnknownStillWaits(t *testing.T) {
	be := newFakeBackend()
	be.statusState = State{
		ID:          "claude-sdk-stale",
		Status:      session.StatusUnknown,
		PodReady:    false,
		ProjectPath: "/w",
	}
	be.portErr = errors.New("stop here")
	c, _, _ := fakeClient(t, be)

	_, _ = c.Open("claude-sdk-stale").Connect(context.Background(), ConnectOptions{})
	if n := be.callCount("start"); n != 1 {
		t.Errorf("StartWithProgress called %d times, want 1 (stale node → Unknown)", n)
	}
}

// The resume trap: the old pod terminates while still briefly Ready, so a wait
// that accepted it would report ready ~10-15s before the new pod is up. A
// just-resumed session cannot take the skip, because its Status was read as
// Suspended *before* Resume ran — never StatusRunning.
func TestConnectAfterResumeStillWaits(t *testing.T) {
	be := newFakeBackend()
	be.statusState = State{
		ID:          "claude-sdk-susp",
		Status:      session.StatusSuspended,
		PodReady:    false,
		ProjectPath: "/w",
	}
	be.portErr = errors.New("stop here")
	c, _, _ := fakeClient(t, be)

	_, _ = c.Open("claude-sdk-susp").Connect(context.Background(), ConnectOptions{})
	if n := be.callCount("resume"); n != 1 {
		t.Fatalf("Resume called %d times, want 1", n)
	}
	if n := be.callCount("start"); n != 1 {
		t.Errorf("StartWithProgress called %d times after a resume, want 1 "+
			"(the new pod is still booting)", n)
	}
}
