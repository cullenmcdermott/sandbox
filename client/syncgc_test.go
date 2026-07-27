package client

import (
	"context"
	"errors"
	"io"
	"testing"

	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
)

// TestSelectOrphanSyncs covers the MF3 context-scoped orphan selection: only
// transport-down syncs, only sessions absent from the current context's live
// set, and — the fix — never a sync a DIFFERENT context created. Copied from
// internal/cli/commands_test.go's TestSelectOrphanSyncs (promoted to client per
// docs/design-principles.md — an SDK importer gets the same orphan-GC policy
// `sandbox sync gc` uses, not a re-derived one).
func TestSelectOrphanSyncs(t *testing.T) {
	const currentCtx = "my-cluster"
	const currentNs = "agent-sessions"
	syncs := []syncpkg.SyncSession{
		// current ctx+ns, gone session, orphaned → reap
		{SessionID: "gone", Context: currentCtx, Namespace: currentNs, Identifier: "sync_gone", Status: "ConnectingBeta"},
		// current ctx+ns, live session, orphaned → keep (reconnecting)
		{SessionID: "live", Context: currentCtx, Namespace: currentNs, Identifier: "sync_live", Status: "ConnectingBeta"},
		// current ctx+ns, gone session, actively watching → keep (not orphaned)
		{SessionID: "busy", Context: currentCtx, Namespace: currentNs, Identifier: "sync_busy", Status: "Watching"},
		// DIFFERENT ctx, not in this cluster's live set, orphaned → keep (MF3)
		{SessionID: "other", Context: "staging", Namespace: currentNs, Identifier: "sync_other", Status: "Disconnected"},
		// DIFFERENT namespace, same ctx, gone from THIS ns's live set → keep ([V28])
		{SessionID: "otherns", Context: currentCtx, Namespace: "team-b", Identifier: "sync_otherns", Status: "Disconnected"},
		// legacy (no ctx/ns label), gone session, orphaned → reap (migration fallback)
		{SessionID: "legacy", Context: "", Namespace: "", Identifier: "sync_legacy", Status: "Disconnected"},
		// [V35] paused sync whose session is GONE → reap (kubectl-deleted suspended)
		{SessionID: "pausedgone", Context: currentCtx, Namespace: currentNs, Identifier: "sync_pausedgone", Status: "Paused"},
		// [V35] paused sync whose session STILL EXISTS (merely suspended) → keep
		{SessionID: "pausedlive", Context: currentCtx, Namespace: currentNs, Identifier: "sync_pausedlive", Status: "Paused"},
	}
	live := map[string]bool{"live": true, "pausedlive": true}

	orphanIDs, bySession := selectOrphanSyncs(syncs, live, currentCtx, currentNs)

	got := map[string]bool{}
	for _, id := range orphanIDs {
		got[id] = true
	}
	want := map[string]bool{"sync_gone": true, "sync_legacy": true, "sync_pausedgone": true}
	if len(got) != len(want) {
		t.Fatalf("selected %v, want %v", orphanIDs, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("expected %s to be selected for reap", id)
		}
	}
	if got["sync_other"] {
		t.Error("MF3 violation: a different context's sync was selected for reap")
	}
	if got["sync_otherns"] {
		t.Error("[V28] violation: a different namespace's sync was selected for reap")
	}
	if got["sync_pausedlive"] {
		t.Error("[V35] violation: a suspended-but-alive session's paused sync was reaped")
	}
	if got["sync_live"] || got["sync_busy"] {
		t.Error("a live or actively-syncing sync was wrongly selected")
	}
	if bySession["gone"] != 1 || bySession["legacy"] != 1 || bySession["pausedgone"] != 1 {
		t.Errorf("per-session counts wrong: %v", bySession)
	}
}

// gcRunner is a fake syncpkg.Runner for SyncGC tests: it answers `sync list`
// with a canned template row set and records every `sync terminate` invocation,
// so a test can assert exactly which identifiers were (or were not) terminated.
type gcRunner struct {
	listOut        string
	listErr        error
	terminatedArgs [][]string
}

