package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

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

// newWorktreeCmd is the parent of the per-session worktree maintenance verbs
// (design docs/archive/worktree-lifecycle-design.md §4.8). Today it hosts `gc`.
func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage per-session git worktrees",
		Long: "Per-session git worktrees isolate each session's edits on their own\n" +
			"branch (sandbox/<id>) so two agents on one repo never cross-feed. This\n" +
			"command group maintains them (garbage-collect orphans).",
	}
	cmd.AddCommand(newWorktreeGCCmd())
	return cmd
}

// newWorktreeGCCmd reaps orphaned per-session worktrees: those whose session is
// no longer live in the cluster. Clean worktrees are removed; dirty ones are
// never deleted outright — their work is committed to the session branch first
// (I2, never-lost), and the reap report names the branch so the user can find
// it. Stale git admin entries are pruned. It exits 0 even when everything is
// skipped.
func newWorktreeGCCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage-collect orphaned per-session worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// ReapWorktrees cross-references the live cluster (backend.List), so a
			// full client is required — an orphan is defined relative to the live set.
			c, err := newClient()
			if err != nil {
				return fmt.Errorf("worktree gc needs cluster access to confirm live sessions: %w", err)
			}
			return runWorktreeGC(cmd.Context(), c, cmd.OutOrStdout(), dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "classify and report worktrees without mutating anything")
	return cmd
}

// runWorktreeGC drives ReapWorktrees and prints one line per reported directory
// (session id, action, branch, commit SHA when captured) plus a summary count.
// Split from the cobra wiring so it is unit-testable with a fake reaper.
func runWorktreeGC(ctx context.Context, r worktreeReaper, out io.Writer, dryRun bool) error {
	reaped, err := r.ReapWorktrees(ctx, client.ReapOptions{DryRun: dryRun})
	if err != nil {
		return fmt.Errorf("worktree gc: %w", err)
	}
	if len(reaped) == 0 {
		fmt.Fprintln(out, "worktree gc: no worktrees found.")
		return nil
	}
	acted := 0
	for _, w := range reaped {
		if w.Action != "skipped" {
			acted++
		}
		line := fmt.Sprintf("  %-28s %-24s %s", w.SessionID, w.Action, w.Branch)
		if w.CommitSHA != "" {
			line += " " + shortSHA(w.CommitSHA)
		}
		fmt.Fprintln(out, line)
	}
	verb := "reaped"
	if dryRun {
		verb = "would reap"
	}
	fmt.Fprintf(out, "worktree gc: %s %d of %d worktree(s)", verb, acted, len(reaped))
	if dryRun {
		fmt.Fprint(out, " (dry-run — re-run without --dry-run to apply)")
	}
	fmt.Fprintln(out, ".")
	return nil
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
