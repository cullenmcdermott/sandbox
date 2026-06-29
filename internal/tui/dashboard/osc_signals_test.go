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

// nudge drives one App.Update with a benign non-key message so the
// progress-transition logic (which runs after the dashboard delegation) fires,
// and returns the first tea.Raw payload it emitted (or "" for no emission). A
// WindowSizeMsg falls through to the delegation while preserving the dashboard's
// sessions/caps, so it is the cleanest aggregate-state probe.
func nudge(t *testing.T, a *App) string {
	t.Helper()
	_, cmd := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return rawString(t, cmd)
}

// Tab progress rides tea.Raw (the v2 cell renderer drops escapes spliced into
// View content), so View must NEVER prepend an OSC 9;4 progress string. A non-nil
// transcript is set so withTerminalSignals runs its full body (past the nil-guard,
// down the Kitty path) — pinning the invariant on the live splice path rather than
// vacuously short-circuiting. With no pending Kitty image the content is also
// byte-identical to the plain screen view on every terminal.
func TestAppViewNeverPrependsProgress(t *testing.T) {
	for _, ghostty := range []bool{true, false} {
		a := &App{screen: ScreenDashboard, dashboard: New(nil), transcript: &TranscriptModel{}}
		a.dashboard.caps = terminal.Caps{IsGhostty: ghostty}
		a.dashboard.width, a.dashboard.height = 80, 24
		a.dashboard.sessions = busySessions()

		content := a.View().Content
		if strings.Contains(content, "\x1b]9;4") {
			t.Fatalf("ghostty=%v: View must not splice an OSC 9;4 progress escape into content", ghostty)
		}
		if content != a.screenView().Content {
			t.Fatalf("ghostty=%v: with no pending Kitty image, View must equal the plain screen view", ghostty)
		}
	}
}

// On Ghostty, every aggregate-state transition must emit the matching OSC
// progress via tea.Raw from App.Update, and only on the edge. The walk covers all
// three states — idle/busy/error — so a regression from the three-valued
// terminal.Progress edge-trigger back to a boolean on/off guard (which would stop
// distinguishing busy↔error) is caught: OSCProgress(Busy) and OSCProgress(Error)
// are distinct strings.
func TestAppUpdateEmitsProgressViaRawOnGhostty(t *testing.T) {
	a := &App{screen: ScreenDashboard, dashboard: New(nil)}
	a.dashboard.caps = terminal.Caps{IsGhostty: true}
	a.dashboard.width, a.dashboard.height = 80, 24

	// idle → busy: emit ProgressBusy.
	a.dashboard.sessions = busySessions()
	if got := nudge(t, a); got != terminal.OSCProgress(terminal.ProgressBusy) {
		t.Fatalf("idle→busy must emit busy progress via tea.Raw, got %q", got)
	}
	// busy → busy (no change): no emission.
	if got := nudge(t, a); got != "" {
		t.Fatalf("steady busy must not re-emit progress, got %q", got)
	}
	// busy → waiting: emit the DISTINCT ProgressError state (a pending permission
	// outranks busy). A boolean on/off guard would miss this edge entirely.
	a.dashboard.sessions = waitingSessions()
	if got := nudge(t, a); got != terminal.OSCProgress(terminal.ProgressError) {
		t.Fatalf("busy→waiting must emit error progress via tea.Raw, got %q", got)
	}
	// waiting → waiting (no change): no emission.
	if got := nudge(t, a); got != "" {
		t.Fatalf("steady waiting must not re-emit progress, got %q", got)
	}
	// waiting → idle: emit a single ProgressNone clear.
	a.dashboard.sessions = nil
	if got := nudge(t, a); got != terminal.OSCProgress(terminal.ProgressNone) {
		t.Fatalf("waiting→idle must emit a one-shot clear via tea.Raw, got %q", got)
	}
	// idle → idle: stay quiet.
	if got := nudge(t, a); got != "" {
		t.Fatalf("steady idle must not re-emit a clear, got %q", got)
	}
}

// A non-Ghostty terminal must never emit a progress signal, regardless of the
// session aggregate (progressState returns ProgressNone, so there is no edge).
func TestAppUpdateNoProgressWithoutGhostty(t *testing.T) {
	a := &App{screen: ScreenDashboard, dashboard: New(nil)}
	a.dashboard.caps = terminal.Caps{} // not Ghostty
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.sessions = busySessions()

	if got := nudge(t, a); got != "" {
		t.Fatalf("non-Ghostty must emit no progress signal, got %q", got)
	}
}

// Under the external (opencode) PTY pane, App.Update must emit NO progress signal:
// the pane owns the terminal. The guard, not the aggregate, is the suppressor —
// the session is busy and the terminal is Ghostty, yet nothing is written.
func TestAppUpdateNoProgressUnderExternalPane(t *testing.T) {
	a := &App{screen: ScreenExternal, dashboard: New(nil)}
	a.dashboard.caps = terminal.Caps{IsGhostty: true}
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.sessions = busySessions()

	if got := nudge(t, a); got != "" {
		t.Fatalf("external pane must suppress progress emission, got %q", got)
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
