package dashboard

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// ctrl+g on the dashboard screen must route to jumpToNextNeedingAttention — the
// key was documented + wired only in the chat modal before, dead on the
// dashboard itself.
func TestCtrlGJumpsToAttentionOnDashboard(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{State: session.State{ID: "idle", Status: session.StatusRunning}, DashStatus: StatusIdle},
		{State: session.State{ID: "failed", Status: session.StatusRunning}, DashStatus: StatusFailed},
	}
	m.cursor = 0

	m.handleKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	vis := m.visibleSessions()
	if m.cursor < 0 || m.cursor >= len(vis) || vis[m.cursor].ID() != "failed" {
		t.Fatalf("ctrl+g did not move the cursor to the attention session; cursor=%d", m.cursor)
	}
}

// In group view the cursor indexes display ROWS (headers included), so ctrl+g
// must land on the attention session's row — not stuff a raw session index into
// the row cursor — and must expand the group if the target is collapsed
// (§1b "ctrl+g jump sets a session index into a display-row cursor"). Otherwise
// post-jump approve/suspend/destroy would target the wrong highlighted row.
func TestCtrlGGroupViewLandsOnRowAndExpands(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{State: session.State{ID: "a1", Status: session.StatusRunning, ProjectPath: "/r/alpha"}, DashStatus: StatusIdle},
		{State: session.State{ID: "b1", Status: session.StatusRunning, ProjectPath: "/r/beta"}, DashStatus: StatusIdle},
		{State: session.State{ID: "b2", Status: session.StatusRunning, ProjectPath: "/r/beta"}, DashStatus: StatusFailed},
	}
	m.toggleGroupView()
	// Collapse the beta group so the attention session (b2) is off-screen.
	m.groupView.repos["beta"] = false
	m.cursor = 0

	m.handleKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	if !m.groupView.repos["beta"] {
		t.Fatal("ctrl+g did not expand the collapsed group holding the attention session")
	}
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		t.Fatalf("cursor %d out of range for %d rows", m.cursor, len(rows))
	}
	row := rows[m.cursor]
	if row.session == nil || row.session.ID() != "b2" {
		t.Fatalf("ctrl+g landed the row cursor on %+v, want the b2 session row", row)
	}
	if sel := m.selectedRowSession(); sel == nil || sel.ID() != "b2" {
		t.Fatalf("selectedRowSession after ctrl+g = %v, want b2", sel)
	}
}
