package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	entry := Entry{
		SandboxSessionID: "claude-sdk-test",
		Backend:          "claude-sdk",
		ProjectPath:      "/Users/cullen/git/homelab",
		Namespace:        "agent-sessions",
		SandboxName:      "claude-sdk-test",
		CreatedAt:        time.Now(),
		LastActivity:     time.Now(),
		LastEventSeq:     42,
	}

	if err := idx.Save("claude-sdk-test", entry); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file exists
	path := filepath.Join(dir, "claude-sdk-test", "session.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	loaded, err := idx.Load("claude-sdk-test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.SandboxSessionID != entry.SandboxSessionID {
		t.Errorf("id: got %q, want %q", loaded.SandboxSessionID, entry.SandboxSessionID)
	}
	if loaded.Backend != entry.Backend {
		t.Errorf("backend: got %q, want %q", loaded.Backend, entry.Backend)
	}
	if loaded.LastEventSeq != entry.LastEventSeq {
		t.Errorf("seq: got %d, want %d", loaded.LastEventSeq, entry.LastEventSeq)
	}
	// RunnerToken should not be persisted
	if loaded.RunnerToken != "" {
		t.Errorf("token should not be in JSON, got %q", loaded.RunnerToken)
	}

	if err := idx.Delete("claude-sdk-test"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should be deleted")
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	for _, id := range []string{"session-a", "session-b", "session-c"} {
		entry := Entry{
			SandboxSessionID: id,
			Backend:          "claude-sdk",
		}
		if err := idx.Save(id, entry); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}

	entries, err := idx.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
}

// RecentProjects orders distinct project paths most-recently-active first,
// dedupes a path shared by several sessions (keeping its most recent slot),
// skips entries with no path, and honors the cap (T10 — the dashboard directory
// picker's recents source).
func TestRecentProjects(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	save := func(id, path string, last time.Time, created time.Time) {
		t.Helper()
		if err := idx.Save(id, Entry{
			SandboxSessionID: id,
			ProjectPath:      path,
			LastActivity:     last,
			CreatedAt:        created,
		}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	save("s-old", "/proj/old", base.Add(1*time.Hour), base)
	save("s-newest", "/proj/newest", base.Add(4*time.Hour), base)
	// /proj/dup appears twice; the fresher session must define its rank.
	save("s-dup-old", "/proj/dup", base.Add(2*time.Hour), base)
	save("s-dup-new", "/proj/dup", base.Add(3*time.Hour), base)
	// No LastActivity: falls back to CreatedAt (the freshest of all).
	save("s-created-only", "/proj/created-only", time.Time{}, base.Add(5*time.Hour))
	// No path at all: skipped.
	save("s-no-path", "", base.Add(6*time.Hour), base)

	got := idx.RecentProjects(0)
	want := []string{"/proj/created-only", "/proj/newest", "/proj/dup", "/proj/old"}
	if len(got) != len(want) {
		t.Fatalf("RecentProjects = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RecentProjects[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}

	if capped := idx.RecentProjects(2); len(capped) != 2 || capped[0] != want[0] || capped[1] != want[1] {
		t.Errorf("RecentProjects(2) = %v, want the top two of %v", capped, want)
	}

	// Empty index: nil, no error path.
	if got := New(t.TempDir()).RecentProjects(5); got != nil {
		t.Errorf("empty index RecentProjects = %v, want nil", got)
	}
}

func TestListEmpty(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)
	entries, err := idx.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil, got %v", entries)
	}
}

func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)
	_, err := idx.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing entry")
	}
}

// Snapshot persists across a save/load round-trip so the dashboard can hydrate
// its live read-model on relaunch (status/usage) without replaying history.
func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	entry := Entry{
		SandboxSessionID: "snap-1",
		Backend:          "claude-sdk",
		LastEventSeq:     128,
		Snapshot: &Snapshot{
			DashStatus:   "waiting",
			Model:        "opus-4.8",
			InputTokens:  1000,
			OutputTokens: 200,
			TotalCostUSD: 0.42,
		},
	}
	if err := idx.Save("snap-1", entry); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := idx.Load("snap-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Snapshot == nil {
		t.Fatal("snapshot not persisted")
	}
	if loaded.Snapshot.DashStatus != "waiting" || loaded.Snapshot.Model != "opus-4.8" {
		t.Errorf("snapshot fields: got %+v", loaded.Snapshot)
	}
	if loaded.Snapshot.InputTokens != 1000 || loaded.LastEventSeq != 128 {
		t.Errorf("seq/tokens: seq=%d in=%d", loaded.LastEventSeq, loaded.Snapshot.InputTokens)
	}
}

// A partial Save (e.g. a rename) must not drop a previously-persisted Snapshot:
// mergeEntry fills the nil incoming Snapshot from the on-disk entry.
func TestSnapshotPreservedByPartialSave(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	if err := idx.Save("snap-2", Entry{
		SandboxSessionID: "snap-2",
		LastEventSeq:     7,
		Snapshot:         &Snapshot{DashStatus: "busy"},
	}); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	// A later partial save that only sets a rename and carries no snapshot.
	if err := idx.Save("snap-2", Entry{RenamedTitle: "my work"}); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	loaded, err := idx.Load("snap-2")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.RenamedTitle != "my work" {
		t.Errorf("rename not applied: %q", loaded.RenamedTitle)
	}
	if loaded.Snapshot == nil || loaded.Snapshot.DashStatus != "busy" {
		t.Errorf("snapshot clobbered by partial save: %+v", loaded.Snapshot)
	}
}

// T6: AutoTitle (runner-generated) persists alongside RenamedTitle and survives
// a save/load round-trip.
func TestAutoTitleRoundTrip(t *testing.T) {
	dir := t.TempDir()
	idx := New(dir)

	entry := Entry{
		SandboxSessionID: "claude-sdk-t6",
		Backend:          "claude-sdk",
		AutoTitle:        "fix auth race condition",
		RenamedTitle:     "",
	}
	if err := idx.Save("claude-sdk-t6", entry); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := idx.Load("claude-sdk-t6")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.AutoTitle != entry.AutoTitle {
		t.Errorf("AutoTitle: got %q, want %q", loaded.AutoTitle, entry.AutoTitle)
	}
}
