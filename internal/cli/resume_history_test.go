package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// TestSyncResumeHistory verifies the laptop resume-picker bridge: a sandbox
// session whose transcript has synced down gets a history.jsonl entry; one whose
// transcript is absent does NOT (it would list-but-fail); and re-running is
// idempotent.
func TestSyncResumeHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "") // force the ~/.claude fallback under temp HOME

	cfgDir := filepath.Join(home, ".claude")
	root := filepath.Join(home, ".local", "share", "sandbox", "remote-sessions")
	idx := index.New(root)

	synced := "11111111-1111-1111-1111-111111111111"
	unsynced := "22222222-2222-2222-2222-222222222222"
	if err := idx.Save("sess-synced", index.Entry{
		SandboxSessionID: "sess-synced",
		ProjectPath:      "/Users/cullen/git/sandbox",
		ClaudeSessionID:  synced,
		AutoTitle:        "add resume support",
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Save("sess-unsynced", index.Entry{
		SandboxSessionID: "sess-unsynced",
		ProjectPath:      "/Users/cullen/git/other",
		ClaudeSessionID:  unsynced,
	}); err != nil {
		t.Fatal(err)
	}

	// Only the first session's transcript has synced down (any projects/* subdir).
	tdir := filepath.Join(cfgDir, "projects", "-Users-cullen-git-sandbox")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, synced+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncResumeHistory(); err != nil {
		t.Fatalf("syncResumeHistory: %v", err)
	}

	historyPath := filepath.Join(cfgDir, "history.jsonl")
	got := readFile(t, historyPath)
	if !strings.Contains(got, synced) {
		t.Errorf("synced session missing from history.jsonl:\n%s", got)
	}
	if strings.Contains(got, unsynced) {
		t.Errorf("unsynced session must NOT be added (would list-but-fail):\n%s", got)
	}
	if !strings.Contains(got, `"project":"/Users/cullen/git/sandbox"`) {
		t.Errorf("project field wrong:\n%s", got)
	}
	if !strings.Contains(got, "add resume support") {
		t.Errorf("display should fall back to the auto title:\n%s", got)
	}

	// Idempotent: a second run adds no duplicate.
	if err := syncResumeHistory(); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readFile(t, historyPath), synced); n != 1 {
		t.Errorf("expected exactly 1 entry for the synced session after re-run, got %d", n)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
