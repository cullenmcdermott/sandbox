package dashboard

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// rawString executes a Cmd (recursing into tea.Batch) and returns the first
// tea.RawMsg string payload it finds, or "" if none. tea.Raw is how out-of-band
// terminal writes (here, the desktop notification) reach the terminal, bypassing
// the cell renderer that would drop them from View content.
func rawString(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		return ""
	}
	switch msg := cmd().(type) {
	case tea.RawMsg:
		if s, ok := msg.Msg.(string); ok {
			return s
		}
	case tea.BatchMsg:
		for _, c := range msg {
			if s := rawString(t, c); s != "" {
				return s
			}
		}
	}
	return ""
}

// A toast transition must fire the desktop notification out-of-band via tea.Raw
// (not spliced into View content, which the v2 cell renderer drops). On Ghostty
// that's the OSC 777 form.
func TestToastNotifiesViaRawOnGhostty(t *testing.T) {
	m := New(nil)
	m.caps = terminal.Caps{IsGhostty: true}
	m.toastTickActive = true // suppress the 50ms tick cmd so the batch is just the notify

	_, cmd := m.Update(toastMsg{id: "s1", title: "sess", note: "wants: Bash", status: StatusWaiting})
	got := rawString(t, cmd)
	if !strings.Contains(got, "777;notify;sess") {
		t.Fatalf("expected OSC 777 notify via tea.Raw, got %q", got)
	}
}

// On iTerm2/WezTerm the notification uses OSC 9 (single message), not OSC 777.
func TestToastNotifiesViaRawOnITerm(t *testing.T) {
	m := New(nil)
	m.caps = terminal.Caps{IsITerm2: true}
	m.toastTickActive = true

	_, cmd := m.Update(toastMsg{id: "s1", title: "sess", note: "wants: Bash", status: StatusWaiting})
	got := rawString(t, cmd)
	if !strings.HasPrefix(got, "\x1b]9;") || !strings.Contains(got, "sess") {
		t.Fatalf("expected OSC 9 notify via tea.Raw, got %q", got)
	}
	if strings.Contains(got, "777") {
		t.Fatalf("iTerm2 must not use OSC 777, got %q", got)
	}
}

// The notification must be suppressed under the global off switch and on a
// terminal we can't target (so non-Ghostty/iTerm/WezTerm output stays clean).
func TestToastNoNotifyWhenUntargetableOrReduced(t *testing.T) {
	reduced := New(nil)
	reduced.caps = terminal.Caps{IsGhostty: true, ReduceMotion: true}
	reduced.toastTickActive = true
	if got := rawString(t, mustCmd(reduced.Update(toastMsg{id: "s1", title: "sess"}))); got != "" {
		t.Fatalf("ReduceMotion must suppress the notification, got %q", got)
	}

	plain := New(nil)
	plain.caps = terminal.Caps{} // unknown terminal
	plain.toastTickActive = true
	if got := rawString(t, mustCmd(plain.Update(toastMsg{id: "s1", title: "sess"}))); got != "" {
		t.Fatalf("untargetable terminal must emit no notification, got %q", got)
	}
}

func mustCmd(_ tea.Model, cmd tea.Cmd) tea.Cmd { return cmd }
