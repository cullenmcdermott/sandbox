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
	"path/filepath"
	"strconv"
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
			// depend on the VPN being up. newOfflineClient (not newClient) is what
			// makes that true: the latter resolves a kubeconfig at construction and
			// fails without one, long before the index is read.
			c, err := newOfflineClient()
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

// matchSummary is the one-line description of a candidate in the no-tty
// listing: branch when there is one, else the project, plus how it matched.
// (The picker rows use pickerCols instead — that surface has room to align
// fields into columns, and this one already prints the id in its own column.)
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

// pickerCols is the aligned detail for one picker row: repo, short id, age, and
// (only when a query produced something other than "any") how it matched.
//
// These are COLUMNS rather than a prose summary because the rows they describe
// are near-identical: on a working machine most candidates share a project and
// many have no title but their own id. Scanning twenty such rows means comparing
// the same field across them, which a ragged line makes you re-find on every row
// and an aligned stripe hands you for free. The match kind is dropped for
// MatchAny — with an empty query every row carries it, so it separates nothing
// and costs a whole column.
func pickerCols(m client.SessionMatch, now time.Time) []string {
	repo := filepath.Base(m.ProjectPath)
	if m.ProjectPath == "" {
		repo = "—"
	}
	cols := []string{repo, shortSessionID(string(m.ID)), shortAge(m.LastActivity, now)}
	if m.MatchedBy != "" && m.MatchedBy != client.MatchAny {
		cols = append(cols, string(m.MatchedBy))
	}
	return cols
}

// shortSessionID is the trailing random suffix of a session id
// ("claude-pane-df80e6-3bf602fc" → "3bf602fc"), the part that actually varies
// between two sessions of one project. Ids without a "-" are returned whole.
func shortSessionID(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// shortAge renders how long ago t was in one compact token ("4m", "2h", "3d").
// A zero or future t yields "now" rather than a negative or absurd age — the
// index is written by this machine, but a clock change must not produce "-1h".
func shortAge(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case t.IsZero() || d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
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

	now := time.Now()
	items := make([]picker.Item, 0, len(matches))
	for _, m := range matches {
		name := m.Title
		if name == "" {
			name = string(m.ID)
		}
		items = append(items, picker.Item{
			ID:   string(m.ID),
			Name: name,
			Cols: pickerCols(m, now),
		})
	}

	model := &pathPicker{matches: matches}
	// WithFilter because this list is every session on the machine — long enough
	// that typing to narrow beats paging through it.
	model.p = picker.New("Which session?", items,
		picker.WithFilter(),
		// Columns need room: the default 60-column cap squeezes a session title
		// into an ellipsis long before the terminal runs out of width. The real
		// width arrives with the first WindowSizeMsg; this is the pre-resize
		// floor.
		picker.WithMaxWidth(pickerWidthCap(0)),
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

// pickerChrome is the number of terminal lines the picker spends on everything
// that is not a row: two border lines, the title, the blank after it, the filter
// query line and ITS blank, the blank before the hints, the hints themselves,
// and the two "N more" markers a scrolled window can add. Under-counting is what
// scrolls the box's own top off the screen, so this rounds up.
const pickerChrome = 11

// pickerRowBudget converts a terminal height into a row cap for the picker. It
// never returns less than 3 — on a very short terminal an overflowing box is
// still better than one with no rows in it — and 0 (unbounded) when the height
// is unknown, which is what a non-resizing host reports.
func pickerRowBudget(height int) int {
	if height <= 0 {
		return 0
	}
	if n := height - pickerChrome; n > 3 {
		return n
	}
	return 3
}

// Width bounds for the session picker. It carries four columns, so it wants far
// more than the picker's 60-column default — but not the whole of a very wide
// terminal, where a row's eye has to travel the full screen from title to age.
const (
	pickerMinWidth = 76
	pickerMaxWidth = 110
)

// pickerWidthCap converts a terminal width into the overlay's width cap, leaving
// the margin View itself reserves. An unknown width (0, before the first resize)
// yields the floor rather than the picker's much narrower default.
func pickerWidthCap(termWidth int) int {
	if termWidth <= 0 {
		return pickerMinWidth
	}
	return min(pickerMaxWidth, max(pickerMinWidth, termWidth-8))
}

func (m *pathPicker) Init() tea.Cmd { return nil }

func (m *pathPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.p.SetMaxRows(pickerRowBudget(msg.Height))
		m.p.SetMaxWidth(pickerWidthCap(msg.Width))
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
