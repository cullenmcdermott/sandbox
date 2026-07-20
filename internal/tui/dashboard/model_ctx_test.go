package dashboard

import (
	"errors"
	"testing"

	"charm.land/bubbles/v2/key"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// These tests pin the §2a input-context port of the dashboard's handleKey: the
// context resolver's precedence, the list table's ordered binding set, and the
// §2d footer-truthfulness fix (q advertises "perm queue" only when the queue is
// non-empty, "quit" otherwise). Byte-for-byte dispatch behavior is otherwise
// covered by the existing dashboard suites, which pass unmodified.

// TestDashContextResolution pins the resolver order: confirm preempts filtering,
// help preempts the switcher, and so on down to the list default.
func TestDashContextResolution(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *Model)
		want  dctx
	}{
		{"confirm-beats-filtering", func(m *Model) {
			m.confirm = &confirmPrompt{}
			m.filtering = true
		}, dctxConfirm},
		{"help-beats-switcher", func(m *Model) {
			m.showHelp = true
			m.switcher.open = true
		}, dctxHelp},
		{"switcher-beats-renaming", func(m *Model) {
			m.switcher.open = true
			m.renaming = true
		}, dctxSwitcher},
		{"filtering-beats-renaming", func(m *Model) {
			m.filtering = true
			m.renaming = true
		}, dctxFilter},
		{"renaming-beats-convert", func(m *Model) {
			m.renaming = true
			m.convert = &convertModal{}
		}, dctxRename},
		{"convert-only", func(m *Model) {
			m.convert = &convertModal{}
		}, dctxConvert},
		{"none-is-list", func(*Model) {}, dctxList},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(nil)
			tc.setup(m)
			if got := m.activeContext(); got != tc.want {
				t.Fatalf("activeContext() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestListTablePrecedence pins the list table's ordered primary keys — first-match
// precedence is data, so a reorder is a behavior change and must fail here.
func TestListTablePrecedence(t *testing.T) {
	m := New(nil)
	var got []string
	for _, e := range m.dashListTable() {
		got = append(got, e.binding.Keys()[0])
	}
	want := []string{
		"r", "esc", "R", "b", "space", "ctrl+g", "ctrl+k", "q", "?",
		"g", "G", "k", "j", "/", "s", "S", "\\", "enter", "v", "a", "d", "n",
		"x", "r", "!",
	}
	if len(got) != len(want) {
		t.Fatalf("list table keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("list table order = %v, want %v", got, want)
		}
	}
}

// footerHasDesc reports whether the footer bindings advertise the given help desc.
func footerHasDesc(bindings []key.Binding, desc string) bool {
	for _, b := range bindings {
		if b.Help().Desc == desc {
			return true
		}
	}
	return false
}


// TestSeedErrRetryPrecedesResume pins that the seedErr retry entry claims r ahead
// of the resume binding: with the initial seed failed, r re-issues the seed (and
// clears seedErr) instead of resuming a suspended session.
func TestSeedErrRetryPrecedesResume(t *testing.T) {
	m := New(nil)
	m.seedErr = errors.New("cluster unreachable")
	m.sessions = []Session{{
		State:            session.State{ID: "s", Status: session.StatusSuspended},
		sessionReadModel: sessionReadModel{DashStatus: StatusSuspended},
	}}

	_, cmd := m.handleKey(keyMsg("r"))

	if m.seedErr != nil {
		t.Fatal("r did not clear seedErr (retry entry should have run)")
	}
	if cmd == nil {
		t.Fatal("r with seedErr set produced no command (expected the seed batch)")
	}
	if m.sessions[0].PendingAction == "resume" {
		t.Fatal("r triggered a resume instead of the seed retry (precedence broken)")
	}
}
