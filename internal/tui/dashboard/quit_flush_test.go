package dashboard

import (
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1a step 4: quitting must flush a final snapshot for EVERY session (bypassing
// the 3s save throttle), so the persisted resume cursor is current and a
// relaunch resumes from head instead of replaying a tail of history as live.
func TestCancelFlushesSnapshotsForAllSessions(t *testing.T) {
	store := newFakeSnapshotStore()
	m := New(nil).WithSnapshotStore(store)

	s1 := SessionFromState(session.State{ID: "s1", Status: session.StatusRunning})
	s1.lastSeq = 42
	s1.lastSnapSave = time.Now() // recent → an unforced save would be throttled out
	s2 := SessionFromState(session.State{ID: "s2", Status: session.StatusRunning})
	s2.lastSeq = 7
	s2.lastSnapSave = time.Now()
	m.sessions = []Session{s1, s2}

	m.Cancel()

	if snap, ok := store.LoadSnapshot("s1"); !ok || snap.LastSeq != 42 {
		t.Fatalf("s1 snapshot not flushed with current cursor: ok=%v lastSeq=%d (want 42)", ok, snap.LastSeq)
	}
	if snap, ok := store.LoadSnapshot("s2"); !ok || snap.LastSeq != 7 {
		t.Fatalf("s2 snapshot not flushed with current cursor: ok=%v lastSeq=%d (want 7)", ok, snap.LastSeq)
	}
}
