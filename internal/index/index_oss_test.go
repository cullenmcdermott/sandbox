package index

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A corrupt/partial session.json must produce a decode error from Load, and
// List must skip it observably rather than returning it as a zero entry or
// failing the whole listing.
func TestLoadCorruptReturnsDecodeError(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	entryDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Truncated/garbage JSON: a partial write that never finished.
	if err := os.WriteFile(filepath.Join(entryDir, "session.json"), []byte(`{"backend":"claude`), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	if _, err := idx.Load("broken"); err == nil {
		t.Fatal("Load of corrupt entry: expected a decode error, got nil")
	}
}

func TestListSkipsCorruptEntry(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	// One good entry.
	if err := idx.Save("good", Entry{SandboxSessionID: "good", Backend: "claude-sdk"}); err != nil {
		t.Fatalf("save good: %v", err)
	}
	// One corrupt entry written directly to disk.
	badDir := filepath.Join(dir, "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir bad: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "session.json"), []byte("not json at all"), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	entries, err := idx.List()
	if err != nil {
		t.Fatalf("List returned error, want it to tolerate corrupt entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1 (corrupt one skipped)", len(entries))
	}
	if entries[0].SandboxSessionID != "good" {
		t.Errorf("List returned %q, want only the good entry", entries[0].SandboxSessionID)
	}
}

// Save must create session.json with 0600 perms (owner-only); the index can
// carry tokens/paths the user does not want world-readable.
func TestSavePerms0600(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	if err := idx.Save("perm", Entry{SandboxSessionID: "perm"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "perm", "session.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("session.json perms = %o, want 600", got)
	}
}

// Save and Delete must reject path-traversal ids through the public API, not
// only via the internal validateID helper.
func TestSaveDeleteRejectTraversal(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	for _, id := range []string{"../escape", "..", "a/../../b"} {
		if err := idx.Save(id, Entry{SandboxSessionID: id}); err == nil {
			t.Errorf("Save(%q) = nil, want an escape error", id)
		}
		if err := idx.Delete(id); err == nil {
			t.Errorf("Delete(%q) = nil, want an escape error", id)
		}
	}
}

// A re-seed Save that constructs a partial Entry (only the cluster-known
// fields) must not clobber a previously-persisted RenamedTitle/AutoTitle: the
// load-merge fills zero-valued incoming fields from the on-disk entry.
func TestSaveMergePreservesTitles(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	// First: a rename persists a display label (load-modify-save style).
	if err := idx.Save("sess", Entry{
		SandboxSessionID: "sess",
		Backend:          "claude-sdk",
		RenamedTitle:     "my custom name",
		AutoTitle:        "auto summary",
	}); err != nil {
		t.Fatalf("save 1: %v", err)
	}

	// Then: a re-seed (e.g. session re-create) writes a fresh partial Entry
	// without the titles. The merge must keep them.
	now := time.Now()
	if err := idx.Save("sess", Entry{
		SandboxSessionID: "sess",
		Backend:          "claude-sdk",
		ProjectPath:      "/some/project",
		Namespace:        "agent-sessions",
		SandboxName:      "sess",
		CreatedAt:        now,
		LastActivity:     now,
	}); err != nil {
		t.Fatalf("save 2 (re-seed): %v", err)
	}

	loaded, err := idx.Load("sess")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.RenamedTitle != "my custom name" {
		t.Errorf("RenamedTitle clobbered by re-seed: got %q, want %q", loaded.RenamedTitle, "my custom name")
	}
	if loaded.AutoTitle != "auto summary" {
		t.Errorf("AutoTitle clobbered by re-seed: got %q, want %q", loaded.AutoTitle, "auto summary")
	}
	// And the re-seed's own non-zero fields must have been applied.
	if loaded.ProjectPath != "/some/project" {
		t.Errorf("ProjectPath: got %q, want %q", loaded.ProjectPath, "/some/project")
	}
	if loaded.Namespace != "agent-sessions" {
		t.Errorf("Namespace: got %q, want %q", loaded.Namespace, "agent-sessions")
	}
}

// A non-empty incoming field always wins over the on-disk value (the merge only
// defers to disk for zero values, so an explicit rename overwrites the old one).
func TestSaveMergeNonZeroWins(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	if err := idx.Save("sess", Entry{SandboxSessionID: "sess", RenamedTitle: "old"}); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if err := idx.Save("sess", Entry{SandboxSessionID: "sess", RenamedTitle: "new"}); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	loaded, err := idx.Load("sess")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.RenamedTitle != "new" {
		t.Errorf("RenamedTitle: got %q, want %q (non-zero incoming must win)", loaded.RenamedTitle, "new")
	}
}

// Concurrent Saves that each carry only one locally-owned field must not lose an
// update: with the per-path lock + load-merge, a rename and an auto-title set
// racing on the same entry both survive. Run with -race to exercise the guard.
func TestSaveConcurrentNoLostUpdate(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	// Seed a base entry.
	if err := idx.Save("sess", Entry{SandboxSessionID: "sess", Backend: "claude-sdk"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	// Goroutine A: load-modify-save a RenamedTitle (mirrors SaveTitle).
	go func() {
		defer wg.Done()
		e, err := idx.Load("sess")
		if err != nil {
			t.Errorf("A load: %v", err)
			return
		}
		e.RenamedTitle = "renamed"
		if err := idx.Save("sess", e); err != nil {
			t.Errorf("A save: %v", err)
		}
	}()
	// Goroutine B: load-modify-save an AutoTitle (mirrors SaveAutoTitle).
	go func() {
		defer wg.Done()
		e, err := idx.Load("sess")
		if err != nil {
			t.Errorf("B load: %v", err)
			return
		}
		e.AutoTitle = "auto"
		if err := idx.Save("sess", e); err != nil {
			t.Errorf("B save: %v", err)
		}
	}()
	wg.Wait()

	loaded, err := idx.Load("sess")
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	// Even if A and B read the same base concurrently, the load-merge in Save
	// fills the zero field from the entry the other goroutine wrote, so neither
	// update is lost.
	if loaded.RenamedTitle != "renamed" {
		t.Errorf("RenamedTitle lost: got %q, want %q", loaded.RenamedTitle, "renamed")
	}
	if loaded.AutoTitle != "auto" {
		t.Errorf("AutoTitle lost: got %q, want %q", loaded.AutoTitle, "auto")
	}
}
