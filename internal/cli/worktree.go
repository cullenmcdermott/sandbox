package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// worktreeReaper is the subset of *client.Client the `worktree gc` command
// needs. Declaring it as an interface keeps the reporting/printing logic
// (runWorktreeGC) unit-testable with a fake — *client.Client satisfies it.
type worktreeReaper interface {
	ReapWorktrees(ctx context.Context, opt client.ReapOptions) ([]client.ReapedWorktree, error)
}

// newWorktreeCmd is the parent of the per-session worktree verbs (design
// docs/archive/worktree-lifecycle-design.md §4.8): finding one (`path`),
// finishing with one (`convert`), and cleaning up (`gc`).
func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage per-session git worktrees",
		Long: "Per-session git worktrees isolate each session's edits on their own\n" +
			"branch (sandbox/<id>) so two agents on one repo never cross-feed.\n\n" +
			"  path     print a session's worktree dir, for `cd $(sandbox worktree path …)`\n" +
			"  convert  rename a session's branch to a human name (the headless `b` modal)\n" +
			"  gc       garbage-collect worktrees whose session is gone",
	}
	cmd.AddCommand(newWorktreePathCmd())
	cmd.AddCommand(newWorktreeConvertCmd())
	cmd.AddCommand(newWorktreeGCCmd())
	return cmd
}

// defaultGCMinAge is how long a worktree is kept after its session is gone.
//
// The policy this encodes: a worktree OUTLIVES its agent session. The session
// is where an agent worked; the checkout is a laptop artifact you may still
// want to open, diff or resume from days later. A week is long enough that
// "I'll get back to it tomorrow" survives a gc, and short enough that the
// directory does not accumulate forever.
const defaultGCMinAge = 7 * 24 * time.Hour

// newWorktreeGCCmd reaps orphaned per-session worktrees: those whose session is
// gone AND that retention says are safe to remove (old enough, and holding no
// unmerged commits). Clean worktrees are removed; dirty ones are never deleted
// outright — their work is committed to the session branch first (I2,
// never-lost), and the reap report names the branch so the user can find it.
// Stale git admin entries are pruned. It exits 0 even when everything is
// skipped.
//
// It classifies first, shows exactly what it is about to delete, and asks —
// deletion of someone's checkout should never be the silent consequence of a
// maintenance command.
func newWorktreeGCCmd() *cobra.Command {
	var (
		dryRun       bool
		assumeYes    bool
		minAge       time.Duration
		reapUnlanded bool
		baseBranch   string
	)
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage-collect old per-session worktrees whose session is gone",
		Long: "Remove per-session worktrees that are no longer needed.\n\n" +
			"A worktree is only eligible when ALL of these hold:\n" +
			"  - its session is no longer live in the cluster\n" +
			"  - it belongs to the current namespace (proven by the local index)\n" +
			"  - it has not been touched within --min-age\n" +
			"  - its branch has no commits missing from the base branch\n\n" +
			"The branch is ALWAYS preserved — gc removes checkouts, never work. Dirty\n" +
			"worktrees are committed to their branch before removal.\n\n" +
			"Eligible worktrees are listed and confirmed before anything is deleted;\n" +
			"pass --yes to skip the prompt in scripts.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// ReapWorktrees cross-references the live cluster (backend.List), so a
			// full client is required — an orphan is defined relative to the live set.
			c, err := newClient()
			if err != nil {
				return fmt.Errorf("worktree gc needs cluster access to confirm live sessions: %w", err)
			}
			opt := client.ReapOptions{
				MinAge:       minAge,
				ReapUnlanded: reapUnlanded,
				BaseBranch:   baseBranch,
			}
			return runWorktreeGC(cmd.Context(), c, cmd.OutOrStdout(), opt, dryRun, assumeYes, confirmTTY)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "classify and report worktrees without mutating anything")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt (for scripts)")
	cmd.Flags().DurationVar(&minAge, "min-age", defaultGCMinAge, "retain worktrees touched more recently than this (0 disables the age gate)")
	cmd.Flags().BoolVar(&reapUnlanded, "reap-unlanded", false, "also reap worktrees whose branch has commits not on the base branch")
	cmd.Flags().StringVar(&baseBranch, "base", "", "branch that \"landed\" is measured against (default: origin's default, else main/master)")
	return cmd
}

// confirmFunc asks the user to approve a destructive action. Production reads
// /dev/tty; tests inject a canned answer.
type confirmFunc func(prompt string) (bool, error)

