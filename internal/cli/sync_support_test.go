package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// resetTitleClientForTest clears the package-level cached offline title client
// (see offlineTitleClient) so a test that redirects $HOME gets a fresh client
// bound to its OWN temp index, instead of silently reusing whatever client (and
// $HOME) an earlier test in this package cached first.
func resetTitleClientForTest() {
	titleClientOnce = sync.Once{}
	titleClient = nil
	titleClientErr = nil
}

// [V2]/[V14] healEligible drives the prober's debounced self-heal. A safety halt
// must NEVER be heal-eligible (resuming it confirms a mass deletion); a plain
// transport stall and a paused-while-running sync must be.
func TestHealEligible(t *testing.T) {
	cases := []struct {
		st   client.SyncState
		heal bool
	}{
		{client.SyncStalled, true},       // MF5 transport stall
		{client.SyncPaused, true},        // [V14] failed best-effort resume
		{client.SyncSafetyHalted, false}, // [V2] must NOT auto-resume a safety halt
		{client.SyncConflicted, false},
		{client.SyncSynced, false},
		{client.SyncSyncing, false},
		{client.SyncUnknown, false},
	}
	for _, c := range cases {
		if got := healEligible(c.st); got != c.heal {
			t.Errorf("healEligible(%v) = %v, want %v", c.st, got, c.heal)
		}
	}
}

// writeFakeMutagen writes an executable /bin/sh script literally named
// "mutagen" under a fresh t.TempDir() and returns the directory holding it, so
// a test can point PATH at just that directory and have the real ExecRunner
// (ultimately client.SyncStatus -> internal/sync's StatusReport) resolve and
// run it.
func writeFakeMutagen(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "mutagen")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake mutagen: %v", err)
	}
	return dir
}

// syncStatusProber builds a syncProber backed by an OFFLINE client whose
// mutagen exec resolves through PATH=bindir — either a directory holding a
// fake "mutagen" script, or an empty directory (nothing found).
func syncStatusProber(t *testing.T, bindir string) *syncProber {
	t.Helper()
	t.Setenv("PATH", bindir)
	c, err := client.Offline(client.WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("client.Offline: %v", err)
	}
	return &syncProber{c: c, lastHealAt: make(map[session.ID]time.Time)}
}

// TestSyncProberSurfacesUnderlyingProbeErrorReason ports the reason-shaping
// assertions of the deleted internal/cli TestProbeErrDetail: probeErrDetail
// itself moved to internal/sync/status.go (called from client.SyncStatus's
// StatusReport), and internal/sync's status_test.go has no case at all
// exercising StatusReport's err != nil branch. Rather than calling the
// now-private probeErrDetail directly, this drives the SAME scenarios end to
// end through the public client + a real (fake) "mutagen" binary on PATH, and
// asserts on what the prober's probe() surfaces in dashboard.SyncHealth.Detail
// — proving a missing binary names itself, the argv echo is dropped,
// multi-line stderr collapses to one line, and a long reason is capped with an
// elision mark. It still degrades to "unknown"/never blocks the UI throughout.
//
// One case from the original table — an "unstructured" error with no "]: "
// marker at all (e.g. a bare errors.New("boom")) — is NOT reproducible this
// way: the real ExecRunner always wraps a failure as "mutagen [<argv>]: <err>:
// <msg>", so that shape is unreachable from the production entry point. It
// remains an untested defensive-fallback branch of probeErrDetail; internal/sync
// (which owns the function now) has no direct unit test for it either.
func TestSyncProberSurfacesUnderlyingProbeErrorReason(t *testing.T) {
	t.Run("missing binary names itself", func(t *testing.T) {
		// The single most likely cause, and the only one with an obvious fix.
		// An empty PATH directory means "mutagen" resolves nowhere.
		p := syncStatusProber(t, t.TempDir())
		h := p.probe(context.Background(), session.ID("sess-1"))
		if h.Status != "unknown" {
			t.Errorf("Status = %q, want %q", h.Status, "unknown")
		}
		if h.Detail != "mutagen CLI not found" {
			t.Errorf("Detail = %q, want %q", h.Detail, "mutagen CLI not found")
		}
	})

	t.Run("argv echo dropped, stderr kept", func(t *testing.T) {
		// The argv echo is noise the user can do nothing with; the stderr after
		// it is the actionable part.
		bin := writeFakeMutagen(t, `echo "unable to connect to daemon" 1>&2; exit 1`)
		p := syncStatusProber(t, bin)
		h := p.probe(context.Background(), session.ID("sess-1"))
		if want := "exit status 1: unable to connect to daemon"; h.Detail != want {
			t.Errorf("Detail = %q, want %q", h.Detail, want)
		}
	})

	t.Run("multi-line stderr collapses to one line", func(t *testing.T) {
		bin := writeFakeMutagen(t, `printf 'first line\n\tsecond line' 1>&2; exit 1`)
		p := syncStatusProber(t, bin)
		h := p.probe(context.Background(), session.ID("sess-1"))
		if want := "exit status 1: first line second line"; h.Detail != want {
			t.Errorf("Detail = %q, want %q", h.Detail, want)
		}
	})

	t.Run("long reason is capped and marked elided", func(t *testing.T) {
		bin := writeFakeMutagen(t, `printf '`+strings.Repeat("x", 200)+`' 1>&2; exit 1`)
		p := syncStatusProber(t, bin)
		h := p.probe(context.Background(), session.ID("sess-1"))
		if len([]rune(h.Detail)) > 56 {
			t.Errorf("Detail not capped: %d runes: %q", len([]rune(h.Detail)), h.Detail)
		}
		if !strings.HasSuffix(h.Detail, "…") {
			t.Errorf("a clamped reason should be marked elided, got %q", h.Detail)
		}
	})
}

// [V7] A snapshot write that advances LastEventSeq must not be regressed by a
// concurrent (or interleaved) title write. SaveTitle now writes a PARTIAL entry
// and lets Save's locked merge preserve the newer cursor, and SaveSnapshot uses a
// locked read-modify-write, so neither clobbers the other.
func TestIndexStoreSnapshotSurvivesConcurrentTitle(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // newIndex() resolves the root from $HOME
	// The title store now goes through a package-level cached offline client
	// (offlineTitleClient); clear it so THIS test's client is bound to the
	// $HOME just set above, not to whatever an earlier test in this package
	// cached first.
	resetTitleClientForTest()

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
