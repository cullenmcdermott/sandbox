package dashboard

// mouse_selection_test.go — the per-screen mouse-capture decision and the
// detail panel's worktree row.
//
// Both exist because of the same underlying constraint: DECSET 1002 is a single
// switch covering click, release, wheel and drag, so an app that captures the
// mouse necessarily takes native click-drag selection away from the user. The
// dashboard used to capture on every screen while consuming mouse events on
// exactly one, which cost selection everywhere and bought nothing outside the
// pane. These tests pin the screens where capture is and is not paid for.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestMouseCaptureOnlyOnScreensThatConsumeIt asserts capture is enabled on the
// external pane and NOWHERE else.
//
// The counter-half matters more than the positive half: a regression that
// re-enables capture globally still renders and still passes every other test,
// and the only visible symptom is that the user silently loses text selection.
// Asserting MouseModeNone on the non-pane screens is what catches that.
func TestMouseCaptureOnlyOnScreensThatConsumeIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		screen Screen
		want   tea.MouseMode
	}{
		// handleMouse is reachable only from updateExternalScreen, so the pane is
		// the only screen whose captured events are consumed: the wheel drives
		// scrollback and clicks are re-encoded onto a tracking child's PTY.
		{"external pane captures", ScreenExternal, tea.MouseModeCellMotion},
		// On these the events were captured and dropped. Left uncaptured, the
		// terminal does native selection and (in the alt screen) translates the
		// wheel to Up/Down, which the list already binds to navigation.
		{"session list does not", ScreenDashboard, tea.MouseModeNone},
		{"feed does not", ScreenFeed, tea.MouseModeNone},
		{"connecting splash does not", ScreenConnecting, tea.MouseModeNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{screen: tc.screen, dashboard: New(nil)}
			a.dashboard.width, a.dashboard.height = 80, 24

			if got := a.View().MouseMode; got != tc.want {
				t.Errorf("screen %v: MouseMode = %v, want %v", tc.screen, got, tc.want)
			}
		})
	}
}

// TestMouseCaptureFollowsScreenChanges pins that the decision is re-made per
// frame rather than latched once. Capture is a terminal mode the renderer diffs
// between views, so a value computed once at startup would leave the terminal in
// whatever mode the first screen wanted for the rest of the session.
func TestMouseCaptureFollowsScreenChanges(t *testing.T) {
	a := &App{screen: ScreenDashboard, dashboard: New(nil)}
	a.dashboard.width, a.dashboard.height = 80, 24

	if got := a.View().MouseMode; got != tea.MouseModeNone {
		t.Fatalf("list frame: MouseMode = %v, want none", got)
	}
	a.screen = ScreenExternal
	if got := a.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("pane frame: MouseMode = %v, want cell motion", got)
	}
	a.screen = ScreenDashboard
	if got := a.View().MouseMode; got != tea.MouseModeNone {
		t.Fatalf("back on the list: MouseMode = %v, want none — capture must be released, not latched", got)
	}
}

// TestFitPathTailKeepsTheIdentifyingTail asserts paths are cut from the LEFT.
// Worktree paths share a long prefix and differ only in the final segment, so a
// right-truncation renders every session's identically.
func TestFitPathTailKeepsTheIdentifyingTail(t *testing.T) {
	const p = "~/.local/share/sandbox/remote-sessions/worktrees/claude-pane-df80e6-031396fb"

	// Measured in DISPLAY COLUMNS, not bytes: the "…" marker is 3 bytes wide and
	// 1 column, so a len() check here reports a false overflow.
	got := fitPathTail(p, 40)
	if w := lipgloss.Width(got); w > 40 {
		t.Fatalf("fitPathTail(40) = %q, which is %d columns", got, w)
	}
	if !strings.HasSuffix(got, "claude-pane-df80e6-031396fb") {
		t.Errorf("fitPathTail dropped the session id: %q — the tail is the only part that differs between sessions", got)
	}
	if !strings.HasPrefix(got, "…/") {
		t.Errorf("fitPathTail = %q, want a leading …/ marking the elision", got)
	}
	// Whole segments only: no partial directory name should survive the cut.
	if strings.Contains(got, "…/orktrees") || strings.Contains(got, "…/essions") {
		t.Errorf("fitPathTail cut mid-segment: %q", got)
	}
}

