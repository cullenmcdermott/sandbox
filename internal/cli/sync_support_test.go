package cli

import (
	"sync"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// [V2]/[V14] healEligible drives the prober's debounced self-heal. A safety halt
// must NEVER be heal-eligible (resuming it confirms a mass deletion); a plain
// transport stall and a paused-while-running sync must be.
func TestHealEligible(t *testing.T) {
	cases := []struct {
		st   syncpkg.SyncState
		heal bool
	}{
		{syncpkg.SyncStalled, true},       // MF5 transport stall
		{syncpkg.SyncPaused, true},        // [V14] failed best-effort resume
		{syncpkg.SyncSafetyHalted, false}, // [V2] must NOT auto-resume a safety halt
		{syncpkg.SyncConflicted, false},
		{syncpkg.SyncSynced, false},
		{syncpkg.SyncSyncing, false},
		{syncpkg.SyncUnknown, false},
	}
	for _, c := range cases {
		if got := healEligible(c.st); got != c.heal {
			t.Errorf("healEligible(%v) = %v, want %v", c.st, got, c.heal)
		}
	}
}

// [V7] A snapshot write that advances LastEventSeq must not be regressed by a
// concurrent (or interleaved) title write. SaveTitle now writes a PARTIAL entry
// and lets Save's locked merge preserve the newer cursor, and SaveSnapshot uses a
// locked read-modify-write, so neither clobbers the other.
func TestIndexStoreSnapshotSurvivesConcurrentTitle(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // newIndex() resolves the root from $HOME

	var titles indexTitleStore
	var snaps indexSnapshotStore
	id := session.ID("claude-sdk-race")

	// Establish a high cursor.
	snaps.SaveSnapshot(id, dashboard.SessionSnapshot{LastSeq: 5000})

	// Interleaved: SaveTitle (loads then writes) must not drop the cursor back.
	titles.SaveTitle(id, "user label")

	idx, err := newIndex()
	if err != nil {
		t.Fatal(err)
	}
	e, err := idx.Load(string(id))
	if err != nil {
		t.Fatal(err)
	}
	if e.LastEventSeq != 5000 {
		t.Fatalf("SaveTitle regressed LastEventSeq to %d (want 5000)", e.LastEventSeq)
	}
	if e.RenamedTitle != "user label" {
		t.Fatalf("RenamedTitle = %q, want %q", e.RenamedTitle, "user label")
	}

	// Concurrent stress: many title writes racing snapshot advances. The final
	// cursor must be the highest snapshot seq written, never a regressed value.
	const iters = 50
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			snaps.SaveSnapshot(id, dashboard.SessionSnapshot{LastSeq: uint64(5000 + i)})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			titles.SaveTitle(id, "t")
		}
	}()
	wg.Wait()

	e, err = idx.Load(string(id))
	if err != nil {
		t.Fatal(err)
	}
	if e.LastEventSeq < 5000 {
		t.Fatalf("concurrent title writes regressed LastEventSeq to %d (< 5000)", e.LastEventSeq)
	}
}
