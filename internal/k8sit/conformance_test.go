//go:build integration

package k8sit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Phase C — k8sit backend-conformance suite (docs/archive/testing-parity-plan.md). Every
// scenario is table-driven over backendCases so each backend runs the IDENTICAL
// live checks: a new backend (Codex) is onboarded by appending its row, never by
// inventing a test story. These exercise the runTurn/registry-level parity paths
// that the runner unit tests can't reach (live abort, error-surface no-wedge,
// SSE replay, suspend/resume lifecycle).
//
// Runtime: each test creates its own pod (createReadySession), so the suite is
// slow and on-demand (just kind-test), never in `just check`.

// --- Error surface: a failed turn must not wedge the session ----------------

// TestBackendErrorSurface drives a turn with a deliberately invalid model and
// asserts the parity guarantee that matters most operationally: the turn SETTLES
// (turn.failed or turn.completed) AND returns the session to idle, so a follow-up
// turn is accepted (not 409'd) — the session is never wedged 'busy'. When the turn
// fails, the failure must carry a non-empty reason (what the TUI decodes).
//
// NOTE (live finding): backends differ in model-validation strictness — opencode
// rejects an unknown model with turn.failed, while the claude binary tolerates /
// falls back on one and completes. So "bad model → turn.failed" is NOT a parity
// assertion; the no-wedge-on-settle property is. Table-driven over every backend.
func TestBackendErrorSurface(t *testing.T) {
	localRestConfig(t)
	for _, bc := range backendCases {
		t.Run(bc.name, func(t *testing.T) {
			be, ref := createReadySession(t, bc.backend, bc.idTag+"-err")
			client, cleanup := runnerClientForRef(t, be, ref)
			t.Cleanup(cleanup)

			hctx, hcancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer hcancel()
			if err := pollHealthy(hctx, client); err != nil {
				t.Fatalf("runner never healthy: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			tref, err := client.StartTurn(ctx, ref, session.TurnInput{
				Prompt: turnPrompt,
				Model:  "nope/totally-bogus-model-9999",
			})
			if err != nil {
				t.Fatalf("StartTurn: %v", err)
			}
			events, err := client.Events(ctx, ref, 0)
			if err != nil {
				t.Fatalf("Events: %v", err)
			}
			outcome, failMsg := assertTurnSettles(t, ctx, events, tref.Turn)
			t.Logf("%s bogus-model turn settled as %s", bc.name, outcome)
			if outcome == session.EventTurnFailed && failMsg == "" {
				t.Fatalf("turn.failed carried an empty message (the TUI needs a reason)")
			}

			// No wedge: the session returns to idle and accepts a NEW turn (a wedged
			// 'busy' session would 409 here).
			waitIdle(t, client, ref, 30*time.Second)
			if _, err := client.StartTurn(ctx, ref, session.TurnInput{Prompt: turnPrompt, Model: bc.turnModel}); err != nil {
				t.Fatalf("session wedged: a follow-up turn was rejected after the prior turn settled: %v", err)
			}
		})
	}
}

// --- Live interrupt: abort returns the session to idle ----------------------

