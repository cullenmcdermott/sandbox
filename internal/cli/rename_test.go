package cli

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// T6: `sandbox rename <id> <name>` writes RenamedTitle (not AutoTitle) through
// the title store, so a custom name always wins over the runner auto title.
func TestRenameWritesRenamedTitle(t *testing.T) {
	store := newFakeCLITitleStore()
	id := session.ID("claude-sdk-rename")

	exists := func(session.ID) bool { return true }
	if err := runRename(store, exists, string(id), "my custom name"); err != nil {
		t.Fatalf("runRename: %v", err)
	}
	if got := store.LoadTitle(id); got != "my custom name" {
		t.Errorf("RenamedTitle = %q, want %q", got, "my custom name")
	}
	if got := store.LoadAutoTitle(id); got != "" {
		t.Errorf("AutoTitle should be untouched, got %q", got)
	}
}

// T6: rename rejects an empty name (it would otherwise clear the override).
func TestRenameRejectsEmptyName(t *testing.T) {
	store := newFakeCLITitleStore()
	if err := runRename(store, nil, "id", "   "); err == nil {
		t.Fatal("expected error for empty name")
	}
}

// RV: rename rejects a non-existent session id so a typo doesn't silently
// create a phantom local index entry.
func TestRenameRejectsUnknownSession(t *testing.T) {
	store := newFakeCLITitleStore()
	notFound := func(session.ID) bool { return false }
	if err := runRename(store, notFound, "typo-id", "name"); err == nil {
		t.Fatal("expected error for unknown session")
	}
	if got := store.LoadTitle("typo-id"); got != "" {
		t.Errorf("no title should be written for an unknown session, got %q", got)
	}
}

// T6: --name at creation persists RenamedTitle for the session via the same
// write path, so the custom name overrides the runner auto title from the start.
func TestApplyCreateNameWritesRenamedTitle(t *testing.T) {
	store := newFakeCLITitleStore()
	id := session.ID("claude-sdk-create")

	applyCreateName(store, id, "  start label  ")

	if got := store.LoadTitle(id); got != "start label" {
		t.Errorf("RenamedTitle = %q, want %q", got, "start label")
	}
}

// T6: an empty --name is a no-op (no override written).
func TestApplyCreateNameEmptyNoop(t *testing.T) {
	store := newFakeCLITitleStore()
	id := session.ID("claude-sdk-create2")
	applyCreateName(store, id, "")
	if got := store.LoadTitle(id); got != "" {
		t.Errorf("expected no rename, got %q", got)
	}
}

// fakeCLITitleStore is an in-memory titleWriter for CLI rename tests.
type fakeCLITitleStore struct {
	titles map[session.ID]string
	autos  map[session.ID]string
}

func newFakeCLITitleStore() *fakeCLITitleStore {
	return &fakeCLITitleStore{titles: map[session.ID]string{}, autos: map[session.ID]string{}}
}

func (f *fakeCLITitleStore) SaveTitle(id session.ID, t string)     { f.titles[id] = t }
func (f *fakeCLITitleStore) LoadTitle(id session.ID) string        { return f.titles[id] }
func (f *fakeCLITitleStore) SaveAutoTitle(id session.ID, t string) { f.autos[id] = t }
func (f *fakeCLITitleStore) LoadAutoTitle(id session.ID) string    { return f.autos[id] }