func TestFitPathTailLeavesFittingPathsAlone(t *testing.T) {
	const p = "~/git/sandbox"
	if got := fitPathTail(p, 40); got != p {
		t.Errorf("fitPathTail(%q, 40) = %q, want it unchanged", p, got)
	}
}

// TestFitPathTailDegradesWhenEvenTheTailOverflows covers the narrow-pane case:
// there is no elision that fits, so it must still return something within the
// budget rather than overflowing the panel.
func TestFitPathTailDegradesWhenEvenTheTailOverflows(t *testing.T) {
	got := fitPathTail("~/.local/share/sandbox/worktrees/claude-pane-df80e6-031396fb", 10)
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("fitPathTail(10) = %q (%d columns), want it clamped to 10", got, w)
	}
}

// TestWorktreeDetailSuppressesTheNonWorktreeCase asserts the row is omitted
// when WorkspacePath carries no information ProjectPath does not already show.
// A non-git or --worktree=off session has workspace == repo root, and printing
// one path under two labels is noise, not detail.
func TestWorktreeDetailSuppressesTheNonWorktreeCase(t *testing.T) {
	for _, tc := range []struct {
		name          string
		project, work string
	}{
		{"no worktree recorded", "/Users/x/git/sandbox", ""},
		{"workspace is the repo root", "/Users/x/git/sandbox", "/Users/x/git/sandbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Session{State: session.State{ProjectPath: tc.project, WorkspacePath: tc.work}}
			if got := worktreeDetail(s, 60); got != "" {
				t.Errorf("worktreeDetail = %q, want \"\" so the row is skipped", got)
			}
		})
	}
}

func TestWorktreeDetailRendersADistinctWorktree(t *testing.T) {
	s := Session{State: session.State{
		ProjectPath:   "/Users/x/git/sandbox",
		WorkspacePath: "/Users/x/.local/share/sandbox/remote-sessions/worktrees/claude-pane-df80e6-031396fb",
	}}
	got := worktreeDetail(s, 60)
	if got == "" {
		t.Fatal("worktreeDetail = \"\", want the worktree path — this session's changes live somewhere other than the repo root")
	}
	if !strings.HasSuffix(got, "claude-pane-df80e6-031396fb") {
		t.Errorf("worktreeDetail = %q, want it to end in the session id", got)
	}
}

// TestDetailPanelShowsTheWorktreePath is the end-to-end half: the row actually
// reaches the rendered panel, and does not overflow the width it was given.
func TestDetailPanelShowsTheWorktreePath(t *testing.T) {
	const width = 46
	m := New(nil)
	m.width, m.height = 120, 40
	m.sessions = []Session{{State: session.State{
		ID:            "claude-pane-df80e6-031396fb",
		Backend:       "claude-pane",
		ProjectPath:   "/Users/x/git/sandbox",
		WorkspacePath: "/Users/x/.local/share/sandbox/remote-sessions/worktrees/claude-pane-df80e6-031396fb",
	}}}
	m.cursor = 0

	lines := m.renderDetailLines(width, 40)
	joined := stripANSI(strings.Join(lines, "\n"))

	if !strings.Contains(joined, "worktree") {
		t.Fatalf("detail panel has no worktree row:\n%s", joined)
	}
	if !strings.Contains(joined, "031396fb") {
		t.Errorf("worktree row lost the session id that identifies the dir:\n%s", joined)
	}
	for i, ln := range strings.Split(joined, "\n") {
		if w := lipgloss.Width(ln); w > width {
			t.Errorf("detail line %d is %d wide, over the %d budget: %q", i, w, width, ln)
		}
	}
}
