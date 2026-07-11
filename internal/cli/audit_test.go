package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// TestTranscriptAuditLog pins §1d: learning a session's Claude SDK session id
// appends one provenance line to the append-only audit log, deduped per session
// (so the log grows once per session, not per event).
func TestTranscriptAuditLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store := indexTitleStore{}
	store.SaveClaudeSessionID("claude-sdk-a", "uuid-1")
	store.SaveClaudeSessionID("claude-sdk-a", "uuid-1") // dedupe: already recorded
	store.SaveClaudeSessionID("claude-sdk-b", "uuid-2")
	store.SaveClaudeSessionID("claude-sdk-c", "") // empty id: no-op

	root, err := index.DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "transcript-audit.jsonl"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("audit log has %d lines, want 2:\n%s", len(lines), data)
	}
	want := map[string]string{"claude-sdk-a": "uuid-1", "claude-sdk-b": "uuid-2"}
	for _, l := range lines {
		var rec transcriptAuditRecord
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("unmarshal %q: %v", l, err)
		}
		if want[rec.SandboxSession] != rec.ClaudeSessionID {
			t.Errorf("record %+v does not match expected mapping %v", rec, want)
		}
		delete(want, rec.SandboxSession)
	}
	if len(want) != 0 {
		t.Errorf("missing audit records for %v", want)
	}
}
