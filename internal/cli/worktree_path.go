package cli

// worktree_path.go — `sandbox worktree path [query]`, the command that answers
// "where is that session's checkout?" in a form a shell can consume:
//
//	cd $(sandbox worktree path pick-small)
//
// That one-liner dictates the whole design. Command substitution captures
// STDOUT, so stdout carries the resolved path and NOTHING else — no banner, no
// prompt, no escape sequence. When the query is ambiguous the picker therefore
// draws on STDERR and reads keys from /dev/tty, both of which are still the
// terminal inside `$(...)`. Get this backwards and the command either pastes
// ANSI into your `cd` argument or draws a picker nobody can see.
//
// Resolution itself is client.ResolveSessions — offline, index-only, shared with
// shell completion. This command adds no matching rules of its own.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/tui/picker"
)

// sessionResolver is the subset of *client.Client this command needs. An
// interface so the 0/1/N branches are unit-testable without a cluster or a
// local index — *client.Client satisfies it.
type sessionResolver interface {
	ResolveSessions(ctx context.Context, query string) ([]client.SessionMatch, error)
}

// chooseFunc picks one match from several. Production supplies the /dev/tty
// picker; tests supply a deterministic stub. It returns ErrNoTTY when it cannot
// prompt, which is what turns an ambiguous query into a listing error.
type chooseFunc func(matches []client.SessionMatch) (client.SessionMatch, error)

// errNoTTY reports that disambiguation needed a terminal and there wasn't one.
var errNoTTY = errors.New("no terminal available for an interactive choice")

// errPickCancelled reports that the user dismissed the picker (esc). It exits
// non-zero without an error message: cancelling is a decision, not a failure,
// and `cd $(...)` must not receive a path.
var errPickCancelled = errors.New("cancelled")

func newWorktreePathCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "path [query]",
		Short: "Print a session's worktree directory (for `cd $(sandbox worktree path <query>)`)",
		Long: "Print the local git worktree directory of the session matching <query>.\n\n" +
			"The query is resolved the same way everywhere in the CLI: a session id or\n" +
			"id prefix, a branch name, a path inside the worktree or project, or a\n" +
			"substring of the session's title. With no query, every known session is\n" +
			"offered. When several sessions match and a terminal is available, a picker\n" +
			"is shown ON STDERR so that only the chosen path reaches stdout.",
		Example: "  cd $(sandbox worktree path pick-small)   # jump to a session's checkout\n" +
			"  sandbox worktree path                    # pick from all sessions\n" +
			"  sandbox worktree path auth --json        # structured, never interactive",
		Args: cobra.MaximumNArgs(1),
		// Completion over sessions: the same resolver the command itself uses.
		ValidArgsFunction: completeSessionArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolution is offline (local index only), so this command works with
			// no cluster reachable — deliberately: `cd` to a worktree must not
			// depend on the VPN being up.
			c, err := newClient()
			if err != nil {
				return err
			}
			var query string
			if len(args) == 1 {
				query = args[0]
			}
			return runWorktreePath(cmd.Context(), c, cmd.OutOrStdout(), query, asJSON, ttyPicker)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the matching sessions as JSON (never interactive)")
	return cmd
}

// worktreePathJSON is the --json record: one object per matching session. The
// shape is an ARRAY even for a single match, so a script never has to branch on
// how many matched.
type worktreePathJSON struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Backend      string    `json:"backend"`
	WorktreePath string    `json:"worktreePath"`
	ProjectPath  string    `json:"projectPath"`
	Branch       string    `json:"branch"`
	LastActivity time.Time `json:"lastActivity"`
	MatchedBy    string    `json:"matchedBy"`
}

// runWorktreePath is the command body, split from the cobra wiring so the
// 0/1/N/no-tty branches are testable with a fake resolver and a stub chooser.
func runWorktreePath(ctx context.Context, r sessionResolver, out io.Writer, query string, asJSON bool, choose chooseFunc) error {
	matches, err := r.ResolveSessions(ctx, query)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return noMatchError(query)
	}

	// --json is structured and never interactive: it reports every candidate and
	// lets the caller decide, rather than prompting inside a pipeline.
	if asJSON {
		recs := make([]worktreePathJSON, 0, len(matches))
		for _, m := range matches {
			recs = append(recs, worktreePathJSON{
				ID:           string(m.ID),
				Title:        m.Title,
				Backend:      m.Backend,
				WorktreePath: m.WorktreePath,
				ProjectPath:  m.ProjectPath,
				Branch:       m.Branch,
				LastActivity: m.LastActivity,
				MatchedBy:    string(m.MatchedBy),
			})
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(recs)
	}

	match := matches[0]
	if len(matches) > 1 {
		// Ambiguity is only real within a match kind: a query that lands one
		// id-prefix hit plus three weaker project-path hits is not ambiguous, and
		// prompting about it would be noise. Same rule as client.ResolveSession.
		if tied := tiedMatches(matches); len(tied) > 1 {
			if choose == nil {
				return ambiguousError(query, tied)
			}
			picked, perr := choose(tied)
			if errors.Is(perr, errNoTTY) {
				return ambiguousError(query, tied)
			}
			if perr != nil {
				return perr
			}
			match = picked
		}
	}

	if match.WorktreePath == "" {
		return noWorktreeError(match)
	}
	// The path, alone, on stdout — the entire contract of this command.
	fmt.Fprintln(out, match.WorktreePath)
	return nil
}

