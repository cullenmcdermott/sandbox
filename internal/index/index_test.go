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
