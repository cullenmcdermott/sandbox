package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestCompletionDesc pins the description column: the branch is added only when
// it says something the title does not.
func TestCompletionDesc(t *testing.T) {
	for _, tc := range []struct {
		name, title, branch, want string
	}{
		{"both", "auth refactor", "feat/auth", "auth refactor — feat/auth"},
		{"title only", "auth refactor", "", "auth refactor"},
		{"branch only", "", "feat/auth", "feat/auth"},
		{"neither", "", "", ""},
		{"branch already in title", "work on feat/auth", "feat/auth", "work on feat/auth"},
		{"whitespace is trimmed", "  auth  ", "  ", "auth"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := completionDesc(tc.title, tc.branch); got != tc.want {
				t.Errorf("completionDesc(%q, %q) = %q, want %q", tc.title, tc.branch, got, tc.want)
			}
		})
	}
}

// TestCompleteSessionArgOnlyCompletesTheFirstArgument pins that
// `sandbox rename <id> <name>` does not offer session ids where the new name
// goes — the one place a session-taking command has a second positional.
func TestCompleteSessionArgOnlyCompletesTheFirstArgument(t *testing.T) {
	cmd := &cobra.Command{Use: "rename"}
	got, directive := completeSessionArg(cmd, []string{"already-picked"}, "")
	if got != nil {
		t.Errorf("offered %v for the second argument, want none", got)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}

// TestCompletionCommandGeneratesScripts asserts each shell's generator actually
// emits its script (and that an unknown shell is rejected by cobra's ValidArgs
// rather than silently producing powershell).
func TestCompletionCommandGeneratesScripts(t *testing.T) {
	for _, tc := range []struct{ shell, marker string }{
		{"bash", "bash completion"},
		{"zsh", "#compdef"},
		{"fish", "fish completion"},
		{"powershell", "Register-ArgumentCompleter"},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			root := NewRoot()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{"completion", tc.shell})
			if err := root.Execute(); err != nil {
				t.Fatalf("completion %s: %v", tc.shell, err)
			}
			if !strings.Contains(strings.ToLower(out.String()), strings.ToLower(tc.marker)) {
				t.Errorf("completion %s output missing %q (%d bytes)", tc.shell, tc.marker, out.Len())
			}
		})
	}
}

// TestCompletionRejectsUnknownShell: an unknown shell must fail loudly rather
// than fall through to a default the user did not ask for.
func TestCompletionRejectsUnknownShell(t *testing.T) {
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"completion", "tcsh"})
	if err := root.Execute(); err == nil {
		t.Error("expected an error for an unsupported shell")
	}
}

// TestSessionCommandsHaveCompletion is the wiring pin: every command whose
// first argument is a session must offer completion. Adding a new one without a
// ValidArgsFunction fails here rather than being noticed months later.
func TestSessionCommandsHaveCompletion(t *testing.T) {
	root := NewRoot()
	want := map[string]bool{
		"attach": true, "suspend": true, "resume": true, "cancel": true,
		"destroy": true, "sync": true, "rename": true,
	}
	for _, c := range root.Commands() {
		name := c.Name()
		if !want[name] {
			continue
		}
		delete(want, name)
		if c.ValidArgsFunction == nil {
			t.Errorf("command %q takes a session id but has no ValidArgsFunction", name)
		}
	}
	for name := range want {
		t.Errorf("command %q not found on the root — did it get renamed?", name)
	}

	// The worktree subcommands that take a session too.
	for _, c := range root.Commands() {
		if c.Name() != "worktree" {
			continue
		}
		found := map[string]bool{}
		for _, sub := range c.Commands() {
			found[sub.Name()] = true
			if sub.Name() == "gc" {
				continue // gc takes no session argument
			}
			if sub.ValidArgsFunction == nil {
				t.Errorf("worktree %s takes a session query but has no ValidArgsFunction", sub.Name())
			}
		}
		for _, want := range []string{"path", "convert", "gc"} {
			if !found[want] {
				t.Errorf("worktree subcommand %q is missing", want)
			}
		}
	}
}