// tiedMatches returns the leading run of candidates sharing the strongest match
// kind (the slice is already sorted most-specific-first).
func tiedMatches(matches []client.SessionMatch) []client.SessionMatch {
	best := matches[0].MatchedBy
	n := 1
	for _, m := range matches[1:] {
		if m.MatchedBy != best {
			break
		}
		n++
	}
	return matches[:n]
}

// noMatchError explains an empty result, including the offline caveat — the
// most likely cause of a surprising "no match" is a session created on another
// machine, which this index cannot see.
func noMatchError(query string) error {
	if query == "" {
		return fmt.Errorf("no sessions found on this machine — run `sandbox status` to list them")
	}
	return fmt.Errorf("no session matches %q — run `sandbox status` to list sessions "+
		"(resolution is offline, so a session created on another machine is invisible here)", query)
}

// ambiguousError is the non-interactive answer to ambiguity: name the
// candidates so the next invocation can be exact, rather than guessing.
func ambiguousError(query string, matches []client.SessionMatch) error {
	var b strings.Builder
	if query == "" {
		fmt.Fprintf(&b, "%d sessions to choose from and no terminal to choose with", len(matches))
	} else {
		fmt.Fprintf(&b, "%q matches %d sessions and there is no terminal to choose with", query, len(matches))
	}
	b.WriteString(" — re-run with an exact id:\n")
	for _, m := range matches {
		fmt.Fprintf(&b, "  %-28s %s\n", m.ID, matchSummary(m))
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

// noWorktreeError explains a session that resolved fine but has no worktree.
// It deliberately does NOT fall back to the project path: `cd $(...)` into the
// main repo when you asked for a session's isolated checkout is a silent lie
// that could get work committed to the wrong tree.
func noWorktreeError(m client.SessionMatch) error {
	msg := fmt.Sprintf("session %s (%s) has no per-session worktree", m.ID, m.Title)
	if m.ProjectPath != "" {
		msg += fmt.Sprintf(" — it works directly in %s", m.ProjectPath)
	}
	return errors.New(msg + " (created with --worktree=off, or its project is not a git repo)")
}

// matchSummary is the one-line description of a candidate used by both the
// picker rows and the no-tty listing: branch when there is one, else the
// project, plus how it matched.
func matchSummary(m client.SessionMatch) string {
	detail := m.Branch
	if detail == "" {
		detail = m.ProjectPath
	}
	if detail == "" {
		return string(m.MatchedBy)
	}
	return fmt.Sprintf("%s  (%s)", detail, m.MatchedBy)
}

// ttyPicker disambiguates interactively. It draws on STDERR and reads from
// /dev/tty so that stdout stays clean for command substitution — the reason
// this command cannot just reuse the dashboard's picker plumbing.
func ttyPicker(matches []client.SessionMatch) (client.SessionMatch, error) {
	// /dev/tty is the controlling terminal regardless of how stdin was
	// redirected; opening it is also the test for "is anyone there?".
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return client.SessionMatch{}, errNoTTY
	}
	defer tty.Close()

	items := make([]picker.Item, 0, len(matches))
	for _, m := range matches {
		name := m.Title
		if name == "" {
			name = string(m.ID)
		}
		items = append(items, picker.Item{
			ID:   string(m.ID),
			Name: name,
			Desc: matchSummary(m),
		})
	}

	model := &pathPicker{matches: matches}
	// WithFilter because this list is every session on the machine — long enough
	// that typing to narrow beats paging through it.
	model.p = picker.New("Which session?", items,
		picker.WithFilter(),
		picker.WithChoose(func(it picker.Item) { model.chosen = it.ID; model.done = true }),
		picker.WithCancel(func() { model.cancelled = true; model.done = true }),
	)

	// Output to stderr, input from the tty: the two halves of the TTY rule.
	prog := tea.NewProgram(model, tea.WithInput(tty), tea.WithOutput(os.Stderr))
	if _, rerr := prog.Run(); rerr != nil {
		return client.SessionMatch{}, rerr
	}
	if model.cancelled {
		return client.SessionMatch{}, errPickCancelled
	}
	for _, m := range matches {
		if string(m.ID) == model.chosen {
			return m, nil
		}
	}
	return client.SessionMatch{}, errPickCancelled
}

// pathPicker is the minimal tea.Model hosting the public picker for the
// one-shot disambiguation prompt: no alt-screen (the picker should scroll away
// with the rest of the shell's output), no mouse, quit as soon as a row is
// chosen or the prompt is dismissed.
type pathPicker struct {
	p         *picker.Model
	matches   []client.SessionMatch
	chosen    string
	cancelled bool
	done      bool
	width     int
}

func (m *pathPicker) Init() tea.Cmd { return nil }

func (m *pathPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyPressMsg:
		// ctrl+c is a hard cancel; everything else belongs to the picker (whose
		// own esc handling clears a filter before it dismisses).
		if msg.String() == "ctrl+c" {
			m.cancelled, m.done = true, true
			return m, tea.Quit
		}
		m.p, _ = m.p.Update(msg)
		if m.done {
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}

func (m *pathPicker) View() tea.View {
	w := m.width
	if w <= 0 {
		w = 80
	}
	// Once a choice is made the frame is blanked, so the picker does not linger
	// above the shell prompt after the command returns.
	if m.done {
		return tea.NewView("")
	}
	return tea.NewView(m.p.View(w))
}
