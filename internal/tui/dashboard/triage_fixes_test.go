package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// fakeTitleStore is an in-memory TitleStore for tests.
type fakeTitleStore struct {
	m      map[session.ID]string
	auto   map[session.ID]string
	claude map[session.ID]string
}

func newFakeTitleStore() *fakeTitleStore {
	return &fakeTitleStore{m: map[session.ID]string{}, auto: map[session.ID]string{}, claude: map[session.ID]string{}}
}

func (f *fakeTitleStore) LoadTitle(id session.ID) string        { return f.m[id] }
func (f *fakeTitleStore) SaveTitle(id session.ID, t string)     { f.m[id] = t }
func (f *fakeTitleStore) LoadAutoTitle(id session.ID) string    { return f.auto[id] }
func (f *fakeTitleStore) SaveAutoTitle(id session.ID, t string) { f.auto[id] = t }
func (f *fakeTitleStore) SaveClaudeSessionID(id session.ID, c string) {
	if f.claude == nil {
		f.claude = map[session.ID]string{}
	}
	f.claude[id] = c
}

// T5: committing a rename writes through the title store so it can be restored.
func TestCommitRenamePersists(t *testing.T) {
	store := newFakeTitleStore()
	m := New(nil).WithTitleStore(store)
	m.sessions = []Session{
		SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/a"}),
	}
	m.cursor = 0
	m.renaming = true
	m.renameBuf = "  my session  " // surrounding space should be trimmed

	m.commitRename()

	if got := store.LoadTitle("s1"); got != "my session" {
		t.Fatalf("store title = %q, want %q", got, "my session")
	}
	if got := m.sessions[0].RenamedTitle; got != "my session" {
		t.Fatalf("in-memory title = %q, want %q", got, "my session")
	}
	if m.renaming {
		t.Error("renaming should be cleared after commit")
	}
}

// T5: a fresh seed restores the persisted rename from the store, even though the
// cluster state carries no local label.
func TestSeedRestoresPersistedTitle(t *testing.T) {
	store := newFakeTitleStore()
	store.SaveTitle("s1", "renamed one")
	m := New(nil).WithTitleStore(store)

	m.applySeed([]session.State{
		{ID: "s1", Status: session.StatusRunning, ProjectPath: "/a"},
		{ID: "s2", Status: session.StatusRunning, ProjectPath: "/b"},
	})

	for _, s := range m.sessions {
		switch s.ID() {
		case "s1":
			if s.DisplayTitle() != "renamed one" {
				t.Errorf("s1 DisplayTitle = %q, want %q", s.DisplayTitle(), "renamed one")
			}
		case "s2":
			if s.RenamedTitle != "" {
				t.Errorf("s2 should have no rename, got %q", s.RenamedTitle)
			}
		}
	}
}

// T12: the chat header label is action-oriented and disambiguates "done/ready"
// (StatusNeedsInput) from "needs you" (StatusWaiting).
func TestChatStatusLabel(t *testing.T) {
	cases := map[SessionStatus]string{
		StatusBusy:       "working",
		StatusWaiting:    "awaiting approval",
		StatusNeedsInput: "ready for input",
		StatusIdle:       "idle",
	}
	for st, want := range cases {
		if got := chatStatusLabel(st); got != want {
			t.Errorf("chatStatusLabel(%v) = %q, want %q", st, got, want)
		}
	}
	// The "done" state must not be labeled like it's blocked on the user.
	if chatStatusLabel(StatusNeedsInput) == chatStatusLabel(StatusWaiting) {
		t.Error("ready-for-input and awaiting-approval must read differently")
	}
}
