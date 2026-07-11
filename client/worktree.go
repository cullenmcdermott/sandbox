package client

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// worktree.go holds the DETERMINISTIC git side of the per-session worktree
// lifecycle (design docs/worktree-lifecycle-design.md §4.3/§4.7/§4.9): detect a
// git project, add a worktree on an auto-branch at Create, roll it back if the
// cluster create fails, and capture-then-remove it at Destroy. No LLM ever runs
// here (I3) — the LLM only proposes branch names/messages in the TUI (a later
// wave); every `git` invocation is pure and unit-testable against a temp repo.

// gitRunner is the exec seam for git invocations, mirroring how internal/sync
// wraps the mutagen CLL behind an injectable Runner. dir maps to `git -C <dir>`
// (empty runs git in the process cwd). It returns combined stdout+stderr so a
// caller can classify a failure (e.g. "already exists") from the output. The
// production implementation (realGitRunner) shells out; a test may inject a stub
// via Client.gitExec, though the git-ops themselves are exercised against REAL
// temp repos.
type gitRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// realGitRunner shells out to the git binary, prepending `-C <dir>` when dir is
// set. On failure it wraps the error with the trimmed combined output so callers
// (and error messages) carry git's own diagnostic.
func realGitRunner(ctx context.Context, dir string, args ...string) ([]byte, error) {
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	out, err := exec.CommandContext(ctx, "git", full...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// git returns the client's git exec seam, defaulting to the real binary.
func (c *Client) git() gitRunner {
	if c.gitExec != nil {
		return c.gitExec
	}
	return realGitRunner
}

// gitAvailable reports whether git can be invoked: true when a test injected a
// stub exec seam, else whether the git binary is on PATH.
func (c *Client) gitAvailable() bool {
	if c.gitExec != nil {
		return true
	}
	_, err := exec.LookPath("git")
	return err == nil
}

// worktreeRollbackTimeout bounds the best-effort worktree cleanup Create runs
// when a later step (cluster create) fails after the worktree was added. It runs
// on a context independent of the caller's (which may already be cancelled by the
// time the failure surfaces), mirroring the k8s backend's createRollbackTimeout.
const worktreeRollbackTimeout = 30 * time.Second

// worktreeInfo describes a freshly created per-session worktree. The zero value
// (empty Path) means no worktree was created (non-git fallback / WorktreeOff).
type worktreeInfo struct {
	Path     string // <worktreesRoot>/<id>: git worktree dir, Mutagen alpha, pod cwd
	Branch   string // sandbox/<id>: the auto-branch that preserves work post-removal
	RepoRoot string // git toplevel of ProjectPath: `git -C RepoRoot worktree ...`
}

// pathExists reports whether a filesystem path is present.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// worktreeMissingForSync reports whether Connect must SKIP file sync because the
// session's worktree dir is gone (design §4.9). True only for a real worktree
// session — workspacePath is a distinct worktree dir, not the repo root — whose
// dir no longer exists (the user deleted it). Syncing into a fresh empty alpha
// under two-way-safe would look like a mass delete and risk storming the pod's
// files away, so the caller skips sync with a warning instead. A non-git session
// (workspacePath == projectPath) is never "missing".
func worktreeMissingForSync(workspacePath, projectPath string) bool {
	return workspacePath != "" && workspacePath != projectPath && !pathExists(workspacePath)
}

// worktreeBranch is the auto-branch a session's worktree is created on. Using a
// real branch (not detached HEAD) satisfies I2 from the first commit: any work is
// reachable as sandbox/<id> even after the worktree dir is removed.
func worktreeBranch(id string) string { return "sandbox/" + id }

// createWorktree performs the Create-time worktree step (design §4.3). Under
// WorktreeAuto it creates a worktree iff ProjectPath is inside a git work tree
// and git is present, else it falls back (returns the zero worktreeInfo, nil).
// Under WorktreeOn a non-git project (or missing git) is ErrNotAGitRepo. The
// caller passes WorktreeOff sessions straight through without calling this.
//
// On success the worktree lives at <worktreesRoot>/<id> on branch sandbox/<id>
// created from the repo's current HEAD. An existing path/branch yields
// ErrWorktreeExists.
func (c *Client) createWorktree(ctx context.Context, mode WorktreeMode, projectPath, id string) (worktreeInfo, error) {
	if !c.gitAvailable() {
		if mode == WorktreeOn {
			return worktreeInfo{}, fmt.Errorf("%w: git binary not found in PATH", ErrNotAGitRepo)
		}
		return worktreeInfo{}, nil // Auto: no git ⇒ fall back to no worktree.
	}
	git := c.git()
	toplevel, err := gitToplevel(ctx, git, projectPath)
	if err != nil {
		if mode == WorktreeOn {
			return worktreeInfo{}, err // ErrNotAGitRepo
		}
		return worktreeInfo{}, nil // Auto: not a git repo ⇒ fall back.
	}
	// A repo whose HEAD is unborn (fresh `git init`, zero commits) cannot anchor
	// `worktree add … HEAD`. Auto falls back to no-worktree (the session still
	// works, just unisolated — same as non-git); On errors up front with the
	// reason rather than letting the raw `worktree add` failure surface.
	if _, herr := git(ctx, toplevel, "rev-parse", "--verify", "HEAD"); herr != nil {
		if mode == WorktreeOn {
			return worktreeInfo{}, fmt.Errorf("%w: %s has no commits (unborn HEAD) — a worktree needs a commit to branch from", ErrNotAGitRepo, toplevel)
		}
		return worktreeInfo{}, nil
	}
	root := c.worktreesRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return worktreeInfo{}, fmt.Errorf("sandbox: create worktree root: %w", err)
	}
	info := worktreeInfo{
		Path:     filepath.Join(root, id),
		Branch:   worktreeBranch(id),
		RepoRoot: toplevel,
	}
	if err := worktreeAdd(ctx, git, info.RepoRoot, info.Branch, info.Path); err != nil {
		return worktreeInfo{}, err
	}
	return info, nil
}

// rollbackWorktree tears down a worktree created earlier in the same Create when
// a later step fails (design §4.3 rollback). Safe and unconditional: the worktree
// was just added from HEAD with zero commits, so removing it and deleting the
// auto-branch loses nothing. Best-effort; errors are swallowed (the create is
// already failing and the residue, if any, is reapable).
func (c *Client) rollbackWorktree(ctx context.Context, info worktreeInfo) {
	if info.RepoRoot == "" || info.Path == "" {
		return
	}
	git := c.git()
	_, _ = git(ctx, info.RepoRoot, "worktree", "remove", "--force", info.Path)
	_ = os.RemoveAll(info.Path) // in case `worktree remove` didn't take
	if info.Branch != "" {
		_, _ = git(ctx, info.RepoRoot, "branch", "-D", info.Branch)
	}
	_, _ = git(ctx, info.RepoRoot, "worktree", "prune")
}

// teardownWorktree runs the Destroy-time capture-then-remove step (design
// §4.7/§4.9): read the session's recorded worktree, WIP-commit any dirty state to
// its branch (never a silent discard — I2), then `git worktree remove` +
// `git worktree prune`. It is best-effort: the cluster is already gone, so a git
// failure must not fail Destroy — every error is swallowed EXCEPT that a dirty
// worktree is never removed when its WIP commit failed (the work stays on disk
// and on branch). A worktree already gone (user deleted the dir) is a no-op
// beyond pruning stale admin entries. The branch is never deleted — it IS the
// preserved work. Must run before RemoveLocalState drops the index entry.
func (c *Client) teardownWorktree(ctx context.Context, id ID) {
	entry, err := c.index.Load(string(id))
	if err != nil || entry.WorktreePath == "" {
		return // no worktree recorded for this session
	}
	git := c.git()
	if _, statErr := os.Stat(entry.WorktreePath); os.IsNotExist(statErr) {
		// Dir already gone: just clear any stale admin entry (design §4.8 case 2).
		if entry.RepoRoot != "" {
			_, _ = git(ctx, entry.RepoRoot, "worktree", "prune")
		}
		return
	}
	// Capture-if-dirty: a silent WIP commit to the session branch is the
	// signed-off default (§4.9 / resolution 5), never a silent discard.
	if porcelain, serr := worktreeStatusPorcelain(ctx, git, entry.WorktreePath); serr == nil && strings.TrimSpace(string(porcelain)) != "" {
		msg := fmt.Sprintf("sandbox: WIP at destroy (%s)", id)
		if cerr := worktreeCommitAll(ctx, git, entry.WorktreePath, msg); cerr != nil {
			// The WIP commit failed — do NOT remove the worktree (would lose the
			// uncommitted work). Leave it for the reaper / manual recovery.
			return
		}
	}
	// No --force: git refuses a still-dirty worktree, a belt-and-suspenders atop
	// the capture step. RepoRoot anchors the git dir since the worktree's own
	// .git pointer may be unreliable.
	_, _ = git(ctx, entry.RepoRoot, "worktree", "remove", entry.WorktreePath)
	if entry.RepoRoot != "" {
		_, _ = git(ctx, entry.RepoRoot, "worktree", "prune")
	}
}

// gitToplevel resolves the git work tree ProjectPath lives in via
// `git -C <projectPath> rev-parse --show-toplevel`. Any failure (not a repo,
// git error) maps to ErrNotAGitRepo — under WorktreeAuto the caller treats that
// as "fall back", under WorktreeOn as a hard error.
func gitToplevel(ctx context.Context, git gitRunner, projectPath string) (string, error) {
	out, err := git(ctx, projectPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotAGitRepo, projectPath)
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return "", fmt.Errorf("%w: %s", ErrNotAGitRepo, projectPath)
	}
	return top, nil
}

