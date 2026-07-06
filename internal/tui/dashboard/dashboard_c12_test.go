package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestApplySeedClearsStalePermissionOnSuspend verifies C12: if a session was
// showing a pending permission ("waiting") and a later seed reports it suspended
// (e.g. the idle reaper kicked in while the permission was still on screen), the
// stale Waiting status and pending permission must be dropped in favour of the
// authoritative cluster status — there is no live stream to ever resolve it, so
// preserving it would leave a phantom permission badge.
func TestApplySeedClearsStalePermissionOnSuspend(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{
			State:            session.State{ID: "s", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusWaiting, PendingPermissionID: "perm1", PendingPermissionTool: "Edit"},
		}}

	seed := []session.State{{ID: "s", Status: session.StatusSuspended}}
	next, _ := m.applySeed(seed)

	if len(next.sessions) != 1 {
		t.Fatalf("session count: got %d, want 1", len(next.sessions))
	}
	s := next.sessions[0]
	if s.PendingPermissionID != "" || s.PendingPermissionTool != "" {
		t.Errorf("stale pending permission not cleared on suspend (C12): id=%q tool=%q", s.PendingPermissionID, s.PendingPermissionTool)
	}
	if s.DashStatus == StatusWaiting {
		t.Error("stale Waiting status preserved on suspend; want cluster-derived status")
	}
}

// TestApplySeedStillPreservesRunningWaiting guards the B10 invariant under the
// C12 change: a still-running session keeps its runner-derived Waiting status and
// pending permission across a concurrent seed (the live stream will resolve it).
func TestApplySeedStillPreservesRunningWaiting(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{
			State:            session.State{ID: "s", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusWaiting, PendingPermissionID: "perm1", PendingPermissionTool: "Edit"},
		}}

	seed := []session.State{{ID: "s", Status: session.StatusRunning}}
	next, _ := m.applySeed(seed)

	s := next.sessions[0]
	if s.PendingPermissionID != "perm1" || s.DashStatus != StatusWaiting {
		t.Errorf("running session lost its pending permission across seed (B10): status=%v id=%q", s.DashStatus, s.PendingPermissionID)
	}
}
