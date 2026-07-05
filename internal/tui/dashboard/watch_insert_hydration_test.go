package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1a step 6: when the watch informer beats the seed, the new-session insert
// path must hydrate lastSeq/seenSeq (and, for a running pod, full state) from
// the snapshot BEFORE starting the SSE stream — otherwise the stream resumes at
// after=0 and replays the entire history as if live.
func TestWatchInsertHydratesFromSnapshot(t *testing.T) {
	store := newFakeSnapshotStore()
	store.snaps["x"] = SessionSnapshot{LastSeq: 42, DashStatus: StatusBusy, InputTokens: 100}
	m := New(nil).WithSnapshotStore(store)

	// "x" is not yet in m.sessions — the informer insert races ahead of the seed.
	m.applyPodEvent(k8s.StateEvent{State: session.State{ID: "x", Status: session.StatusRunning}})

	s := m.sessionByID("x")
	if s.ID() != "x" {
		t.Fatal("watch insert did not add session x")
	}
	if s.lastSeq != 42 {
		t.Fatalf("watch-insert lastSeq=%d, want 42 (hydrated, not 0 → would full-replay)", s.lastSeq)
	}
	if s.seenSeq != 42 {
		t.Fatalf("watch-insert seenSeq=%d, want 42 (silent restore)", s.seenSeq)
	}
	if s.DashStatus != StatusBusy {
		t.Fatalf("running watch-insert should apply the snapshot status, got %v", s.DashStatus)
	}
	if s.InputTokens != 100 {
		t.Fatalf("watch-insert usage not hydrated: InputTokens=%d, want 100", s.InputTokens)
	}

	// A suspended pod: take only lastSeq; applySnapshot is skipped so the cached
	// running-status can't override the authoritative suspended state (C12).
	store.snaps["y"] = SessionSnapshot{LastSeq: 7, DashStatus: StatusBusy}
	m.applyPodEvent(k8s.StateEvent{State: session.State{ID: "y", Status: session.StatusSuspended}})
	sy := m.sessionByID("y")
	if sy.lastSeq != 7 {
		t.Fatalf("suspended watch-insert lastSeq=%d, want 7 (cursor still hydrated)", sy.lastSeq)
	}
	if sy.DashStatus == StatusBusy {
		t.Fatalf("suspended watch-insert must NOT apply the cached Busy status, got %v", sy.DashStatus)
	}
}