// TestBackendInterrupt starts a long-running turn, interrupts it, and asserts the
// session emits turn.interrupted and returns to idle. Skipped for plumbing-only
// backends (no key → the turn fails too fast to interrupt deterministically).
func TestBackendInterrupt(t *testing.T) {
	rc := localRestConfig(t)
	for _, bc := range backendCases {
		t.Run(bc.name, func(t *testing.T) {
			if !bc.expectRealReply(t, rc) {
				t.Skip("plumbing-only backend: no sustained turn to interrupt") // gate-ok: no provider key, turn can't run long enough
			}
			be, ref := createReadySession(t, bc.backend, bc.idTag+"-int")
			client, cleanup := runnerClientForRef(t, be, ref)
			t.Cleanup(cleanup)

			hctx, hcancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer hcancel()
			if err := pollHealthy(hctx, client); err != nil {
				t.Fatalf("runner never healthy: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			// A long prompt so there is a window to interrupt mid-flight.
			tref, err := client.StartTurn(ctx, ref, session.TurnInput{
				Prompt: "Count from 1 to 300, one number per line, slowly.",
				Model:  bc.turnModel,
			})
			if err != nil {
				t.Fatalf("StartTurn: %v", err)
			}
			events, err := client.Events(ctx, ref, 0)
			if err != nil {
				t.Fatalf("Events: %v", err)
			}
			// Interrupt as soon as the turn is under way.
			waitForEvent(t, ctx, events, tref.Turn, session.EventTurnStarted)
			if err := client.InterruptTurn(ctx, ref, tref); err != nil {
				t.Fatalf("InterruptTurn: %v", err)
			}
			// server.ts owns turn.interrupted; assert it lands and the session idles.
			waitForEvent(t, ctx, events, tref.Turn, session.EventTurnInterrupted)
			waitIdle(t, client, ref, 30*time.Second)
		})
	}
}

// --- Reconnect / SSE replay: after=<seq> replays only newer events ----------

// TestBackendReconnectReplay runs a turn to a terminal event, then reopens the
// event stream with after=<turn.started seq> and asserts the replay is consistent
// and bounded: every replayed event has seq > the cut, the turn.started at the cut
// is NOT replayed, and the same terminal event is seen again. This is the SSE
// catch-up the `sandbox attach` path depends on. Table-driven over every backend.
func TestBackendReconnectReplay(t *testing.T) {
	localRestConfig(t)
	for _, bc := range backendCases {
		t.Run(bc.name, func(t *testing.T) {
			be, ref := createReadySession(t, bc.backend, bc.idTag+"-rc")
			client, cleanup := runnerClientForRef(t, be, ref)
			t.Cleanup(cleanup)

			hctx, hcancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer hcancel()
			if err := pollHealthy(hctx, client); err != nil {
				t.Fatalf("runner never healthy: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()

			tref, err := client.StartTurn(ctx, ref, session.TurnInput{Prompt: turnPrompt, Model: bc.turnModel})
			if err != nil {
				t.Fatalf("StartTurn: %v", err)
			}
			events, err := client.Events(ctx, ref, 0)
			if err != nil {
				t.Fatalf("Events: %v", err)
			}

			// First pass (after=0): record turn.started's seq (the replay cut) and the
			// terminal event type, consuming until the turn settles.
			var startedSeq uint64
			var terminal session.EventType
			for terminal == "" {
				select {
				case <-ctx.Done():
					t.Fatalf("first pass timed out before terminal (started=%d)", startedSeq)
				case ev, ok := <-events:
					if !ok {
						t.Fatalf("first-pass stream closed before terminal (started=%d)", startedSeq)
					}
					if ev.TurnID != tref.Turn {
						continue
					}
					switch ev.Type {
					case session.EventTurnStarted:
						startedSeq = ev.Seq
					case session.EventTurnCompleted, session.EventTurnFailed:
						terminal = ev.Type
					}
				}
			}
			if startedSeq == 0 {
				t.Fatalf("never observed turn.started seq")
			}

			// Second pass (after=startedSeq): a fresh stream must replay only seq >
			// startedSeq, never the turn.started at the cut, and re-deliver the terminal.
			rctx, rcancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer rcancel()
			replay, err := client.Events(rctx, ref, startedSeq)
			if err != nil {
				t.Fatalf("replay Events(after=%d): %v", startedSeq, err)
			}
			var sawTerminalAgain bool
			for !sawTerminalAgain {
				select {
				case <-rctx.Done():
					t.Fatalf("replay timed out before re-seeing %s after seq %d", terminal, startedSeq)
				case ev, ok := <-replay:
					if !ok {
						t.Fatalf("replay stream closed before re-seeing %s", terminal)
					}
					if ev.Seq <= startedSeq {
						t.Fatalf("replay after=%d delivered a stale event seq=%d type=%s", startedSeq, ev.Seq, ev.Type)
					}
					if ev.TurnID == tref.Turn && ev.Type == session.EventTurnStarted {
						t.Fatalf("replay after=%d re-delivered the turn.started at the cut", startedSeq)
					}
					if ev.TurnID == tref.Turn && ev.Type == terminal {
						sawTerminalAgain = true
					}
				}
			}
		})
	}
}

// --- Lifecycle: suspend → resume → turn -------------------------------------

// TestBackendLifecycle suspends a ready session (replicas→0), resumes it
// (replicas→1), and asserts a turn still runs afterward — the PVC-backed state
// survived the suspend and the runner reattaches. Then it asserts Destroy is
// idempotent. Table-driven over every backend.
func TestBackendLifecycle(t *testing.T) {
	localRestConfig(t)
	for _, bc := range backendCases {
		t.Run(bc.name, func(t *testing.T) {
			be, ref := createReadySession(t, bc.backend, bc.idTag+"-life")

			// Suspend → Resume (each bounded; resume waits for the pod to be Ready
			// again, which re-pulls nothing since the image is kind-loaded).
			susCtx, susCancel := context.WithTimeout(context.Background(), 120*time.Second)
			if err := be.Suspend(susCtx, ref); err != nil {
				susCancel()
				t.Fatalf("Suspend: %v", err)
			}
			susCancel()
			resCtx, resCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := be.Resume(resCtx, ref); err != nil {
				resCancel()
				t.Fatalf("Resume: %v", err)
			}
			resCancel()

			// A turn must run after resume — the parity guarantee is that suspend/
			// resume restored a turn-ACCEPTING session whose turns settle. We assert
			// it SSE-independently (poll /idle, then replay the persisted log) so a
			// transient post-resume port-forward/pod-churn blip — which production
			// handles by reconnecting — doesn't read as a backend failure.
			client, cleanup := runnerClientForRef(t, be, ref)
			t.Cleanup(cleanup)
			hctx, hcancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer hcancel()
			if err := pollHealthy(hctx, client); err != nil {
				t.Fatalf("runner never healthy after resume: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			tref, err := client.StartTurn(ctx, ref, session.TurnInput{Prompt: turnPrompt, Model: bc.turnModel})
			if err != nil {
				t.Fatalf("post-resume StartTurn (session not turn-accepting after resume): %v", err)
			}
			waitIdle(t, client, ref, 90*time.Second)            // the turn settled
			assertTurnInHistory(t, ctx, client, ref, tref.Turn) // and it really ran + settled

			// Destroy idempotency: a second Destroy of the same ref must not error
			// (NotFound is tolerated by Backend.Destroy).
			d1, d1c := context.WithTimeout(context.Background(), 60*time.Second)
			if err := be.Destroy(d1, ref); err != nil {
				d1c()
				t.Fatalf("first Destroy: %v", err)
			}
			d1c()
			d2, d2c := context.WithTimeout(context.Background(), 60*time.Second)
			if err := be.Destroy(d2, ref); err != nil {
				d2c()
				t.Fatalf("second Destroy not idempotent: %v", err)
			}
			d2c()
		})
	}
}

// --- shared conformance helpers ---------------------------------------------

// assertTurnSettles consumes the stream until a terminal event (turn.completed |
// turn.failed) for ourTurn, returning which and — for a failure — the decoded
// `message`. Fails on stream close or timeout.
func assertTurnSettles(t *testing.T, ctx context.Context, events <-chan session.Event, ourTurn session.TurnID) (session.EventType, string) {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for the turn to settle: %v", ctx.Err())
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before the turn settled")
			}
			if ev.TurnID != ourTurn {
				continue
			}
			switch ev.Type {
			case session.EventTurnCompleted:
				return session.EventTurnCompleted, ""
			case session.EventTurnFailed:
				var p struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(ev.Payload, &p)
				return session.EventTurnFailed, p.Message
			}
		}
	}
}

// waitForEvent consumes the stream until the given event type for ourTurn.
func waitForEvent(t *testing.T, ctx context.Context, events <-chan session.Event, ourTurn session.TurnID, want session.EventType) {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s: %v", want, ctx.Err())
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before %s", want)
			}
			if ev.TurnID == ourTurn && ev.Type == want {
				return
			}
		}
	}
}

