package cli

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// T6: indexTitleStore persists the auto title to the index entry without
// clobbering a user-chosen RenamedTitle, and load reads it back.
func TestIndexTitleStoreAutoTitle(t *testing.T) {
	// newIndex() resolves the index root from index.DefaultRoot(), which is
	// derived from os.UserHomeDir() ($HOME). Redirect HOME to a temp dir so the
	// store reads/writes a hermetic index, not the real ~/.local/share.
	t.Setenv("HOME", t.TempDir())

	var store indexTitleStore
	id := session.ID("claude-sdk-store")

	store.SaveTitle(id, "user label")
	store.SaveAutoTitle(id, "auto summary")

	if got := store.LoadAutoTitle(id); got != "auto summary" {
		t.Errorf("LoadAutoTitle = %q, want %q", got, "auto summary")
	}
	// SaveAutoTitle must not clobber the rename.
	if got := store.LoadTitle(id); got != "user label" {
		t.Errorf("LoadTitle = %q, want %q (rename clobbered)", got, "user label")
	}
}
