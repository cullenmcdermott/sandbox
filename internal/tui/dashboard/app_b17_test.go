package dashboard

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// collectLeafMsgs executes a command tree and returns every non-batch leaf
// message it produces. tea.Batch yields a tea.BatchMsg ([]tea.Cmd), which we
// recurse into. A leaf command that blocks (e.g. an anim tea.Tick) is skipped
// after a short timeout — it is irrelevant to the SSE-reader count we measure.
func collectLeafMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range batch {
				out = append(out, collectLeafMsgs(c)...)
			}
			return out
		}
		return []tea.Msg{msg}
	case <-time.After(100 * time.Millisecond):
		// Blocking cmd (anim tick); not an SSE reader.
		return nil
	}
}

// TestRunnerEventDelegatedOnceOnDashboard is the B17 regression guard.
//
// On ScreenDashboard a background RunnerEventMsg must be delegated to the
// dashboard EXACTLY once. The bug delegated twice — once in the "keep the
// dashboard live behind a modal" block (gated only on !KeyPressMsg) and again
// in the default ScreenDashboard case, returning tea.Batch(cmd, dashCmd). Since
// handleRunnerEvent re-issues liveSSENextCmd, each duplicate delegation spawned
// another reader on the single SSE channel, so readers accumulated per event.
//
// We close the SSE channel so each re-issued reader returns StreamEnded
// immediately (no blocking); the number of StreamEnded leaves equals the number
// of readers spawned by one Update. Exactly one means single delegation.
func TestRunnerEventDelegatedOnceOnDashboard(t *testing.T) {
	app := NewApp(nil, nil, nil)
	app.dashboard.seeded = true
	app.screen = ScreenDashboard
	app.dashboard.sessions = []Session{
		{State: session.State{ID: "s1", Status: session.StatusRunning}, DashStatus: StatusIdle},
	}

	ch := make(chan session.Event)
	close(ch) // readers return StreamEnded instantly instead of blocking
	app.dashboard.liveSSEChannels["s1"] = ch

	_, cmd := app.Update(RunnerEventMsg{ID: "s1", Event: mkEvent(session.EventTurnStarted, nil)})

	readers := 0
	for _, msg := range collectLeafMsgs(cmd) {
		if re, ok := msg.(RunnerEventMsg); ok && re.ID == "s1" && re.StreamEnded {
			readers++
		}
	}
	if readers != 1 {
		t.Fatalf("RunnerEventMsg spawned %d SSE readers, want exactly 1 (B17 double-delegation regression)", readers)
	}
}

// TestNonKeyStillReachesDashboardBehindModal guards the OTHER half of the B17
// fix: when a transcript modal is open (ScreenTranscript), a background
// (non-key) message must STILL reach the dashboard exactly once so its live
// state and toasts stay current — the single-delegation refactor must not drop
// that path.
func TestNonKeyStillReachesDashboardBehindModal(t *testing.T) {
	app := NewApp(nil, nil, nil)
	app.dashboard.seeded = true
	app.dashboard.sessions = []Session{
		{State: session.State{ID: "s1", Status: session.StatusRunning}, DashStatus: StatusIdle},
	}
	app.screen = ScreenTranscript
	app.transcript = NewTranscript(&fakeRunnerClient{}, app.dashboard.sessions[0], nil)

	ch := make(chan session.Event)
	close(ch)
	app.dashboard.liveSSEChannels["s1"] = ch

	_, cmd := app.Update(RunnerEventMsg{ID: "s1", Event: mkEvent(session.EventTurnStarted, nil)})

	readers := 0
	for _, msg := range collectLeafMsgs(cmd) {
		if re, ok := msg.(RunnerEventMsg); ok && re.ID == "s1" && re.StreamEnded {
			readers++
		}
	}
	if readers != 1 {
		t.Fatalf("background RunnerEventMsg behind a modal reached the dashboard %d times, want exactly 1", readers)
	}
}
