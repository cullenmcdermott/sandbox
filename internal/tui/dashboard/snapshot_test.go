package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// fakeSnapshotStore is an in-memory SnapshotStore for tests.
type fakeSnapshotStore struct {
	snaps map[session.ID]SessionSnapshot
	saves int
}

func newFakeSnapshotStore() *fakeSnapshotStore {
	return &fakeSnapshotStore{snaps: map[session.ID]SessionSnapshot{}}
}

func (f *fakeSnapshotStore) LoadSnapshot(id session.ID) (SessionSnapshot, bool) {
	s, ok := f.snaps[id]
	return s, ok
}

func (f *fakeSnapshotStore) SaveSnapshot(id session.ID, snap SessionSnapshot) {
	f.snaps[id] = snap
	f.saves++
}

// applySeed hydrates a fresh running session from the cached snapshot so the row
// shows its real status/usage immediately instead of the cluster-derived idle.
func TestApplySeedHydratesFromSnapshot(t *testing.T) {
	store := newFakeSnapshotStore()
	store.snaps["s1"] = SessionSnapshot{
		LastSeq:               55,
		DashStatus:            StatusWaiting,
		PendingPermissionID:   "perm-1",
		PendingPermissionTool: "Bash",
		Model:                 "opus-4.8",
		InputTokens:           1234,
	}
	m := New(nil).WithSnapshotStore(store)

	m, _ = m.applySeed([]session.State{
		{ID: "s1", Status: session.StatusRunning, PodReady: true},
	})

	s := m.sessions[0]
	if s.DashStatus != StatusWaiting {
		t.Errorf("DashStatus = %v, want waiting (hydrated from snapshot)", s.DashStatus)
	}
	if s.PendingPermissionID != "perm-1" || s.PendingPermissionTool != "Bash" {
		t.Errorf("pending perm not hydrated: %q/%q", s.PendingPermissionID, s.PendingPermissionTool)
	}
	if s.InputTokens != 1234 {
		t.Errorf("InputTokens = %d, want 1234", s.InputTokens)
	}
	if s.lastSeq != 55 {
		t.Errorf("lastSeq = %d, want 55 (resume cursor)", s.lastSeq)
	}
}

// A suspended pod's cluster status is authoritative: a stale "running" snapshot
// status must not override it, but the resume cursor is still carried so a later
// stream resumes cleanly (mirrors the prev-branch + C12 invariant).
func TestApplySeedIgnoresSnapshotStatusWhenSuspended(t *testing.T) {
	store := newFakeSnapshotStore()
	store.snaps["s1"] = SessionSnapshot{LastSeq: 9, DashStatus: StatusBusy}
	m := New(nil).WithSnapshotStore(store)

	m, _ = m.applySeed([]session.State{
		{ID: "s1", Status: session.StatusSuspended},
	})

	s := m.sessions[0]
	if s.DashStatus != StatusSuspended {
		t.Errorf("DashStatus = %v, want suspended (cluster authoritative)", s.DashStatus)
	}
	if s.lastSeq != 9 {
		t.Errorf("lastSeq = %d, want 9 (cursor still carried)", s.lastSeq)
	}
}

// handleRunnerEvent advances the resume cursor and persists the snapshot on a
// status transition so a relaunch resumes from there instead of replaying.
func TestHandleRunnerEventPersistsSnapshot(t *testing.T) {
	store := newFakeSnapshotStore()
	m := New(nil).WithSnapshotStore(store)
	m.sessions = []Session{
		{
			State:            session.State{ID: "s1", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
		}}

	m.handleRunnerEvent(RunnerEventMsg{
		ID:    "s1",
		Event: session.Event{Seq: 12, Type: session.EventTurnStarted},
	})

	snap, ok := store.LoadSnapshot("s1")
	if !ok {
		t.Fatal("snapshot not persisted on transition")
	}
	if snap.LastSeq != 12 {
		t.Errorf("persisted LastSeq = %d, want 12", snap.LastSeq)
	}
	if snap.DashStatus != StatusBusy {
		t.Errorf("persisted DashStatus = %v, want busy", snap.DashStatus)
	}
	if m.sessions[0].lastSeq != 12 {
		t.Errorf("session lastSeq = %d, want 12", m.sessions[0].lastSeq)
	}
}

// Usage-only events (no status change) are throttled: the first persists (to
// capture the advancing seq), but a rapid follow-up within the throttle window
// does not write again.
func TestUsageEventsThrottleSnapshotSaves(t *testing.T) {
	store := newFakeSnapshotStore()
	m := New(nil).WithSnapshotStore(store)
	m.sessions = []Session{
		{
			State:            session.State{ID: "s1", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
		}}

	usage := func(seq uint64) RunnerEventMsg {
		return RunnerEventMsg{ID: "s1", Event: mkEventSeq(seq, session.EventUsageUpdated, session.UsagePayload{InputTokens: int(seq)})}
	}

	m.handleRunnerEvent(usage(1)) // first non-transition save
	first := store.saves
	m.handleRunnerEvent(usage(2)) // within throttle window → no new save
	if store.saves != first {
		t.Errorf("usage save not throttled: saves went %d → %d", first, store.saves)
	}
	// The cursor still advances even though we didn't persist again.
	if m.sessions[0].lastSeq != 2 {
		t.Errorf("lastSeq = %d, want 2", m.sessions[0].lastSeq)
	}
}

// mkEventSeq builds a session.Event carrying a seq and JSON-encoded payload.
func mkEventSeq(seq uint64, typ session.EventType, payload interface{}) session.Event {
	ev := mkEvent(typ, payload)
	ev.Seq = seq
	return ev
}
