package cli

// completion.go — shell completion for the session argument that nearly every
// command takes.
//
// Until now every session-taking command wanted a literal `claude-pane-df80e6-
// 3e1d6e81`: the one form nobody remembers and everybody pastes. The local
// index already knows each session's title, branch and worktree, and
// client.ResolveSessions already ranks them, so completion is a thin consumer
// of that — the SAME resolver the commands themselves use, which is what keeps
// "what TAB offered" and "what the command accepts" from drifting apart.
//
// Two properties are load-bearing, both inherited deliberately from
// ResolveSessions: it is OFFLINE (no apiserver round-trip on every TAB) and it
// returns an empty slice rather than an error when nothing matches. Completion
// must never block, prompt, or print a diagnostic — a shell that gets stderr
// noise or a hung process on TAB is worse than one that offers nothing.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// completionTimeout bounds a single completion round. Resolution is local file
// I/O so this should never fire; it exists so a wedged network home directory
// cannot hang the user's shell on TAB.
const completionTimeout = 2 * time.Second

// completeSessionArg is the ValidArgsFunction for commands whose first argument
// is a session. It offers "id\tdescription" pairs — zsh and fish render the
// description column, bash ignores it — ranked by the resolver, so the most
// recently active plausible session is first.
//
// It completes only the FIRST positional argument: `sandbox rename <id> <name>`
// must not offer session ids where the new name goes.
func completeSessionArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeSessions(cmd, toComplete)
}

// completeSessions is the shared body: resolve, then format. Every failure mode
// returns "no suggestions" rather than an error — see the package note.
func completeSessions(cmd *cobra.Command, toComplete string) ([]string, cobra.ShellCompDirective) {
	c, err := newClient()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()

	// The empty query is intentional: ResolveSessions ranks by match kind, but
	// the SHELL is already filtering by prefix, and offering every session lets
	// zsh's own matcher work on titles the way the user expects. Passing
	// toComplete instead would double-filter and hide sessions whose id does not
	// start with what was typed.
	matches, err := c.ResolveSessions(ctx, "")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		id := string(m.ID)
		if toComplete != "" && !strings.HasPrefix(id, toComplete) {
			continue
		}
		if d := completionDesc(m.Title, m.Branch); d != "" {
			out = append(out, id+"\t"+d)
			continue
		}
		out = append(out, id)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completionDesc builds the description column: the title, and the branch when
// it adds something the title does not already say.
func completionDesc(title, branch string) string {
	title, branch = strings.TrimSpace(title), strings.TrimSpace(branch)
	switch {
	case title == "" && branch == "":
		return ""
	case title == "":
		return branch
	case branch == "" || strings.Contains(title, branch):
		return title
	default:
		return title + " — " + branch
	}
}

// newCompletionCmd emits the shell completion script. Cobra generates it; this
// is only the wiring, kept explicit (rather than relying on the hidden default
// `completion` command) so `--help` advertises it and the install line for each
// shell is documented in one place.
func newCompletionCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "completion <bash|zsh|fish|powershell>",
		Short: "Generate a shell completion script",
		Long: "Generate a shell completion script for sandbox.\n\n" +
			"Session-taking commands (attach, destroy, rename, suspend, resume, cancel,\n" +
			"sync, worktree path/convert) then complete session ids from the local index,\n" +
			"annotated with each session's title and branch. Completion is offline: it\n" +
			"never contacts the cluster, so TAB stays instant and works on a plane.\n\n" +
			"zsh:\n" +
			"  sandbox completion zsh > \"${fpath[1]}/_sandbox\"\n\n" +
			"bash:\n" +
			"  sandbox completion bash > /etc/bash_completion.d/sandbox\n\n" +
			"fish:\n" +
			"  sandbox completion fish > ~/.config/fish/completions/sandbox.fish\n\n" +
			"Nix/home-manager users: run the generator at build time and install its\n" +
			"output, rather than shipping a snapshot that can drift from the binary.",
		// OnlyValidArgs alongside ExactArgs is load-bearing: ValidArgs alone only
		// feeds completion, it does not VALIDATE. Without it an unknown shell
		// falls through the switch below and silently gets powershell.
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(out, true)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(out)
			default:
				// Unreachable while Args validates, kept so a future edit to either
				// list cannot reintroduce the silent fallthrough.
				return fmt.Errorf("unsupported shell %q", args[0])
			}
		},
	}
}
