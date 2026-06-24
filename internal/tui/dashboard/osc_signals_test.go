package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
)

func busySessions() []Session {
	return []Session{
		{State: session.State{ID: "a", Status: session.StatusRunning}, DashStatus: StatusBusy},
	}
}

func waitingSessions() []Session {
	return []Session{
		{State: session.State{ID: "a", Status: session.StatusRunning}, DashStatus: StatusWaiting},
	}
}

// progressState must stay ProgressNone on a non-Ghostty terminal regardless of
// the session aggregate — the degradation guarantee for Stage 2.
func TestProgressStateGatedOnGhostty(t *testing.T) {
	m := New(nil)
	m.caps = terminal.Caps{} // not Ghostty
	m.sessions = waitingSessions()
	if got := m.progressState(); got != terminal.ProgressNone {
		t.Fatalf("non-Ghostty must yield ProgressNone, got %v", got)
	}
}

// The global off switch (NO_COLOR / SANDBOX_REDUCE_MOTION, folded into
// caps.ReduceMotion) must suppress Stage 2 even on Ghostty (D2/D4).
func TestProgressStateGatedOnReduceMotion(t *testing.T) {
	m := New(nil)
	m.caps = terminal.Caps{IsGhostty: true, ReduceMotion: true}
	m.sessions = waitingSessions()
	if got := m.progressState(); got != terminal.ProgressNone {
		t.Fatalf("ReduceMotion must suppress progress, got %v", got)
	}
}

func TestProgressStateMapping(t *testing.T) {
	m := New(nil)
	m.caps = terminal.Caps{IsGhostty: true}

	m.sessions = nil
	if got := m.progressState(); got != terminal.ProgressNone {
		t.Errorf("idle aggregate: got %v, want None", got)
	}
	m.sessions = busySessions()
	if got := m.progressState(); got != terminal.ProgressBusy {
		t.Errorf("busy aggregate: got %v, want Busy", got)
	}
	// Waiting (pending permission) outranks busy → error state.
	m.sessions = append(busySessions(), waitingSessions()...)
	if got := m.progressState(); got != terminal.ProgressError {
		t.Errorf("waiting aggregate: got %v, want Error", got)
	}
}

// On Ghostty, App.View must prepend the OSC progress string; on a non-Ghostty
// terminal the content is byte-identical to the undecorated screen view.
func TestAppViewPrependsProgressOnGhostty(t *testing.T) {
	a := &App{screen: ScreenDashboard, dashboard: New(nil)}
	a.dashboard.caps = terminal.Caps{IsGhostty: true}
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.sessions = busySessions()

	plain := a.screenView().Content
	decorated := a.View().Content
	if !strings.HasPrefix(decorated, terminal.OSCProgress(terminal.ProgressBusy)) {
		t.Fatal("expected OSC busy-progress prefix on Ghostty")
	}
	if !strings.HasSuffix(decorated, plain) {
		t.Fatal("decorated frame must still contain the full plain frame")
	}
}

func TestAppViewNoSignalsWithoutGhostty(t *testing.T) {
	a := &App{screen: ScreenDashboard, dashboard: New(nil)}
	a.dashboard.caps = terminal.Caps{} // not Ghostty
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.sessions = busySessions()

	if a.View().Content != a.screenView().Content {
		t.Fatal("non-Ghostty App.View must be byte-identical to the plain screen view")
	}
}

// The active→idle transition must emit exactly one clear, then stop emitting.
func TestAppViewClearsProgressOnce(t *testing.T) {
	a := &App{screen: ScreenDashboard, dashboard: New(nil)}
	a.dashboard.caps = terminal.Caps{IsGhostty: true}
	a.dashboard.width, a.dashboard.height = 80, 24

	a.dashboard.sessions = busySessions()
	_ = a.View() // sets progressActive = true
	if !a.progressActive {
		t.Fatal("expected progressActive after a busy frame")
	}

	a.dashboard.sessions = nil // go idle
	clearFrame := a.View().Content
	if !strings.HasPrefix(clearFrame, terminal.OSCProgress(terminal.ProgressNone)) {
		t.Fatal("expected a one-shot clear on busy→idle transition")
	}
	if a.progressActive {
		t.Fatal("progressActive must be false after the clear")
	}
	// A second idle frame must not re-emit the clear.
	idleFrame := a.View().Content
	if strings.HasPrefix(idleFrame, "\x1b]9;4") {
		t.Fatal("idle frame must not re-emit progress once cleared")
	}
}

// The desktop notification queued on a toast transition drains exactly once.
func TestPendingOSCDrainsOnce(t *testing.T) {
	a := &App{screen: ScreenDashboard, dashboard: New(nil)}
	a.dashboard.caps = terminal.Caps{IsGhostty: true}
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.pendingOSC = terminal.OSCNotify("sess", "wants: Bash")

	first := a.View().Content
	if !strings.Contains(first, "777;notify;sess") {
		t.Fatal("expected the queued notification in the first frame")
	}
	second := a.View().Content
	if strings.Contains(second, "777;notify") {
		t.Fatal("notification must fire exactly once (drained)")
	}
}
