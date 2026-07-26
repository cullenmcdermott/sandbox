package cli

// worktree_convert.go — `sandbox worktree convert`, the headless twin of the
// dashboard's `b` modal.
//
// Converting is how a session's work stops being an implementation detail: the
// auto-branch `sandbox/<id>` is renamed to something a human chose, and any
// uncommitted work is captured under a message they wrote. Until now that lived
// only in the TUI, so nothing about finishing a session's work was scriptable —
// you could not convert from a Makefile, over ssh, or from a shell alias.
//
// The git half is already public and already deterministic
// (client.Session.ConvertToBranch: validate the name, refuse a taken one BEFORE
// committing, commit dirty state, rename). This file adds only argument
// resolution and the error vocabulary — it must never generate a branch name or
// a commit message, because ConvertToBranch's contract is that both arrive
// already human-approved.

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/client"
)

// worktreeConverter is the subset of the SDK this command drives, as an
// interface so the flag/error mapping is testable without a git repo.
type worktreeConverter interface {
	ResolveSessions(ctx context.Context, query string) ([]client.SessionMatch, error)
	ConvertToBranch(ctx context.Context, id client.ID, opt client.ConvertOptions) (client.BranchResult, error)
}

// clientConverter adapts *client.Client to worktreeConverter (the SDK exposes
// convert on a Session handle; this flattens it to an id-taking call).
type clientConverter struct{ c *client.Client }

func (a clientConverter) ResolveSessions(ctx context.Context, query string) ([]client.SessionMatch, error) {
	return a.c.ResolveSessions(ctx, query)
}

func (a clientConverter) ConvertToBranch(ctx context.Context, id client.ID, opt client.ConvertOptions) (client.BranchResult, error) {
	return a.c.Open(id).ConvertToBranch(ctx, opt)
}

func newWorktreeConvertCmd() *cobra.Command {
	var branch, message string
	cmd := &cobra.Command{
		Use:   "convert [query] --branch <name>",
		Short: "Rename a session's auto-branch to a human name (committing any WIP)",
		Long: "Convert a session's automatic branch (sandbox/<id>) to a name you choose,\n" +
			"committing any uncommitted work first.\n\n" +
			"This is the scriptable form of the dashboard's `b` modal. Both --branch and\n" +
			"--message are taken verbatim: this command never invents a branch name or a\n" +
			"commit message.\n\n" +
			"After converting, the branch is still checked out BY THE SESSION'S WORKTREE,\n" +
			"so `git checkout <branch>` in the main repo fails. That is expected and not a\n" +
			"problem: `git diff`, `git log`, `git merge` and `git push` all work against\n" +
			"the branch from the main repo without checking it out.",
		Example: "  sandbox worktree convert auth-refactor --branch feat/auth --message \"wip: auth\"\n" +
			"  sandbox worktree convert --branch fix/panic    # resolve the session interactively",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeSessionArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			var query string
			if len(args) == 1 {
				query = args[0]
			}
			return runWorktreeConvert(cmd.Context(), clientConverter{c: c}, cmd.OutOrStdout(), query, branch, message, ttyPicker)
		},
	}
	cmd.Flags().StringVarP(&branch, "branch", "b", "", "target branch name (required)")
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message, required when the worktree is dirty")
	_ = cmd.MarkFlagRequired("branch")
	return cmd
}

// runWorktreeConvert resolves the session, converts, and reports. Split from
// the cobra wiring so the resolution and error-mapping branches are testable.
func runWorktreeConvert(ctx context.Context, cv worktreeConverter, out io.Writer, query, branch, message string, choose chooseFunc) error {
	match, err := resolveOneSession(ctx, cv, query, choose)
	if err != nil {
		return err
	}
	if match.WorktreePath == "" {
		return noWorktreeError(match)
	}

	res, cerr := cv.ConvertToBranch(ctx, match.ID, client.ConvertOptions{BranchName: branch, Message: message})
	if cerr != nil {
		return convertError(cerr, match, branch)
	}

	if res.Committed {
		fmt.Fprintf(out, "Committed %s and renamed the branch to %s.\n", shortSHA(res.CommitSHA), res.Branch)
	} else {
		fmt.Fprintf(out, "Renamed the branch to %s (worktree was clean, nothing to commit).\n", res.Branch)
	}
	// The checkout gotcha, said at the moment it becomes relevant: this is
	// exactly when someone tries `git checkout <branch>` in the main repo and
	// reads git's "already used by worktree" as a failure.
	fmt.Fprintf(out, "\nThe session's worktree still holds %s, so `git checkout %s` in the main\n"+
		"repo will refuse. Merge it without checking it out:\n\n"+
		"  git -C %s merge %s\n",
		res.Branch, res.Branch, orDash(match.ProjectPath), res.Branch)
	return nil
}

// resolveOneSession narrows a query to a single session, prompting when the
// query is genuinely ambiguous. Shared by convert and any future session-taking
// worktree verb; the 0/N rules match `worktree path` exactly, so the two
// commands cannot disagree about what a query means.
func resolveOneSession(ctx context.Context, r sessionResolver, query string, choose chooseFunc) (client.SessionMatch, error) {
	matches, err := r.ResolveSessions(ctx, query)
	if err != nil {
		return client.SessionMatch{}, err
	}
	if len(matches) == 0 {
		return client.SessionMatch{}, noMatchError(query)
	}
	if tied := tiedMatches(matches); len(tied) > 1 {
		if choose == nil {
			return client.SessionMatch{}, ambiguousError(query, tied)
		}
		picked, perr := choose(tied)
		if errors.Is(perr, errNoTTY) {
			return client.SessionMatch{}, ambiguousError(query, tied)
		}
		if perr != nil {
			return client.SessionMatch{}, perr
		}
		return picked, nil
	}
	return matches[0], nil
}

// convertError maps the SDK's convert sentinels onto messages that say what to
// do next. The sentinels exist precisely so a front-end can do this rather than
// echoing a git error the user did not run.
func convertError(err error, match client.SessionMatch, branch string) error {
	switch {
	case errors.Is(err, client.ErrInvalidBranchName):
		return fmt.Errorf("%q is not a valid git branch name — no spaces, no leading dash, no \"..\" (git check-ref-format decides)", branch)
	case errors.Is(err, client.ErrBranchNameTaken):
		return fmt.Errorf("branch %q already exists — pick another name, or delete it first "+
			"(this command never force-renames onto an existing branch)", branch)
	case errors.Is(err, client.ErrWorktreeDirty):
		return fmt.Errorf("session %s has uncommitted changes — pass -m/--message to capture them in a commit", match.ID)
	case errors.Is(err, client.ErrNoWorktree):
		return noWorktreeError(match)
	default:
		return err
	}
}

// orDash renders an empty path as a placeholder so a copy-pasteable command
// never contains an empty argument.
func orDash(s string) string {
	if s == "" {
		return "<repo>"
	}
	return s
}
