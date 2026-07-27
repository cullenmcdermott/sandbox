package dashboard

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeCredRefresher is a scripted CredentialRefresher for App.Update tests. It
// records how many times Refresh was actually invoked so tests can assert the
// credRefreshing latch prevents a second concurrent handover, and returns
// noopExec (already defined in account_picker_test.go) as the command — the
// handover is never actually run in these tests, only built.
type fakeCredRefresher struct {
	needsRefresh bool
	refreshCalls int
	finalizeErr  error
}

func (f *fakeCredRefresher) NeedsRefresh(err error) bool { return f.needsRefresh }

func (f *fakeCredRefresher) Refresh() (tea.ExecCommand, func() error) {
	f.refreshCalls++
	return noopExec{}, func() error { return f.finalizeErr }
}

// TestCredRefreshMatchingErrorHandsOverAndKeepsNormalHandling pins the core
// wiring: a matching seedFailedMsg both spawns the terminal-handover command
// AND still reaches seedFailedMsg's own handler (m.seedErr stays set) — the
// handover is batched alongside, not substituted for, normal dispatch.
func TestCredRefreshMatchingErrorHandsOverAndKeepsNormalHandling(t *testing.T) {
	fake := &fakeCredRefresher{needsRefresh: true}
	app := &App{dashboard: New(nil), credRefresher: fake}

	wantErr := errors.New("certificate has expired")
	_, cmd := app.Update(seedFailedMsg{err: wantErr})

	if cmd == nil {
		t.Fatal("expected a non-nil command for a matching credential error")
	}
	if fake.refreshCalls != 1 {
		t.Fatalf("Refresh called %d times, want 1", fake.refreshCalls)
	}
	if !app.credRefreshing {
		t.Fatal("expected credRefreshing to latch")
	}
	if app.dashboard.seedErr == nil {
		t.Fatal("expected seedFailedMsg's normal handling (m.seedErr) to still run")
	}
}

// TestCredRefreshNonMatchingErrorNoHandover pins the conservative side: when
// NeedsRefresh says no, Refresh must never be called and the latch must not arm.
func TestCredRefreshNonMatchingErrorNoHandover(t *testing.T) {
	fake := &fakeCredRefresher{needsRefresh: false}
	app := &App{dashboard: New(nil), credRefresher: fake}

	app.Update(seedFailedMsg{err: errors.New("connection refused")})

	if fake.refreshCalls != 0 {
		t.Fatalf("Refresh called %d times, want 0", fake.refreshCalls)
	}
	if app.credRefreshing {
		t.Fatal("credRefreshing must not latch for a non-matching error")
	}
}

// TestCredRefreshLatchPreventsConcurrentHandover pins the error-storm guard: while
// a handover is already in flight (credRefreshing latched), a second matching
// error must not spawn a second Refresh.
func TestCredRefreshLatchPreventsConcurrentHandover(t *testing.T) {
	fake := &fakeCredRefresher{needsRefresh: true}
	app := &App{dashboard: New(nil), credRefresher: fake, credRefreshing: true}

	app.Update(seedFailedMsg{err: errors.New("certificate has expired")})

	if fake.refreshCalls != 0 {
		t.Fatalf("Refresh called %d times while latched, want 0", fake.refreshCalls)
	}
	if !app.credRefreshing {
		t.Fatal("expected credRefreshing to remain latched")
	}
}

// TestCredRefreshDoneSuccessClearsLatchAndReseeds pins handleCredRefreshDone's
// success path: the latch clears and a re-seed + restart-watch command is
// produced, mirroring the `r` retry key (model_input.go dashListTable).
func TestCredRefreshDoneSuccessClearsLatchAndReseeds(t *testing.T) {
	app := &App{dashboard: New(nil), credRefreshing: true, credRefreshErr: errors.New("stale")}

	_, cmd := app.Update(credRefreshDoneMsg{})

	if app.credRefreshing {
		t.Fatal("expected credRefreshing to clear")
	}
	if cmd == nil {
		t.Fatal("expected a re-seed command on success")
	}
	if app.credRefreshErr != nil {
		t.Fatalf("expected credRefreshErr cleared, got %v", app.credRefreshErr)
	}
}

// TestCredRefreshDoneFailureSurfacesError pins handleCredRefreshDone's failure
// path: the latch still clears, and the error is stored (credRefreshErr) and
// surfaced through the same connectErr field a connector failure uses.
func TestCredRefreshDoneFailureSurfacesError(t *testing.T) {
	app := &App{dashboard: New(nil), credRefreshing: true}
	wantErr := errors.New("tsh kube credentials: still expired")

	app.Update(credRefreshDoneMsg{err: wantErr})

	if app.credRefreshing {
		t.Fatal("expected credRefreshing to clear even on failure")
	}
	if !errors.Is(app.credRefreshErr, wantErr) && app.credRefreshErr != wantErr {
		t.Fatalf("credRefreshErr = %v, want %v", app.credRefreshErr, wantErr)
	}
	if app.connectErr != wantErr {
		t.Fatalf("connectErr = %v, want %v", app.connectErr, wantErr)
	}
	if app.dashboard.connectErr != wantErr {
		t.Fatalf("dashboard.connectErr = %v, want %v", app.dashboard.connectErr, wantErr)
	}
}

