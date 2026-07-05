package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Group view used to iterate the raw m.sessions, so the `/` filter was inert:
// groups showed every session regardless of the query. Groups must now be built
// from visibleSessions() so the filter narrows group contents and drops
// now-empty groups.
func TestGroupViewRespectsFilter(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/r/alpha"}),
		SessionFromState(session.State{ID: "s2", Status: session.StatusRunning, ProjectPath: "/r/alpha"}),
		SessionFromState(session.State{ID: "s3", Status: session.StatusRunning, ProjectPath: "/r/beta"}),
	}
	m.toggleGroupView()

	// No filter: both groups + all three sessions are present.
	if got := len(m.visibleRows()); got != 5 {
		t.Fatalf("unfiltered grouped rows = %d, want 5 (2 headers + 3 sessions)", got)
	}

	// Filter to "beta": only the beta group and s3 survive; alpha drops entirely.
	m.filter = "beta"
	rows := m.visibleRows()
	if len(rows) != 2 {
		t.Fatalf("filtered grouped rows = %d, want 2 (beta header + s3), rows=%+v", len(rows), rows)
	}
	if rows[0].repo != "beta" {
		t.Fatalf("first row repo = %q, want beta header", rows[0].repo)
	}
	if rows[1].session == nil || rows[1].session.ID() != "s3" {
		t.Fatalf("second row = %+v, want session s3", rows[1])
	}
}

// While filtering in group view, down-nav must clamp against visibleRows()
// (headers included), not visibleSessions() — otherwise the trailing rows of a
// grouped list are unreachable with the query buffer open.
func TestFilterNavReachesLastGroupedRow(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/r/alpha"}),
		SessionFromState(session.State{ID: "s2", Status: session.StatusRunning, ProjectPath: "/r/alpha"}),
		SessionFromState(session.State{ID: "s3", Status: session.StatusRunning, ProjectPath: "/r/beta"}),
	}
	m.toggleGroupView()
	m.filtering = true // empty query: all rows visible

	last := len(m.visibleRows()) - 1 // 4: [hdr alpha, s1, s2, hdr beta, s3]
	for i := 0; i < 10; i++ {
		m.handleFilterKey("down")
	}
	if m.cursor != last {
		t.Fatalf("filter-mode down clamped to cursor %d, want last row %d", m.cursor, last)
	}
}

// j/k must be typeable in the filter query (they used to be intercepted as
// nav, so they could never appear in a search). Only arrow keys navigate.
func TestFilterLettersAreTypeable(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/r/jk"}),
	}
	m.filtering = true
	for _, ks := range []string{"j", "k"} {
		m.handleFilterKey(ks)
	}
	if m.filterBuf != "jk" {
		t.Fatalf("filterBuf = %q, want %q (j/k should append, not navigate)", m.filterBuf, "jk")
	}
}
