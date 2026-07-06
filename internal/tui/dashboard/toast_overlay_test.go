package dashboard

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestToastCompositesOverModalScreen is the T3 regression: a cross-session
// "needs you" toast must float over every screen, not only the bare dashboard.
// Before the fix renderToast was composited only inside renderZoned, so the
// toast was invisible behind the chat modal / connecting splash — exactly when a
// background session needs attention. withToast now overlays it at the App level.
func TestToastCompositesOverModalScreen(t *testing.T) {
	a := &App{screen: ScreenTranscript, dashboard: New(nil), width: 80, height: 24}
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.toast = &notification{
		title:     "BgSess",
		note:      "claude · proj",
		status:    StatusWaiting,
		createdAt: time.Now(),
	}

	// A synthetic 24×80 frame standing in for the chat-modal screen content.
	var rows []string
	for i := 0; i < 24; i++ {
		rows = append(rows, strings.Repeat("x", 80))
	}
	base := tea.NewView(strings.Join(rows, "\n"))

	out := a.withToast(base).Content
	if !strings.Contains(out, "BgSess") {
		t.Fatal("toast title must appear when composited over a non-dashboard screen")
	}
	if !strings.Contains(out, "needs you") {
		t.Error("toast body must appear over the modal screen")
	}
}

// TestWithToastNoOpWithoutToast keeps the no-toast path byte-identical so the
// OSC-signal frame assertions (and every non-toast frame) are unaffected.
func TestWithToastNoOpWithoutToast(t *testing.T) {
	a := &App{screen: ScreenTranscript, dashboard: New(nil), width: 80, height: 24}
	base := tea.NewView("hello")
	if got := a.withToast(base).Content; got != "hello" {
		t.Fatalf("withToast with no toast changed content: %q", got)
	}
}

// TestWithToastSuppressedOnBareDashboard is the over-eager-toast fix: on the bare
// dashboard the row glyphs already show attention, so the toast must NOT float
// over the list (it only appears once a modal/splash hides it).
func TestWithToastSuppressedOnBareDashboard(t *testing.T) {
	a := &App{screen: ScreenDashboard, dashboard: New(nil), width: 80, height: 24}
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.toast = &notification{title: "BgSess", note: "claude · proj", status: StatusWaiting, createdAt: time.Now()}
	base := tea.NewView("dashboard-frame")
	if got := a.withToast(base).Content; got != "dashboard-frame" {
		t.Fatalf("toast must be suppressed on the bare dashboard, got %q", got)
	}
}

// TestToastNotSheared is the render-shear regression: the box must be placed as a
// unit, so its top and bottom border arcs land in the SAME column. The old code
// prepended spaces to only line 0, leaving the body + bottom border at column 0
// while the top border sat at the right.
func TestToastNotSheared(t *testing.T) {
	a := &App{screen: ScreenTranscript, dashboard: New(nil), width: 80, height: 24}
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.toast = &notification{title: "BgSess", note: "claude · proj", status: StatusWaiting, createdAt: time.Now()}

	var rows []string
	for i := 0; i < 24; i++ {
		rows = append(rows, strings.Repeat("x", 80))
	}
	out := a.withToast(tea.NewView(strings.Join(rows, "\n"))).Content

	// Cells left of the box are ASCII 'x' (1 byte each), so the byte index of the
	// first box-drawing rune on a line equals its visual column.
	topCol, botCol := -1, -1
	for _, ln := range strings.Split(out, "\n") {
		if c := strings.IndexRune(ln, '╭'); c >= 0 && topCol < 0 {
			topCol = c
		}
		if c := strings.IndexRune(ln, '╰'); c >= 0 {
			botCol = c
		}
	}
	if topCol < 0 || botCol < 0 {
		t.Fatalf("toast borders not found (top=%d bot=%d)", topCol, botCol)
	}
	if topCol != botCol {
		t.Fatalf("toast sheared: top border at col %d, bottom border at col %d", topCol, botCol)
	}
	if topCol == 0 {
		t.Fatal("toast should be offset toward the right edge, not at column 0")
	}
}

// TestNotifyEdgeDedupes is the over-eager-fire fix: a session that stays in an
// attention state must be toasted only once (on entry), not on every event, and
// must re-toast only after leaving and re-entering attention.
func TestNotifyEdgeDedupes(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusWaiting},
	}}

	if cmd := m.notifyIfBackgroundAttention(""); cmd == nil {
		t.Fatal("first entry into attention should toast")
	}
	if cmd := m.notifyIfBackgroundAttention(""); cmd != nil {
		t.Fatal("a still-waiting session must not re-toast (edge, not level)")
	}
	// Leave attention, then re-enter — should toast again.
	m.sessions[0].DashStatus = StatusIdle
	_ = m.notifyIfBackgroundAttention("")
	m.sessions[0].DashStatus = StatusWaiting
	if cmd := m.notifyIfBackgroundAttention(""); cmd == nil {
		t.Fatal("re-entering attention after leaving should toast again")
	}
}

func TestJumpToNextAttentionIncludesFailed(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{
			State:            session.State{ID: "idle", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
		},
		{
			State:            session.State{ID: "failed", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusFailed},
		}}
	got := m.jumpToNextNeedingAttention()
	if got == nil || got.ID() != "failed" {
		t.Fatalf("jump target = %v, want failed session", got)
	}
}

func TestJumpToNextAttentionUsesRowsInGroupView(t *testing.T) {
	m := New(nil)
	m.groupView.open = true
	m.groupView.repos = map[string]bool{"repo-a": true, "repo-b": true}
	m.sessions = []Session{
		{
			State:            session.State{ID: "idle", Status: session.StatusRunning, ProjectPath: "/repo-a"},
			sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
		},
		{
			State:            session.State{ID: "failed", Status: session.StatusRunning, ProjectPath: "/repo-b"},
			sessionReadModel: sessionReadModel{DashStatus: StatusFailed},
		}}
	got := m.jumpToNextNeedingAttention()
	if got == nil || got.ID() != "failed" {
		t.Fatalf("jump target = %v, want failed session", got)
	}
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) || rows[m.cursor].session == nil || rows[m.cursor].session.ID() != "failed" {
		t.Fatalf("cursor = %d, want row for failed session", m.cursor)
	}
}

func TestJumpToNextAttentionExpandsCollapsedGroup(t *testing.T) {
	m := New(nil)
	m.groupView.open = true
	m.groupView.repos = map[string]bool{"repo-a": true, "repo-b": false}
	m.sessions = []Session{
		{
			State:            session.State{ID: "idle", Status: session.StatusRunning, ProjectPath: "/repo-a"},
			sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
		},
		{
			State:            session.State{ID: "failed", Status: session.StatusRunning, ProjectPath: "/repo-b"},
			sessionReadModel: sessionReadModel{DashStatus: StatusFailed},
		}}
	got := m.jumpToNextNeedingAttention()
	if got == nil || got.ID() != "failed" {
		t.Fatalf("jump target = %v, want failed session", got)
	}
	if !m.groupView.repos["repo-b"] {
		t.Fatal("jump should expand the target's collapsed group")
	}
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) || rows[m.cursor].session == nil || rows[m.cursor].session.ID() != "failed" {
		t.Fatalf("cursor = %d, want row for failed session", m.cursor)
	}
}
