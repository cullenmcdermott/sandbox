package dashboard

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
func (f *fakeTitleStore) SaveAgentSessionID(id session.ID, c string) {
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

// Renaming via the keyboard: R opens the overlay, typed characters land in the
// rename buffer, and enter commits. Regression test for the bug where keypresses
// fell through to navigation because handleKey never routed to the rename buffer.
func TestRenameKeyboardInput(t *testing.T) {
	store := newFakeTitleStore()
	m := New(nil).WithTitleStore(store)
	m.sessions = []Session{
		SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/a"}),
	}
	m.cursor = 0

	m.handleKey(keyMsg("R"))
	if !m.renaming {
		t.Fatal("R should open the rename overlay")
	}

	// Clear the pre-filled title and type a new name.
	m.renameBuf = ""
	for _, k := range []string{"n", "e", "w"} {
		m.handleKey(keyMsg(k))
	}
	if m.renameBuf != "new" {
		t.Fatalf("renameBuf = %q, want %q", m.renameBuf, "new")
	}

	// Backspace removes the last rune.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.renameBuf != "ne" {
		t.Fatalf("after backspace renameBuf = %q, want %q", m.renameBuf, "ne")
	}

	m.handleKey(keyMsg("enter"))
	if m.renaming {
		t.Error("enter should commit and close the rename overlay")
	}
	if got := store.LoadTitle("s1"); got != "ne" {
		t.Fatalf("store title = %q, want %q", got, "ne")
	}
}

// esc cancels a rename without persisting anything.
func TestRenameEscapeCancels(t *testing.T) {
	store := newFakeTitleStore()
	m := New(nil).WithTitleStore(store)
	m.sessions = []Session{
		SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/a"}),
	}
	m.cursor = 0

	m.handleKey(keyMsg("R"))
	m.renameBuf = "discard"
	m.handleKey(keyMsg("esc"))

	if m.renaming {
		t.Error("esc should close the rename overlay")
	}
	if got := store.LoadTitle("s1"); got != "" {
		t.Fatalf("esc should not persist; store title = %q", got)
	}
}

func TestTruncatePreservesANSISequences(t *testing.T) {
	styled := "\x1b[31mabcdef\x1b[0m"
	got := truncate(styled, 4)
	if lipgloss.Width(got) > 4 {
		t.Fatalf("truncate width = %d, want <= 4 (got %q)", lipgloss.Width(got), got)
	}
	if plain := stripANSI(got); plain != "abc…" {
		t.Fatalf("truncate visible text = %q, want %q (raw %q)", plain, "abc…", got)
	}
	if strings.Contains(got, "\x1b[31") && !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("truncate dropped reset from styled output: %q", got)
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
