package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1a step 5: a cluster re-List (applySeed) of a warm session must carry seenSeq
// forward, not reset it to 0 while carrying lastSeq — otherwise every re-seed
// resurrects a phantom lifetime-event-count unread badge.
func TestApplySeedCarriesSeenSeqForward(t *testing.T) {
	m := New(nil)
	prev := SessionFromState(session.State{ID: "s1", Status: session.StatusRunning})
	prev.lastSeq = 10
	prev.seenSeq = 4
	m.sessions = []Session{prev}

	m, _ = m.applySeed([]session.State{{ID: "s1", Status: session.StatusRunning}})

	s := m.sessionByID("s1")
	if s.seenSeq != 4 {
		t.Fatalf("re-seed reset seenSeq to %d, want 4 (carried forward)", s.seenSeq)
	}
	if s.Unread() != 6 {
		t.Fatalf("Unread()=%d after re-seed, want 6 (10-4), not the lifetime count", s.Unread())
	}
}

// §1b related: a re-seed must also carry the SSE-accumulated live state
// (usage/cost/model/branch/recent-tools) forward, not reset it to the
// cluster-derived zero (a phantom "just started" row).
func TestApplySeedCarriesLiveStateForward(t *testing.T) {
	m := New(nil)
	prev := SessionFromState(session.State{ID: "s1", Status: session.StatusRunning})
	prev.lastSeq = 10
	prev.InputTokens, prev.OutputTokens = 1234, 567
	prev.TotalCostUSD = 0.42
	prev.Model = "claude-opus-4-8"
	prev.Branch = "feature/x"
	prev.RecentTools = []ToolRef{{}}
	m.sessions = []Session{prev}

	m, _ = m.applySeed([]session.State{{ID: "s1", Status: session.StatusRunning}})

	s := m.sessionByID("s1")
	if s.InputTokens != 1234 || s.OutputTokens != 567 || s.TotalCostUSD != 0.42 {
		t.Fatalf("usage not carried: in=%d out=%d cost=%v", s.InputTokens, s.OutputTokens, s.TotalCostUSD)
	}
	if s.Model != "claude-opus-4-8" || s.Branch != "feature/x" {
		t.Fatalf("model/branch not carried: model=%q branch=%q", s.Model, s.Branch)
	}
	if len(s.RecentTools) != 1 {
		t.Fatalf("RecentTools dropped on re-seed: len=%d, want 1", len(s.RecentTools))
	}
}

// Cold hydrate from a snapshot must treat all restored history as already-seen,
// so a relaunch shows Unread()==0 immediately (silent restore), for both a
// running pod (via applySnapshot) and a suspended pod (which skips it).
func TestApplySnapshotHydrateMarksAllSeen(t *testing.T) {
	// Running: hydrated via applySnapshot.
	store := newFakeSnapshotStore()
	store.snaps["run"] = SessionSnapshot{LastSeq: 10, DashStatus: StatusIdle}
	m := New(nil).WithSnapshotStore(store)
	m, _ = m.applySeed([]session.State{{ID: "run", Status: session.StatusRunning}})
	if s := m.sessionByID("run"); s.seenSeq != 10 || s.Unread() != 0 {
		t.Fatalf("running hydrate: seenSeq=%d Unread=%d, want 10/0", s.seenSeq, s.Unread())
	}

	// Suspended: applySnapshot is skipped, but seenSeq must still be restored.
	store2 := newFakeSnapshotStore()
	store2.snaps["susp"] = SessionSnapshot{LastSeq: 8, DashStatus: StatusBusy}
	m2 := New(nil).WithSnapshotStore(store2)
	m2, _ = m2.applySeed([]session.State{{ID: "susp", Status: session.StatusSuspended}})
	if s := m2.sessionByID("susp"); s.seenSeq != 8 || s.Unread() != 0 {
		t.Fatalf("suspended hydrate: seenSeq=%d Unread=%d, want 8/0", s.seenSeq, s.Unread())
	}
}