// runWorktreeGC classifies, reports, confirms, then acts. Split from the cobra
// wiring so every branch is unit-testable with a fake reaper and a stub prompt.
//
// The two-phase shape (dry-run to classify, then a real run) is what lets the
// confirmation name actual victims. The second pass re-classifies rather than
// deleting a remembered list, so a session that came back to life between the
// two passes is still protected by the live gate.
func runWorktreeGC(ctx context.Context, r worktreeReaper, out io.Writer, opt client.ReapOptions, dryRun, assumeYes bool, confirm confirmFunc) error {
	plan := opt
	plan.DryRun = true
	reaped, err := r.ReapWorktrees(ctx, plan)
	if err != nil {
		return fmt.Errorf("worktree gc: %w", err)
	}
	if len(reaped) == 0 {
		fmt.Fprintln(out, "worktree gc: no worktrees found.")
		return nil
	}

	victims := 0
	for _, w := range reaped {
		if w.Action != "skipped" {
			victims++
		}
	}
	printReapReport(out, reaped)

	if victims == 0 {
		fmt.Fprintf(out, "worktree gc: nothing to reap (%d worktree(s) retained).\n", len(reaped))
		return nil
	}
	if dryRun {
		fmt.Fprintf(out, "worktree gc: would reap %d of %d worktree(s) (dry-run — re-run without --dry-run to apply).\n", victims, len(reaped))
		return nil
	}

	if !assumeYes {
		ok, cerr := confirm(fmt.Sprintf("Remove %d worktree(s)? Branches are preserved. [y/N] ", victims))
		if cerr != nil {
			return fmt.Errorf("worktree gc: %w (use --yes to skip the prompt)", cerr)
		}
		if !ok {
			fmt.Fprintln(out, "worktree gc: cancelled, nothing removed.")
			return nil
		}
	}

	final := opt
	final.DryRun = false
	done, derr := r.ReapWorktrees(ctx, final)
	if derr != nil {
		return fmt.Errorf("worktree gc: %w", derr)
	}
	acted := 0
	for _, w := range done {
		if w.Action != "skipped" {
			acted++
		}
	}
	fmt.Fprintf(out, "worktree gc: reaped %d of %d worktree(s).\n", acted, len(done))
	return nil
}

// printReapReport prints one line per enumerated worktree: what will happen to
// it and, for anything retained, why. The reason column is the point — an
// undifferentiated list of "skipped" tells the user nothing about whether gc is
// working or silently refusing.
func printReapReport(out io.Writer, reaped []client.ReapedWorktree) {
	for _, w := range reaped {
		line := fmt.Sprintf("  %-28s %-24s %s", w.SessionID, w.Action, w.Branch)
		if w.CommitSHA != "" {
			line += " " + shortSHA(w.CommitSHA)
		}
		if w.Reason != "" {
			line += "  — " + w.Reason
		}
		fmt.Fprintln(out, line)
	}
}

// confirmTTY reads a yes/no answer from the controlling terminal. It uses
// /dev/tty rather than stdin so a piped stdin cannot accidentally answer "yes",
// and so the absence of a terminal is a clean error pointing at --yes.
func confirmTTY(prompt string) (bool, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, errors.New("no terminal available to confirm")
	}
	defer tty.Close()
	fmt.Fprint(tty, prompt)
	line, rerr := bufio.NewReader(tty).ReadString('\n')
	if rerr != nil && line == "" {
		return false, nil // EOF / ^D reads as "no"
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// shortSHA trims a full commit SHA to the conventional 7-char short form.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// worktreeOps adapts the public client SDK's per-session worktree git surface to
// the dashboard's WorktreeOps seam, mapping the client sentinel errors onto the
// dashboard's package-local sentinels so the dashboard can branch on the failure
// kind without importing client.
type worktreeOps struct{ c *client.Client }

// newWorktreeOps builds the dashboard's convert-to-branch backend on top of the
// client SDK. WorktreeStatus / ConvertToBranch are local git ops (index read +
// git in the worktree), so they need no live pod connection.
func newWorktreeOps(c *client.Client) dashboard.WorktreeOps { return worktreeOps{c: c} }

func (w worktreeOps) Status(ctx context.Context, id session.ID) (branch string, dirty bool, changed []string, err error) {
	st, e := w.c.Open(id).WorktreeStatus(ctx)
	if e != nil {
		return "", false, nil, mapWorktreeErr(e)
	}
	return st.Branch, st.Dirty, st.Changed, nil
}

func (w worktreeOps) Convert(ctx context.Context, id session.ID, branchName, message string) (finalBranch string, committed bool, err error) {
	res, e := w.c.Open(id).ConvertToBranch(ctx, client.ConvertOptions{BranchName: branchName, Message: message})
	if e != nil {
		return "", false, mapWorktreeErr(e)
	}
	return res.Branch, res.Committed, nil
}

// mapWorktreeErr translates the client SDK's worktree sentinels into the
// dashboard's, preserving the underlying error's message via %w wrapping.
func mapWorktreeErr(err error) error {
	switch {
	case errors.Is(err, client.ErrNoWorktree):
		return fmt.Errorf("%w: %v", dashboard.ErrNoWorktree, err)
	case errors.Is(err, client.ErrBranchNameTaken):
		return fmt.Errorf("%w: %v", dashboard.ErrBranchNameTaken, err)
	case errors.Is(err, client.ErrWorktreeDirty):
		return fmt.Errorf("%w: %v", dashboard.ErrWorktreeDirty, err)
	default:
		return err
	}
}