func (r *gcRunner) Output(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "sync" && args[1] == "list" {
		if r.listErr != nil {
			return nil, r.listErr
		}
		return []byte(r.listOut), nil
	}
	if len(args) >= 2 && args[0] == "sync" && args[1] == "terminate" {
		r.terminatedArgs = append(r.terminatedArgs, args)
		return nil, nil
	}
	return nil, nil
}

// gcSyncRow renders one syncListTemplate line (see internal/sync's
// syncListTemplate: sessionID|context|identifier|name|status|namespace). Context
// and namespace are left empty (the "legacy" shape) so the row's reapability
// depends only on the live set, independent of whatever kube context/namespace
// this test happens to resolve to.
func gcSyncRow(sessionID, identifier, status string) string {
	return sessionID + "||" + identifier + "|sandbox-" + sessionID + "-project|" + status + "|"
}

func gcClient(t *testing.T, be *fakeBackend, runner *gcRunner) *Client {
	t.Helper()
	c, err := New(WithBackend(be), WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.syncRunner = runner
	return c
}

func TestSyncGCRefusesOnUnlistableCluster(t *testing.T) {
	be := newFakeBackend()
	be.listErr = errors.New("apiserver unreachable")
	runner := &gcRunner{listOut: gcSyncRow("gone", "sync_gone", "Disconnected")}
	c := gcClient(t, be, runner)

	res, err := c.SyncGC(context.Background(), SyncGCOptions{})
	if err == nil {
		t.Fatal("expected an error when the cluster can't be listed")
	}
	if res.Terminated {
		t.Error("Terminated must be false when the sweep refused to run")
	}
	if len(runner.terminatedArgs) != 0 {
		t.Errorf("terminate must not be called when the cluster is unlistable, got %v", runner.terminatedArgs)
	}
}

func TestSyncGCDryRunReportsWithoutTerminating(t *testing.T) {
	be := newFakeBackend() // listStates empty: no live sessions
	runner := &gcRunner{listOut: gcSyncRow("gone", "sync_gone", "Disconnected")}
	c := gcClient(t, be, runner)

	res, err := c.SyncGC(context.Background(), SyncGCOptions{DryRun: true})
	if err != nil {
		t.Fatalf("SyncGC dry-run: %v", err)
	}
	if res.Terminated {
		t.Error("Terminated must be false for a DryRun sweep")
	}
	if res.Orphans != 1 || res.BySession[ID("gone")] != 1 {
		t.Errorf("Orphans/BySession = %d/%v, want 1/{gone:1}", res.Orphans, res.BySession)
	}
	if len(runner.terminatedArgs) != 0 {
		t.Errorf("DryRun must never call terminate, got %v", runner.terminatedArgs)
	}
}

func TestSyncGCTerminatesExactlyTheOrphans(t *testing.T) {
	be := newFakeBackend() // no live sessions
	runner := &gcRunner{
		listOut: gcSyncRow("gone", "sync_gone", "Disconnected") + "\n" +
			gcSyncRow("also-gone", "sync_also_gone", "ConnectingBeta"),
	}
	c := gcClient(t, be, runner)

	res, err := c.SyncGC(context.Background(), SyncGCOptions{})
	if err != nil {
		t.Fatalf("SyncGC: %v", err)
	}
	if !res.Terminated {
		t.Error("Terminated must be true for a real (non-DryRun) sweep with orphans")
	}
	if res.Orphans != 2 {
		t.Errorf("Orphans = %d, want 2", res.Orphans)
	}
	if len(runner.terminatedArgs) != 1 {
		t.Fatalf("expected exactly one terminate invocation, got %d: %v", len(runner.terminatedArgs), runner.terminatedArgs)
	}
	got := map[string]bool{}
	for _, arg := range runner.terminatedArgs[0][2:] { // skip "sync","terminate"
		got[arg] = true
	}
	if !got["sync_gone"] || !got["sync_also_gone"] {
		t.Errorf("terminate args = %v, want both sync_gone and sync_also_gone", runner.terminatedArgs[0])
	}
	if len(got) != 2 {
		t.Errorf("terminate args = %v, want exactly the 2 orphan identifiers", runner.terminatedArgs[0])
	}
}
