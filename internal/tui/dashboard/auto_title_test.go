package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// T6: DisplayTitle precedence is RenamedTitle > AutoTitle > derived basename.
func TestDisplayTitlePrecedence(t *testing.T) {
	cases := []struct {
		name    string
		renamed string
		auto    string
		title   string // derived basename (Session.Title)
		want    string
	}{
		{"renamed wins over all", "my label", "auto sum", "basename", "my label"},
		{"auto wins when no rename", "", "auto sum", "basename", "auto sum"},
		{"basename when neither", "", "", "basename", "basename"},
		{"renamed wins over auto only", "my label", "auto sum", "", "my label"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Session{Title: tc.title, AutoTitle: tc.auto, RenamedTitle: tc.renamed}
			if got := s.DisplayTitle(); got != tc.want {
				t.Errorf("DisplayTitle() = %q, want %q", got, tc.want)
			}
		})
	}
}

// T6: a session.title event populates Session.AutoTitle and is otherwise a
// no-op on the six-state status (returns false: status unchanged).
func TestApplyRunnerEventSessionTitle(t *testing.T) {
	sess := SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/repo"})
	payload, _ := json.Marshal(session.SessionTitlePayload{Title: "add login flow"})
	ev := session.Event{Type: session.EventSessionTitle, Payload: payload}

	changed := ApplyRunnerEvent(&sess, ev)

	if changed {
		t.Error("session.title must not report a six-state status change")
	}
	if sess.AutoTitle != "add login flow" {
		t.Errorf("AutoTitle = %q, want %q", sess.AutoTitle, "add login flow")
	}
	if sess.DisplayTitle() != "add login flow" {
		t.Errorf("DisplayTitle() = %q, want %q", sess.DisplayTitle(), "add login flow")
	}
}

// T6: an empty title payload is ignored (keeps the derived basename).
func TestApplyRunnerEventSessionTitleEmptyIgnored(t *testing.T) {
	sess := SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/repo"})
	payload, _ := json.Marshal(session.SessionTitlePayload{Title: ""})
	ApplyRunnerEvent(&sess, session.Event{Type: session.EventSessionTitle, Payload: payload})
	if sess.AutoTitle != "" {
		t.Errorf("AutoTitle = %q, want empty", sess.AutoTitle)
	}
	if sess.DisplayTitle() != "repo" {
		t.Errorf("DisplayTitle() = %q, want %q", sess.DisplayTitle(), "repo")
	}
}

// T6: an incoming session.title event persists the auto title through the
// TitleStore so it survives a re-seed (the cluster state carries no local label).
func TestSessionTitleEventPersistsAutoTitle(t *testing.T) {
	store := newFakeTitleStore()
	m := New(nil).WithTitleStore(store)
	m.sessions = []Session{
		SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/repo"}),
	}

	payload, _ := json.Marshal(session.SessionTitlePayload{Title: "add login flow"})
	m.handleRunnerEvent(RunnerEventMsg{
		ID:    "s1",
		Event: session.Event{Type: session.EventSessionTitle, Payload: payload},
	})

	if got := store.LoadAutoTitle("s1"); got != "add login flow" {
		t.Fatalf("stored auto title = %q, want %q", got, "add login flow")
	}
}

// T6: a fresh seed restores the persisted auto title; a later RenamedTitle still
// wins.
func TestSeedRestoresAutoTitle(t *testing.T) {
	store := newFakeTitleStore()
	store.SaveAutoTitle("s1", "auto summary")
	store.SaveAutoTitle("s2", "auto two")
	store.SaveTitle("s2", "user label")
	m := New(nil).WithTitleStore(store)

	m.applySeed([]session.State{
		{ID: "s1", Status: session.StatusRunning, ProjectPath: "/a"},
		{ID: "s2", Status: session.StatusRunning, ProjectPath: "/b"},
	})

	for _, s := range m.sessions {
		switch s.ID() {
		case "s1":
			if s.DisplayTitle() != "auto summary" {
				t.Errorf("s1 DisplayTitle = %q, want %q", s.DisplayTitle(), "auto summary")
			}
		case "s2":
			if s.DisplayTitle() != "user label" {
				t.Errorf("s2 DisplayTitle = %q, want %q", s.DisplayTitle(), "user label")
			}
		}
	}
}
