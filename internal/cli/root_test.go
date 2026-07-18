package cli

import "testing"

// TestCodexCommandRegistered guards that `sandbox codex` is wired into the root
// command tree (item 5 of the codex Phase 1 plumbing). It mirrors the existing
// claude/opencode session-start commands; a missing registration would silently
// drop the subcommand.
func TestCodexCommandRegistered(t *testing.T) {
	root := NewRoot()
	for _, c := range root.Commands() {
		if c.Name() == "codex" {
			return
		}
	}
	t.Fatal("`sandbox codex` command is not registered on the root command")
}
