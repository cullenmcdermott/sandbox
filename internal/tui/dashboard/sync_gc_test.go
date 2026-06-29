package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

type fakeReaper struct {
	orphans    []OrphanSync
	terminated [][]string
}

func (f *fakeReaper) ListOrphans(context.Context) ([]OrphanSync, error) { return f.orphans, nil }
func (f *fakeReaper) Terminate(_ context.Context, ids []string) error {
	f.terminated = append(f.terminated, append([]string(nil), ids...))
	return nil
}

// Sync GC reliability — the load-bearing safety properties:
//   - a LIVE/suspended session's sync (still in the dashboard set) is NEVER reaped;
//   - a GONE session's sync is reaped only after the grace window (so a fresh
//     session still connecting, or a transient blip, survives);
//   - recovered orphans are dropped from the grace map (no unbounded growth).
func TestReapOrphans_GracePolicyAndLiveProtection(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	defer func(orig func() time.Time) { nowFunc = orig }(nowFunc)
	nowFunc = func() time.Time { return cur }

	reaper := &fakeReaper{}
	m := New(nil)
	m.WithSyncReaper(reaper)
	// gcRunning is the authoritative "pod is up" set the reconcile installs: only
	// "live-1" has a running pod. "gone-1" is absent (gone from the cluster).
	m.gcRunning = map[session.ID]bool{"live-1": true}

	orphans := []OrphanSync{
		{Identifier: "sync_gone", SessionID: "gone-1"}, // no live pod → eligible after grace
		{Identifier: "sync_live", SessionID: "live-1"}, // pod running → protected forever
	}
	exec := func() {
		if c := m.reapOrphans(orphans); c != nil {
			c()
		}
	}

	// Pass 1: starts gone-1's grace clock; live-1 protected. Nothing terminated.
	exec()
	if len(reaper.terminated) != 0 {
		t.Fatalf("nothing should terminate on first sighting, got %v", reaper.terminated)
	}
	if _, ok := m.orphanSince["sync_live"]; ok {
		t.Error("a live session's sync must never enter the grace map (would risk reaping it)")
	}
	if _, ok := m.orphanSince["sync_gone"]; !ok {
		t.Error("a gone session's sync should start the grace clock")
	}

	// Pass 2: just before grace elapses → still nothing.
	cur = base.Add(syncGCGrace - time.Second)
	exec()
	if len(reaper.terminated) != 0 {
		t.Fatalf("must not terminate before the grace window elapses, got %v", reaper.terminated)
	}

	// Pass 3: past grace → gone-1's sync is reaped, live-1's never is.
	cur = base.Add(syncGCGrace + time.Second)
	exec()
	if len(reaper.terminated) != 1 || len(reaper.terminated[0]) != 1 || reaper.terminated[0][0] != "sync_gone" {
		t.Fatalf("expected exactly sync_gone reaped, got %v", reaper.terminated)
	}
	for _, batch := range reaper.terminated {
		for _, id := range batch {
			if id == "sync_live" {
				t.Fatal("reaped a LIVE session's sync — data-loss bug")
			}
		}
	}
}

// MF1 regression: a SUSPENDED (or failed) session — present in the cluster but
// with no live pod, so absent from gcRunning — must have its thrashing orphan
// syncs reaped after grace. The idle reaper sets replicas=0 from inside the
// cluster and cannot pause the host sync, so these are the dominant leak source;
// protecting them merely because the Sandbox still exists was the original bug.
func TestReapOrphans_SuspendedSessionIsReaped(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	defer func(orig func() time.Time) { nowFunc = orig }(nowFunc)
	nowFunc = func() time.Time { return cur }

	reaper := &fakeReaper{}
	m := New(nil)
	m.WithSyncReaper(reaper)
	// The session still EXISTS (it's a suspended Sandbox, present in m.sessions) but
	// its pod is down, so the reconcile left it OUT of gcRunning.
	m.sessions = []Session{{State: session.State{ID: "susp-1", Status: session.StatusSuspended}}}
	m.gcRunning = map[session.ID]bool{} // no running pods

	orphans := []OrphanSync{{Identifier: "sync_susp", SessionID: "susp-1"}}
	exec := func() {
		if c := m.reapOrphans(orphans); c != nil {
			c()
		}
	}
	exec() // grace starts (must NOT be protected just because the Sandbox exists)
	if _, ok := m.orphanSince["sync_susp"]; !ok {
		t.Fatal("a suspended session's orphan must accrue the grace clock, not be protected")
	}
	cur = base.Add(syncGCGrace + time.Second)
	exec()
	if len(reaper.terminated) != 1 || reaper.terminated[0][0] != "sync_susp" {
		t.Fatalf("a suspended session's orphan sync must be reaped after grace (the idle-reaper leak); got %v", reaper.terminated)
	}
}

// A gone session's sync that RECOVERS (drops out of the orphan list before grace
// elapses, e.g. the session was recreated / the transport reconnected) is dropped
// from the grace map and never reaped — the clock resets if it reappears.
func TestReapOrphans_RecoveredOrphanResetsGrace(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	defer func(orig func() time.Time) { nowFunc = orig }(nowFunc)
	nowFunc = func() time.Time { return cur }

	reaper := &fakeReaper{}
	m := New(nil)
	m.WithSyncReaper(reaper)
	m.sessions = nil // no live sessions

	orphan := []OrphanSync{{Identifier: "sync_x", SessionID: "gone-x"}}
	if c := m.reapOrphans(orphan); c != nil {
		c()
	}
	if _, ok := m.orphanSince["sync_x"]; !ok {
		t.Fatal("grace clock should have started")
	}

	// It recovers (empty orphan list) → dropped from the grace map.
	if c := m.reapOrphans(nil); c != nil {
		c()
	}
	if _, ok := m.orphanSince["sync_x"]; ok {
		t.Error("a recovered orphan must be dropped from the grace map")
	}

	// It reappears later — the clock restarts (not reaped immediately even though
	// wall-clock has advanced past grace since the FIRST sighting).
	cur = base.Add(2 * syncGCGrace)
	if c := m.reapOrphans(orphan); c != nil {
		c()
	}
	if len(reaper.terminated) != 0 {
		t.Fatalf("a reappeared orphan must restart the grace clock, not reap immediately; got %v", reaper.terminated)
	}
}

// With no reaper configured (unit-test / no-backend default) the GC is a no-op:
// gcListOrphansCmd returns nil so the reconcile path stays inert.
func TestSyncGC_NoReaperIsNoop(t *testing.T) {
	m := New(nil)
	if cmd := m.gcListOrphansCmd(); cmd != nil {
		t.Error("gcListOrphansCmd must be nil without a reaper")
	}
}
