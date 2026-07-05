package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1a step 7: a transient stream drop on a still-Running pod must PRESERVE the
// pending permission (and schedule a reconnect), not wipe it — the reconnect
// replays after=lastSeq which excludes the permission.requested, so clearing it
// would leave approve/deny permanently dead.
func TestStreamEndedPreservesPermissionOnRunningBlip(t *testing.T) {
	store := newFakeSnapshotStore()
	m := New(nil).WithSnapshotStore(store)
	s := SessionFromState(session.State{ID: "s1", Status: session.StatusRunning})
	s.DashStatus = StatusWaiting
	s.PendingPermissionID = "perm-1"
	s.PendingPermissionTool = "Bash"
	s.lastSeq = 5
	m.sessions = []Session{s}

	_, cmd := m.handleRunnerEvent(RunnerEventMsg{ID: "s1", StreamEnded: true})

	got := m.sessionByID("s1")
	if got.PendingPermissionID != "perm-1" || got.PendingPermissionTool != "Bash" {
		t.Fatalf("Running-pod blip wiped the pending permission: id=%q tool=%q (want preserved)",
			got.PendingPermissionID, got.PendingPermissionTool)
	}
	if got.DashStatus != StatusWaiting {
		t.Fatalf("Running-pod blip should keep the Waiting glyph, got %v", got.DashStatus)
	}
	if cmd == nil {
		t.Fatal("Running-pod blip should schedule a reconnect tick")
	}
	if snap, ok := store.LoadSnapshot("s1"); !ok || snap.PendingPermissionID != "perm-1" {
		t.Fatalf("snapshot did not persist the preserved permission: ok=%v id=%q (want perm-1)",
			ok, snap.PendingPermissionID)
	}
}

// (NeedsInput-preservation on a Running-pod blip is covered by the revised B13
// oracle TestRunnerStreamEndedAtRestPreservesAttentionAndReconnects.)

// When the cluster says the pod is no longer running, the permission can never
// resolve, so StreamEnded degrades the status AND clears it.
func TestStreamEndedClearsPermissionOnSuspended(t *testing.T) {
	m := New(nil)
	s := SessionFromState(session.State{ID: "s2", Status: session.StatusSuspended})
	s.DashStatus = StatusWaiting
	s.PendingPermissionID = "perm-2"
	m.sessions = []Session{s}

	m.handleRunnerEvent(RunnerEventMsg{ID: "s2", StreamEnded: true})

	got := m.sessionByID("s2")
	if got.PendingPermissionID != "" {
		t.Fatalf("suspended-pod StreamEnded should clear the permission, got %q", got.PendingPermissionID)
	}
	if got.DashStatus != StatusSuspended {
		t.Fatalf("suspended-pod StreamEnded should degrade to Suspended, got %v", got.DashStatus)
	}
}
