package dashboard

import (
	"errors"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

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
		{"permqueue-beats-filtering", func(m *Model) {
			m.permQueue.open = true
			m.filtering = true
		}, dctxPermQueue},
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
		"r", "esc", "q", "R", "b", "space", "ctrl+g", "ctrl+k", "q", "?",
		"g", "G", "k", "j", "/", "s", "S", "\\", "enter", "a", "d", "n",
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

// TestQTruthful is the §2d fix: with a session waiting on a permission, q opens
// the queue and the footer advertises "perm queue"; with nothing waiting, q quits
// and the footer advertises "quit". The footer renders from the SAME table that
// dispatches, so it can never lie about what q does.
func TestQTruthful(t *testing.T) {
	// Waiting: q opens the queue and the footer says "perm queue".
	m := New(nil)
	m.sessions = []Session{{
		State:            session.State{ID: "w", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusWaiting, PendingPermissionID: "perm1"},
	}}
	if len(m.permQueueItems()) == 0 {
		t.Fatal("test setup: expected a non-empty permission queue")
	}
	m.handleKey(keyMsg("q"))
	if !m.permQueue.open {
		t.Fatal("q did not open the permission queue while a session was waiting")
	}
	if !footerHasDesc(m.shortHelp(), "perm queue") {
		t.Fatal("footer did not advertise 'perm queue' while a session was waiting")
	}
	if footerHasDesc(m.shortHelp(), "quit") {
		t.Fatal("footer advertised 'quit' while a session was waiting (both q slots visible)")
	}

	// Empty queue: q quits and the footer says "quit".
	m2 := New(nil)
	m2.sessions = []Session{{
		State:            session.State{ID: "idle", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}}
	if len(m2.permQueueItems()) != 0 {
		t.Fatal("test setup: expected an empty permission queue")
	}
	_, cmd := m2.handleKey(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q with an empty queue produced no command (expected quit)")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q with an empty queue did not produce tea.Quit")
	}
	if !footerHasDesc(m2.shortHelp(), "quit") {
		t.Fatal("footer did not advertise 'quit' with an empty queue")
	}
	if footerHasDesc(m2.shortHelp(), "perm queue") {
		t.Fatal("footer advertised 'perm queue' with an empty queue")
	}
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