// worktreeAdd runs `git -C <toplevel> worktree add -b <branch> <path> HEAD`. An
// existing path or branch (git reports "already exists" / "already used by
// worktree" / "already checked out") maps to ErrWorktreeExists; other failures
// wrap the git error verbatim.
func worktreeAdd(ctx context.Context, git gitRunner, toplevel, branch, path string) error {
	out, err := git(ctx, toplevel, "worktree", "add", "-b", branch, path, "HEAD")
	if err != nil {
		low := strings.ToLower(string(out) + " " + err.Error())
		switch {
		case strings.Contains(low, "already exists"),
			strings.Contains(low, "already used by worktree"),
			strings.Contains(low, "already checked out"):
			return fmt.Errorf("%w: %s (branch %s)", ErrWorktreeExists, path, branch)
		default:
			return fmt.Errorf("sandbox: git worktree add: %w", err)
		}
	}
	return nil
}

// worktreeStatusPorcelain returns `git -C <path> status --porcelain` output; an
// empty (whitespace-only) result means the worktree is clean.
func worktreeStatusPorcelain(ctx context.Context, git gitRunner, path string) ([]byte, error) {
	return git(ctx, path, "status", "--porcelain")
}

// worktreeCommitAll stages everything and commits it to the current branch:
// `git -C <path> add -A` then `git -C <path> commit -m <msg>`. Used for the
// dirty-destroy WIP capture (§4.9). A missing git identity (user.name/email) or
// nothing-to-commit surfaces as an error the caller treats as "don't remove".
//
// --no-gpg-sign: this is an automated safety commit, not a user-authored one, so
// it must not depend on a commit-signing agent (e.g. gpg / a 1Password SSH
// signer) being reachable. Requiring the agent would turn a never-lose WIP commit
// into a failure that blocks the teardown, and the user can re-sign/amend the
// branch later.
func worktreeCommitAll(ctx context.Context, git gitRunner, path, msg string) error {
	if _, err := git(ctx, path, "add", "-A"); err != nil {
		return fmt.Errorf("sandbox: git add -A: %w", err)
	}
	if _, err := git(ctx, path, "commit", "--no-gpg-sign", "-m", msg); err != nil {
		return fmt.Errorf("sandbox: git commit: %w", err)
	}
	return nil
}