// TestCredRefreshBudgetStopsTerminalThrash pins the sequential-handover cap.
// The loop it guards needs only a plugin that exits ZERO while leaving the
// credential bad: handover "succeeds" → re-seed → same failure → handover …
// forever, with the terminal taken over each time. credRefreshing does NOT
// cover this (each handover finishes before the next error arrives), so this is
// the test that would catch its removal.
func TestCredRefreshBudgetStopsTerminalThrash(t *testing.T) {
	fake := &fakeCredRefresher{needsRefresh: true}
	app := &App{dashboard: New(nil), credRefresher: fake}

	// Each round: a cluster failure hands the terminal over, the plugin exits
	// clean, we re-seed — and the very next seed fails identically.
	for i := 0; i < maxCredRefreshAttempts+3; i++ {
		app.Update(seedFailedMsg{err: errors.New("certificate has expired")})
		app.Update(credRefreshDoneMsg{})
	}

	if fake.refreshCalls != maxCredRefreshAttempts {
		t.Fatalf("Refresh called %d times across %d failing rounds, want the cap of %d",
			fake.refreshCalls, maxCredRefreshAttempts+3, maxCredRefreshAttempts)
	}
}

// TestCredRefreshBudgetRearmsOnClusterSuccess pins the other half: the budget
// resets only when a cluster call actually SUCCEEDS, so a later, genuine expiry
// still gets its handover. A successful handover must not itself re-arm — "the
// plugin ran" is not evidence the credential works, and counting it would
// re-open the loop the cap exists to close.
func TestCredRefreshBudgetRearmsOnClusterSuccess(t *testing.T) {
	fake := &fakeCredRefresher{needsRefresh: true}
	app := &App{dashboard: New(nil), credRefresher: fake}

	for i := 0; i < maxCredRefreshAttempts; i++ {
		app.Update(seedFailedMsg{err: errors.New("certificate has expired")})
		app.Update(credRefreshDoneMsg{})
	}
	if fake.refreshCalls != maxCredRefreshAttempts {
		t.Fatalf("setup: Refresh called %d times, want %d", fake.refreshCalls, maxCredRefreshAttempts)
	}

	// Budget exhausted: another failure must NOT hand over.
	app.Update(seedFailedMsg{err: errors.New("certificate has expired")})
	if fake.refreshCalls != maxCredRefreshAttempts {
		t.Fatalf("Refresh called %d times past the cap, want %d", fake.refreshCalls, maxCredRefreshAttempts)
	}

	// An action that SUCCEEDS proves the credential works and re-arms the budget.
	app.Update(actionResultMsg{action: "suspend", id: "s1"})
	if app.credRefreshAttempts != 0 {
		t.Fatalf("credRefreshAttempts = %d after a cluster success, want 0", app.credRefreshAttempts)
	}

	app.Update(seedFailedMsg{err: errors.New("certificate has expired")})
	if fake.refreshCalls != maxCredRefreshAttempts+1 {
		t.Fatalf("Refresh called %d times, want %d — a re-armed budget must allow a fresh handover",
			fake.refreshCalls, maxCredRefreshAttempts+1)
	}
}

// TestCredRefreshProgressTickIsNotClusterSuccess pins the distinction
// credErrorFrom's second return exists for: a connectUpdateMsg carrying only a
// stage/detail tick says nothing about the cluster, so it must not re-arm the
// budget. (A ready connectUpdateMsg does — that connect completed.)
func TestCredRefreshProgressTickIsNotClusterSuccess(t *testing.T) {
	app := &App{dashboard: New(nil), credRefreshAttempts: maxCredRefreshAttempts}

	stage := StageForward
	app.Update(connectUpdateMsg{stage: &stage, detail: "forwarding"})
	if app.credRefreshAttempts != maxCredRefreshAttempts {
		t.Fatalf("a progress tick re-armed the budget: credRefreshAttempts = %d, want %d",
			app.credRefreshAttempts, maxCredRefreshAttempts)
	}

	app.Update(connectUpdateMsg{ready: &attachReadyMsg{}})
	if app.credRefreshAttempts != 0 {
		t.Fatalf("a completed connect did not re-arm the budget: credRefreshAttempts = %d, want 0",
			app.credRefreshAttempts)
	}
}

// TestCredRefreshNilRefresherIsNoop pins the disabled-by-default case: a nil
// CredentialRefresher must never latch, panic, or otherwise change behavior.
func TestCredRefreshNilRefresherIsNoop(t *testing.T) {
	app := &App{dashboard: New(nil)}

	app.Update(seedFailedMsg{err: errors.New("certificate has expired")})

	if app.credRefreshing {
		t.Fatal("nil refresher must never latch")
	}
	if app.dashboard.seedErr == nil {
		t.Fatal("expected seedFailedMsg's normal handling to still run")
	}
}
