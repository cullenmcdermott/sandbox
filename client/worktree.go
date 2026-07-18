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
// lifecycle (design docs/archive/worktree-lifecycle-design.md §4.3/§4.7/§4.9): detect a
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

// --- Wave 3: the remaining deterministic SDK git surface (design §7) ----------
//
// WorktreeStatus, ConvertToBranch, and ReapWorktrees complete the deterministic
// git surface. Like the wave-2 ops, NO LLM ever runs here (I3): ConvertToBranch
// takes already-approved strings (the TUI/caller owns the LLM proposal + human
// confirmation), and every git invocation is pure and testable against a temp repo.

// WorktreeStatus holds the deterministic git facts about a session's worktree,
// gathered for the convert-to-branch modal / diff view (design §7). It carries no
// LLM-generated content — just what git reports.
type WorktreeStatus struct {
	Path    string   // the local worktree dir
	Branch  string   // current branch (sandbox/<id> until converted), via rev-parse --abbrev-ref
	Dirty   bool     // true when there are uncommitted changes
	Changed []string // porcelain paths of changed files (for the diff/modal)
}

// WorktreeStatus returns the deterministic git facts for the session's worktree:
// current branch (read live via `git rev-parse --abbrev-ref HEAD`, so a post-convert
// rename is reflected — the index branch is not trusted blindly), dirty flag, and
// the porcelain changed-file list. It returns ErrNoWorktree when the session has no
// recorded worktree (non-git fallback / WorktreeOff) or its directory is missing on
// disk. Read-only: it never mutates the worktree.
func (s *Session) WorktreeStatus(ctx context.Context) (WorktreeStatus, error) {
	path, _, _, err := s.resolveWorktree()
	if err != nil {
		return WorktreeStatus{}, err
	}
	git := s.c.git()
	branchOut, berr := git(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	if berr != nil {
		return WorktreeStatus{}, fmt.Errorf("sandbox: read worktree branch: %w", berr)
	}
	branch := strings.TrimSpace(string(branchOut))
	porcelain, serr := worktreeStatusPorcelain(ctx, git, path)
	if serr != nil {
		return WorktreeStatus{}, fmt.Errorf("sandbox: read worktree status: %w", serr)
	}
	changed := parsePorcelainPaths(porcelain)
	return WorktreeStatus{
		Path:    path,
		Branch:  branch,
		Dirty:   len(changed) > 0,
		Changed: changed,
	}, nil
}

// ConvertOptions parameterizes ConvertToBranch. Both strings are ALREADY human-
// approved (LLM-proposed + confirmed in the TUI); ConvertToBranch validates and
// executes them but never generates them.
type ConvertOptions struct {
	BranchName string // human-approved target branch, e.g. "feat/foo"; validated as a git ref
	Message    string // human-approved commit message; empty allowed only when the worktree is clean
}

// BranchResult reports the outcome of ConvertToBranch.
type BranchResult struct {
	Branch    string // the final (renamed) branch name
	Committed bool   // whether a commit was created (only when the worktree was dirty)
	CommitSHA string // the new commit's SHA when Committed, else ""
}

// ConvertToBranch runs the DETERMINISTIC git half of convert-to-branch (design
// §4.6 step 4): it validates BranchName up front, commits any dirty state under
// Message, then renames the session's auto-branch (sandbox/<id>) onto BranchName.
// It takes already-approved strings — no LLM, no prompting.
//
// Order and failure modes:
//   - BranchName is validated with `git check-ref-format --branch` first, so a bad
//     name is rejected before anything mutates (ErrInvalidBranchName).
//   - An existing target branch is a hard error (ErrBranchNameTaken); it never
//     force-renames (no -M). This is checked BEFORE the commit so a taken name
//     doesn't leave a stray commit behind.
//   - A dirty worktree with an empty Message is ErrWorktreeDirty (a message is
//     required to capture the work). A dirty worktree with a Message is committed
//     with --no-gpg-sign (same signing-agent reliability rationale as the wave-2
//     WIP commit — the human can amend/sign afterward), then renamed.
//   - A clean worktree is a pure rename (Committed=false, CommitSHA="").
//
// On success the local index entry's WorktreeBranch is updated to the new name.
func (s *Session) ConvertToBranch(ctx context.Context, opt ConvertOptions) (BranchResult, error) {
	path, _, _, err := s.resolveWorktree()
	if err != nil {
		return BranchResult{}, err
	}
	git := s.c.git()

	// 1. Validate the target name up front (reject before any mutation).
	if _, verr := git(ctx, path, "check-ref-format", "--branch", opt.BranchName); verr != nil {
		return BranchResult{}, fmt.Errorf("%w: %q", ErrInvalidBranchName, opt.BranchName)
	}

	// 2. Resolve the current branch (the rename source). Read live so a prior
	//    convert is reflected rather than trusting the index blindly.
	curOut, cerr := git(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	if cerr != nil {
		return BranchResult{}, fmt.Errorf("sandbox: read worktree branch: %w", cerr)
	}
	current := strings.TrimSpace(string(curOut))

	// 3. Reject a taken target BEFORE committing, so a collision never leaves a
	//    stray commit on the source branch (no -M force — the human picks another).
	if branchExists(ctx, git, path, opt.BranchName) {
		return BranchResult{}, fmt.Errorf("%w: %s", ErrBranchNameTaken, opt.BranchName)
	}

	// 4. Capture dirty state, if any. Empty message + dirty is refused (§7).
	result := BranchResult{Branch: opt.BranchName}
	porcelain, serr := worktreeStatusPorcelain(ctx, git, path)
	if serr != nil {
		return BranchResult{}, fmt.Errorf("sandbox: read worktree status: %w", serr)
	}
	if strings.TrimSpace(string(porcelain)) != "" {
		if strings.TrimSpace(opt.Message) == "" {
			return BranchResult{}, fmt.Errorf("%w: a commit message is required to convert a dirty worktree", ErrWorktreeDirty)
		}
		if cErr := worktreeCommitAll(ctx, git, path, opt.Message); cErr != nil {
			return BranchResult{}, cErr
		}
		shaOut, shaErr := git(ctx, path, "rev-parse", "HEAD")
		if shaErr != nil {
			return BranchResult{}, fmt.Errorf("sandbox: read commit sha: %w", shaErr)
		}
		result.Committed = true
		result.CommitSHA = strings.TrimSpace(string(shaOut))
	}

	// 5. Rename the auto-branch onto the approved name. history is preserved.
	if _, rerr := git(ctx, path, "branch", "-m", current, opt.BranchName); rerr != nil {
		return BranchResult{}, fmt.Errorf("sandbox: git branch -m %s %s: %w", current, opt.BranchName, rerr)
	}

	// 6. Reflect the rename in the local index so reap/teardown target the new name.
	if entry, lerr := s.c.index.Load(string(s.ref.ID)); lerr == nil {
		entry.WorktreeBranch = opt.BranchName
		_ = s.c.index.Save(string(s.ref.ID), entry)
	}
	return result, nil
}

// resolveWorktree loads the session's recorded worktree from the local index and
// returns its path, branch, and repo root. It errors ErrNoWorktree when there is
// no worktree recorded (non-git / WorktreeOff) or the directory is gone on disk —
// the shared precondition for the deterministic per-session git surface.
func (s *Session) resolveWorktree() (path, branch, repoRoot string, err error) {
	entry, lerr := s.c.index.Load(string(s.ref.ID))
	if lerr != nil || entry.WorktreePath == "" {
		return "", "", "", fmt.Errorf("%w: %s", ErrNoWorktree, s.ref.ID)
	}
	if !pathExists(entry.WorktreePath) {
		return "", "", "", fmt.Errorf("%w: directory %s is missing", ErrNoWorktree, entry.WorktreePath)
	}
	return entry.WorktreePath, entry.WorktreeBranch, entry.RepoRoot, nil
}

// branchExists reports whether refs/heads/<name> resolves in the worktree's repo.
// A non-zero exit from show-ref --verify --quiet means the ref does not exist.
func branchExists(ctx context.Context, git gitRunner, dir, name string) bool {
	_, err := git(ctx, dir, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// parsePorcelainPaths extracts the changed-file paths from `git status --porcelain`
// output. Each line is `XY <path>` (two status columns, a space, then the path); a
// rename is `XY <old> -> <new>`, for which the NEW path is reported. Blank lines
// are skipped.
func parsePorcelainPaths(out []byte) []string {
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		// Columns 0-1 are the status, column 2 is a space; the path starts at 3.
		p := strings.TrimSpace(line[3:])
		if p == "" {
			continue
		}
		if idx := strings.Index(p, " -> "); idx >= 0 {
			p = p[idx+len(" -> "):]
		}
		paths = append(paths, p)
	}
	return paths
}

// ReapOptions parameterizes ReapWorktrees.
type ReapOptions struct {
	// DryRun classifies and reports what would happen without mutating anything
	// (no commits, no removals, no index or prune changes).
	DryRun bool
}

// ReapedWorktree reports the disposition of one enumerated worktree directory.
type ReapedWorktree struct {
	SessionID string // the worktree dir's basename (== session id)
	Path      string // the worktree dir
	Branch    string // the session/worktree branch, when known
	Action    string // "removed" | "committed-then-removed" | "skipped"
	CommitSHA string // set when dirty work was captured before removal
}

// Reap action / skip labels.
const (
	reapRemoved            = "removed"
	reapCommittedRemoved   = "committed-then-removed"
	reapSkipped            = "skipped"
	reapWIPMessagePrefixed = "sandbox: WIP at reap"
)

// ReapWorktrees GCs orphaned per-session worktrees under worktreesRoot() (design
// §4.8). A worktree is an orphan when its session is neither live in the cluster
// (backend.List) nor otherwise reachable; a worktree whose session still exists is
// reported as "skipped" (never touched). For each orphan:
//
//   - clean → `git worktree remove` + drop the local index entry ("removed").
//   - dirty → NEVER deleted outright (I2): a WIP commit is made to its branch
//     first (reusing the wave-2 worktreeCommitAll), then removed
//     ("committed-then-removed", CommitSHA set). A failed WIP commit leaves the
//     worktree untouched and is reported "skipped".
//
// Ownership gate [V1]: the state dir is global (~/.local/share/sandbox) and
// SHARED across clusters/namespaces, but backend.List only reports sessions in
// the CURRENT namespace on the CURRENT kubeconfig context — a live session in
// another namespace/cluster is absent from that listing. So a dir is reaped only
// when its local index entry proves ownership by the current namespace: a dir
// whose entry records a DIFFERENT namespace (owned elsewhere, likely still live)
// and a dir with NO index entry at all (ownership cannot be established — no
// reap-by-git-discovery, no junk removal) are both "skipped", left in place.
//
// After removals it runs `git worktree prune` once per distinct repo root
// (silently — no fabricated "pruned" entries). RepoRoot and branch come from the
// owning index entry. DryRun classifies and reports the would-be action without
// mutating anything.
//
// Reporting choice: EVERY enumerated dir is reported (including live-session,
// foreign-namespace, and index-less dirs as "skipped"), so a caller (e.g.
// `sandbox worktree gc`) sees the full classification rather than a silent subset.
func (c *Client) ReapWorktrees(ctx context.Context, opt ReapOptions) ([]ReapedWorktree, error) {
	root := c.worktreesRoot()
	dirents, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // nothing created yet
		}
		return nil, fmt.Errorf("sandbox: enumerate worktrees: %w", err)
	}

	// Liveness signal: a session present in the cluster listing (any status,
	// including suspended) is NOT an orphan — its worktree is a live laptop
	// artifact (§4.9). A List failure is fatal: reaping without knowing which
	// sessions are live risks removing a live session's worktree.
	states, lerr := c.backend.List(ctx)
	if lerr != nil {
		return nil, fmt.Errorf("sandbox: list sessions for reap: %w", lerr)
	}
	live := make(map[string]bool, len(states))
	for _, st := range states {
		live[string(st.ID)] = true
	}

	git := c.git()
	var reaped []ReapedWorktree
	pruneRoots := map[string]bool{} // distinct repo roots that had a removal

	for _, de := range dirents {
		if !de.IsDir() {
			continue
		}
		id := de.Name()
		dir := filepath.Join(root, id)
		rec := ReapedWorktree{SessionID: id, Path: dir}

		// Live session ⇒ never touch its worktree.
		if live[id] {
			rec.Action = reapSkipped
			rec.Branch = c.reapBranchHint(id)
			reaped = append(reaped, rec)
			continue
		}

		// [V1] Ownership gate: only reap a dir whose index entry proves it belongs
		// to THIS namespace. An index-less dir (ownership can't be established) and
		// a dir owned by another namespace (likely a live session on a different
		// cluster sharing the global state dir) are both skipped — reaping either
		// could WIP-commit + os.RemoveAll a live session's worktree, destroying
		// gitignored files `git add -A` never captures.
		entry, entryErr := c.index.Load(id)
		if entryErr != nil || entry.WorktreePath == "" {
			rec.Action = reapSkipped
			reaped = append(reaped, rec)
			continue
		}
		if ns := entry.Namespace; ns != "" && ns != c.backend.Namespace() {
			rec.Branch = entry.WorktreeBranch
			rec.Action = reapSkipped
			reaped = append(reaped, rec)
			continue
		}

		// Repo root (for `worktree remove`/`prune`) and branch come from the owning
		// index entry. A dir with no recorded repo root is left in place.
		repoRoot, branch := entry.RepoRoot, entry.WorktreeBranch
		rec.Branch = branch
		if repoRoot == "" {
			rec.Action = reapSkipped
			reaped = append(reaped, rec)
			continue
		}

		// Classify dirty vs clean.
		porcelain, serr := worktreeStatusPorcelain(ctx, git, dir)
		if serr != nil {
			// Can't read status ⇒ don't risk a mutation; report skipped.
			rec.Action = reapSkipped
			reaped = append(reaped, rec)
			continue
		}
		dirty := strings.TrimSpace(string(porcelain)) != ""

		if opt.DryRun {
			if dirty {
				rec.Action = reapCommittedRemoved
			} else {
				rec.Action = reapRemoved
			}
			reaped = append(reaped, rec)
			continue
		}

		if dirty {
			// Never delete dirty work outright (I2): WIP-commit to its branch first.
			msg := fmt.Sprintf("%s (%s)", reapWIPMessagePrefixed, id)
			if cErr := worktreeCommitAll(ctx, git, dir, msg); cErr != nil {
				// Commit failed ⇒ leave the worktree and its work in place.
				rec.Action = reapSkipped
				reaped = append(reaped, rec)
				continue
			}
			if shaOut, shaErr := git(ctx, dir, "rev-parse", "HEAD"); shaErr == nil {
				rec.CommitSHA = strings.TrimSpace(string(shaOut))
			}
			rec.Action = reapCommittedRemoved
		} else {
			rec.Action = reapRemoved
		}

		// Remove the worktree dir (best-effort, mirroring teardownWorktree: no
		// --force — the capture step above already handled the dirty case) and drop
		// the local index entry. The branch is preserved (it IS the work).
		_, _ = git(ctx, repoRoot, "worktree", "remove", dir)
		_ = os.RemoveAll(dir) // in case `worktree remove` didn't take
		_ = c.index.Delete(id)
		pruneRoots[repoRoot] = true
		reaped = append(reaped, rec)
	}

	// Prune stale admin entries once per distinct repo root (silently — §4.8).
	if !opt.DryRun {
		for repoRoot := range pruneRoots {
			_, _ = git(ctx, repoRoot, "worktree", "prune")
		}
	}
	return reaped, nil
}

// reapBranchHint returns the recorded branch for a live-session worktree, best
// effort, purely for the reap report (never runs git against the live session).
func (c *Client) reapBranchHint(id string) string {
	if entry, err := c.index.Load(id); err == nil {
		return entry.WorktreeBranch
	}
	return ""
}
