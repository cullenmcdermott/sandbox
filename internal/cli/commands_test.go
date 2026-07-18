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
		// [V27] A bare Enter (empty line, not EOF) must deny per the [y/N]
		// default — the old fmt.Fscan hung here instead of reading the newline.
		{"bare enter denies", "\n", false},
		{"leading blank line then real answer reads the blank line as deny", "\ny\n", false},
		{"yes word", "yes\n", true},
		{"capital yes word", "YES\n", true},
		{"surrounding whitespace is trimmed", "  y  \n", true},
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
