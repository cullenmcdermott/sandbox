package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// row_model_test.go pins the §2a consolidation: the session list is ONE ordered
// []listRow (visibleRows) the cursor indexes, and sessionAt(cursor) is the single
// accessor render, navigation, and actions all resolve through. These tests fix
// the CURRENT behavior so a future row class (S15 archive) can't silently drift
// render and actions apart again (the §1b bug).

func rowModelSessions() []Session {
	return []Session{
		SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/r/alpha"}),
		SessionFromState(session.State{ID: "s2", Status: session.StatusRunning, ProjectPath: "/r/alpha"}),
		SessionFromState(session.State{ID: "s3", Status: session.StatusRunning, ProjectPath: "/r/beta"}),
	}
}

// selectedSession (what actions use) must always name the session the render loop
// highlights: for a cursor on a session row, that is visibleRows()[cursor]. Walk
// every session-row index in group view and assert they agree — render highlights
// visibleRows()[cursor], actions act on sessionAt(cursor); a divergence is the §1b
// wrong-target bug.
func TestSessionAtAgreesWithRenderedRow(t *testing.T) {
	m := New(nil)
	m.sessions = rowModelSessions()
	m.toggleGroupView() // rows: [alpha hdr, s1, s2, beta hdr, s3]

	rows := m.visibleRows()
	for i, row := range rows {
		m.cursor = i
		sel := m.selectedSession()
		if row.kind == rowSession {
			// On a session row the accessor returns exactly that row's session — the
			// one the render loop draws as selected (i == m.cursor).
			if sel == nil || sel.ID() != row.session.ID() {
				t.Fatalf("cursor %d on session row %s: selectedSession=%v, want that row's session",
					i, row.session.ID(), sel)
			}
		}
	}
}

// A header row is never itself a selection target: with the cursor on a header,
// sessionAt resolves to the nearest session below it (then above). Pin that so the
// header row is transparent to actions rather than crashing or acting on nothing.
func TestSessionAtOnHeaderResolvesToSessionBelow(t *testing.T) {
	m := New(nil)
	m.sessions = rowModelSessions()
	m.toggleGroupView()

	rows := m.visibleRows()
	sawHeader := false
	for i, row := range rows {
		if row.kind != rowHeader {
			continue
		}
		sawHeader = true
		// The next session row below this header is what the cursor resolves to.
		var want session.ID
		for j := i + 1; j < len(rows); j++ {
			if rows[j].kind == rowSession {
				want = rows[j].session.ID()
				break
			}
		}
		sel := m.sessionAt(i)
		if sel == nil || sel.ID() != want {
			t.Fatalf("sessionAt(header idx %d) = %v, want session %q below", i, sel, want)
		}
	}
	if !sawHeader {
		t.Fatal("expected at least one header row in group view")
	}
}

// CURRENT behavior: toggling group view resets the cursor to the top (0) and
// clamps. Pin it — a later "preserve selection across toggle" change should update
// this test deliberately, not regress it by accident.
func TestGroupToggleResetsCursorToTop(t *testing.T) {
	m := New(nil)
	m.sessions = rowModelSessions()
	m.cursor = 2

	m.toggleGroupView()
	if m.cursor != 0 {
		t.Fatalf("cursor after group-view toggle = %d, want 0", m.cursor)
	}
	m.toggleGroupView() // back to flat
	if m.cursor != 0 {
		t.Fatalf("cursor after second toggle = %d, want 0", m.cursor)
	}
}

// In flat view visibleRows() is exactly one session row per visibleSessions()
// entry, in the same order, so sessionAt(i) == visibleSessions()[i]. This is the
// invariant that lets flat-view navigation index either interchangeably.
func TestFlatViewRowsMatchVisibleSessions(t *testing.T) {
	m := New(nil)
	m.sessions = rowModelSessions()

	rows := m.visibleRows()
	vis := m.visibleSessions()
	if len(rows) != len(vis) {
		t.Fatalf("flat rows = %d, visibleSessions = %d; want equal", len(rows), len(vis))
	}
	for i := range rows {
		if rows[i].kind != rowSession {
			t.Fatalf("flat row %d is a header; flat view must be all session rows", i)
		}
		m.cursor = i
		sel := m.selectedSession()
		if sel == nil || sel.ID() != vis[i].ID() {
			t.Fatalf("flat sessionAt(%d)=%v, visibleSessions[%d]=%s; want equal", i, sel, i, vis[i].ID())
		}
	}
}
