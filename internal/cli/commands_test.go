package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
)

// TestConfirmDestroyGate exercises the destroy command's interactive
// confirmation (C4). Without --force, only an explicit "y"/"Y" may proceed; any
// other answer (or empty input / EOF) must deny and print "Cancelled.". Driving
// confirmDestroy directly keeps the test off the cluster (the proceed path
// would otherwise call newBackend()/Destroy).
func TestConfirmDestroyGate(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantProceed bool
	}{
		{"explicit no", "n\n", false},
		{"empty / EOF", "", false},
		{"capital no", "N\n", false},
		{"unrelated answer", "maybe\n", false},
		{"lowercase yes", "y\n", true},
		{"capital yes", "Y\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			got := confirmDestroy(strings.NewReader(c.input), &out, "some-session")
			if got != c.wantProceed {
				t.Errorf("confirmDestroy(%q) = %v, want %v", c.input, got, c.wantProceed)
			}
			gotCancelled := strings.Contains(out.String(), "Cancelled.")
			if gotCancelled == c.wantProceed {
				t.Errorf("input %q: printed-Cancelled=%v but proceed=%v (output: %q)", c.input, gotCancelled, c.wantProceed, out.String())
			}
		})
	}
}

// fakeTurnClient is a test double for turnStateClient (NEW-9).
type fakeTurnClient struct {
	state        session.State
	stateErr     error
	interruptErr error

	interruptedWith *session.TurnRef // captures the TurnRef passed to InterruptTurn
}

func (f *fakeTurnClient) SessionState(_ context.Context, _ session.Ref) (session.State, error) {
	return f.state, f.stateErr
}

func (f *fakeTurnClient) InterruptTurn(_ context.Context, _ session.Ref, turn session.TurnRef) error {
	f.interruptedWith = &turn
	return f.interruptErr
}

// TestCancelActiveTurnInterruptsLiveTurn is the NEW-9 regression guard: cancel
// must read the active turn id from the runner (SessionState) and interrupt it.
// Before the fix it read LastTurnID from the k8s CRD (always ""), so cancel
// could never reach InterruptTurn.
func TestCancelActiveTurnInterruptsLiveTurn(t *testing.T) {
	ref := session.Ref{ID: "s1"}
	client := &fakeTurnClient{
		state: session.State{ID: "s1", LastTurnID: "turn-42", ActiveTurnID: "turn-42"},
	}

	if err := cancelActiveTurn(context.Background(), client, ref); err != nil {
		t.Fatalf("cancelActiveTurn returned error: %v", err)
	}
	if client.interruptedWith == nil {
		t.Fatal("InterruptTurn was never called (the original bug: cancel returned 'no active turn' unconditionally)")
	}
	if client.interruptedWith.Turn != "turn-42" {
		t.Errorf("interrupted turn = %q, want %q", client.interruptedWith.Turn, "turn-42")
	}
	if client.interruptedWith.Session != "s1" {
		t.Errorf("interrupted session = %q, want %q", client.interruptedWith.Session, "s1")
	}
}

// TestCancelActiveTurnNoActiveTurn: when the runner reports no active turn,
// cancel errors and does NOT call InterruptTurn.
func TestCancelActiveTurnNoActiveTurn(t *testing.T) {
	// LastTurnID persists after a turn finishes; only an empty ActiveTurnID
	// means "nothing running". Set LastTurnID to prove cancel keys off the
	// right field.
	client := &fakeTurnClient{state: session.State{ID: "s1", LastTurnID: "turn-42", ActiveTurnID: ""}}

	err := cancelActiveTurn(context.Background(), client, session.Ref{ID: "s1"})
	if err == nil {
		t.Fatal("expected an error when there is no active turn, got nil")
	}
	if client.interruptedWith != nil {
		t.Error("InterruptTurn must not be called when there is no active turn")
	}
}

// TestCancelActiveTurnPropagatesStateError: a runner SessionState failure is
// surfaced, not swallowed into a misleading "no active turn".
func TestCancelActiveTurnPropagatesStateError(t *testing.T) {
	sentinel := errors.New("runner unreachable")
	client := &fakeTurnClient{stateErr: sentinel}

	err := cancelActiveTurn(context.Background(), client, session.Ref{ID: "s1"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the SessionState error to propagate, got %v", err)
	}
	if client.interruptedWith != nil {
		t.Error("InterruptTurn must not be called when SessionState failed")
	}
}

// TestSelectOrphanSyncs covers the MF3 context-scoped orphan selection: only
// transport-down syncs, only sessions absent from the current context's live
// set, and — the fix — never a sync a DIFFERENT context created.
func TestSelectOrphanSyncs(t *testing.T) {
	const currentCtx = "omni-prod"
	syncs := []syncpkg.SyncSession{
		// current ctx, gone session, orphaned → reap
		{SessionID: "gone", Context: currentCtx, Identifier: "sync_gone", Status: "ConnectingBeta"},
		// current ctx, live session, orphaned → keep (reconnecting)
		{SessionID: "live", Context: currentCtx, Identifier: "sync_live", Status: "ConnectingBeta"},
		// current ctx, gone session, actively watching → keep (not orphaned)
		{SessionID: "busy", Context: currentCtx, Identifier: "sync_busy", Status: "Watching"},
		// DIFFERENT ctx, not in this cluster's live set, orphaned → keep (MF3)
		{SessionID: "other", Context: "staging", Identifier: "sync_other", Status: "Disconnected"},
		// legacy (no ctx label), gone session, orphaned → reap (migration fallback)
		{SessionID: "legacy", Context: "", Identifier: "sync_legacy", Status: "Disconnected"},
	}
	live := map[string]bool{"live": true}

	orphanIDs, bySession := selectOrphanSyncs(syncs, live, currentCtx)

	got := map[string]bool{}
	for _, id := range orphanIDs {
		got[id] = true
	}
	want := map[string]bool{"sync_gone": true, "sync_legacy": true}
	if len(got) != len(want) {
		t.Fatalf("selected %v, want %v", orphanIDs, []string{"sync_gone", "sync_legacy"})
	}
	for id := range want {
		if !got[id] {
			t.Errorf("expected %s to be selected for reap", id)
		}
	}
	if got["sync_other"] {
		t.Error("MF3 violation: a different context's sync was selected for reap")
	}
	if got["sync_live"] || got["sync_busy"] {
		t.Error("a live or actively-syncing sync was wrongly selected")
	}
	if bySession["gone"] != 1 || bySession["legacy"] != 1 {
		t.Errorf("per-session counts wrong: %v", bySession)
	}
}