// assertTurnInHistory opens a fresh replay stream (after=0) — read AFTER the turn
// has settled — and asserts the persisted log holds turn.started and a terminal
// (turn.completed | turn.failed) for ourTurn. Reading from the durable sqlite log
// post-settle is robust to a live-stream drop during the turn itself.
func assertTurnInHistory(t *testing.T, ctx context.Context, client *runner.Client, ref session.Ref, ourTurn session.TurnID) {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	events, err := client.Events(rctx, ref, 0)
	if err != nil {
		t.Fatalf("replay Events(after=0): %v", err)
	}
	var sawStarted, sawTerminal bool
	for !(sawStarted && sawTerminal) {
		select {
		case <-rctx.Done():
			t.Fatalf("post-resume turn not fully in the log (started=%v terminal=%v): %v", sawStarted, sawTerminal, rctx.Err())
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("replay stream closed before the turn was found (started=%v terminal=%v)", sawStarted, sawTerminal)
			}
			if ev.TurnID != ourTurn {
				continue
			}
			switch ev.Type {
			case session.EventTurnStarted:
				sawStarted = true
			case session.EventTurnCompleted, session.EventTurnFailed:
				sawTerminal = true
			}
		}
	}
}

// waitIdle polls /idle until the session reports no active turn, or fails after
// the deadline. Proves a settled turn (completed/failed/interrupted) released the
// session so the reaper clock can start and new turns are accepted.
func waitIdle(t *testing.T, client *runner.Client, ref session.Ref, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		st, err := client.Idle(ctx, ref)
		cancel()
		if err == nil && !st.TurnActive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session never returned to idle within %s (lastErr=%v)", within, err)
		}
		time.Sleep(time.Second)
	}
}
