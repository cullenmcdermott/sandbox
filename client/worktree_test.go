package client

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// worktree_test.go covers the wave-2 per-session worktree lifecycle: the
// deterministic git ops (detect / add / rollback / capture-then-remove) exercised
// against REAL temp git repos, plus fake-backend orchestration of Create's
// worktree stamping + rollback and the missing-worktree Connect guard.
//
// The git-ops tests SELF-SKIP (visibly) when the git binary is absent so the
// suite stays green in a git-less environment (design §6 / requirement 3).

// requireGit skips the test when git is unavailable, else returns its path.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH; skipping worktree git-ops test") // gate-ok: real-git test, self-skips visibly; flox env + CI both carry git so CI always runs it
	}
}

// initRepo creates a real git repo with one commit and a committer identity, so
// worktree add (needs HEAD) and WIP commits (need an identity) work.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	runGit("add", "-A")
	runGit("commit", "-m", "seed")
	return dir
}

// worktreeClient builds a Client (fake backend) rooted at a temp state dir, using
// the REAL git runner so worktree ops hit a real repo.
func worktreeClient(t *testing.T) (*Client, string) {
	t.Helper()
	stateDir := t.TempDir()
	c, err := New(WithBackend(newFakeBackend()), WithStateDir(stateDir))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, stateDir
}

func TestCreateWorktreeDetectAndAdd(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, stateDir := worktreeClient(t)

	info, err := c.createWorktree(ctx, WorktreeAuto, repo, "claude-sdk-abc123")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	wantPath := filepath.Join(stateDir, "worktrees", "claude-sdk-abc123")
	if info.Path != wantPath {
		t.Errorf("worktree path = %q, want %q", info.Path, wantPath)
	}
	if info.Branch != "sandbox/claude-sdk-abc123" {
		t.Errorf("branch = %q, want sandbox/claude-sdk-abc123", info.Branch)
	}
	// RepoRoot is the git toplevel (symlink-resolved), so compare via EvalSymlinks.
	wantRoot, _ := filepath.EvalSymlinks(repo)
	if got, _ := filepath.EvalSymlinks(info.RepoRoot); got != wantRoot {
		t.Errorf("repo root = %q, want %q", info.RepoRoot, wantRoot)
	}
	// The worktree dir exists and is a checkout of HEAD (seed.txt present).
	if _, err := os.Stat(filepath.Join(info.Path, "seed.txt")); err != nil {
		t.Errorf("worktree missing seed.txt from HEAD: %v", err)
	}
	// The auto-branch exists in the repo.
	out, err := exec.Command("git", "-C", repo, "branch", "--list", info.Branch).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "claude-sdk-abc123") {
		t.Errorf("auto-branch not created: %q err=%v", out, err)
	}
}

func TestCreateWorktreeAutoFallbackNonGit(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	c, _ := worktreeClient(t)
	nonGit := t.TempDir()

	info, err := c.createWorktree(ctx, WorktreeAuto, nonGit, "claude-sdk-x")
	if err != nil {
		t.Fatalf("Auto on a non-git dir must fall back, not error: %v", err)
	}
	if info.Path != "" {
		t.Errorf("Auto fallback must yield no worktree, got %q", info.Path)
	}
}

func TestCreateWorktreeOnNonGitErrors(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	c, _ := worktreeClient(t)
	nonGit := t.TempDir()

	if _, err := c.createWorktree(ctx, WorktreeOn, nonGit, "claude-sdk-x"); !errors.Is(err, ErrNotAGitRepo) {
		t.Fatalf("WorktreeOn on a non-git dir: got %v, want ErrNotAGitRepo", err)
	}
}

func TestCreateWorktreeExists(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)

	if _, err := c.createWorktree(ctx, WorktreeOn, repo, "claude-sdk-dup"); err != nil {
		t.Fatalf("first createWorktree: %v", err)
	}
	// A second create for the same id collides on the path AND branch.
	if _, err := c.createWorktree(ctx, WorktreeOn, repo, "claude-sdk-dup"); !errors.Is(err, ErrWorktreeExists) {
		t.Fatalf("duplicate createWorktree: got %v, want ErrWorktreeExists", err)
	}
}

func TestRollbackWorktreeRemovesResidue(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)

	info, err := c.createWorktree(ctx, WorktreeOn, repo, "claude-sdk-rb")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	c.rollbackWorktree(ctx, info)

	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Errorf("rollback left the worktree dir behind: stat err = %v", err)
	}
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", info.Branch).CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("rollback left the auto-branch behind: %q", out)
	}
}

func TestTeardownWorktreeCleanRemoves(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)
	id := ID("claude-sdk-clean")

	info, err := c.createWorktree(ctx, WorktreeOn, repo, string(id))
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	// Record the entry teardown reads.
	seedWorktreeEntry(t, c, id, info)

	c.teardownWorktree(ctx, id)

	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Errorf("clean teardown must remove the worktree dir: stat err = %v", err)
	}
	// The branch is preserved (it IS the work), even for a clean teardown.
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", info.Branch).CombinedOutput()
	if !strings.Contains(string(out), string(id)) {
		t.Errorf("teardown must preserve the session branch; branch --list = %q", out)
	}
}

func TestTeardownWorktreeDirtyWIPCommitsThenRemoves(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)
	id := ID("claude-sdk-dirty")

	info, err := c.createWorktree(ctx, WorktreeOn, repo, string(id))
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	seedWorktreeEntry(t, c, id, info)
	// Dirty the worktree with an uncommitted new file.
	if err := os.WriteFile(filepath.Join(info.Path, "wip.txt"), []byte("unsaved work\n"), 0o644); err != nil {
		t.Fatalf("dirty the worktree: %v", err)
	}

	c.teardownWorktree(ctx, id)

	// The dir is gone (removed after the WIP commit)...
	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Errorf("dirty teardown must remove the worktree dir after committing: stat err = %v", err)
	}
	// ...and the work is preserved on the session branch: the WIP commit lands
	// with the expected message and wip.txt is reachable from the branch tip.
	log, err := exec.Command("git", "-C", repo, "log", "-1", "--format=%s", info.Branch).CombinedOutput()
	if err != nil {
		t.Fatalf("git log on session branch: %v: %s", err, log)
	}
	if want := "sandbox: WIP at destroy (" + string(id) + ")"; strings.TrimSpace(string(log)) != want {
		t.Errorf("WIP commit subject = %q, want %q", strings.TrimSpace(string(log)), want)
	}
	files, err := exec.Command("git", "-C", repo, "ls-tree", "--name-only", info.Branch).CombinedOutput()
	if err != nil || !strings.Contains(string(files), "wip.txt") {
		t.Errorf("WIP work not preserved on branch; ls-tree = %q err=%v", files, err)
	}
}

func TestTeardownWorktreeMissingDirIsNoOp(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)
	id := ID("claude-sdk-gone")

	info, err := c.createWorktree(ctx, WorktreeOn, repo, string(id))
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	seedWorktreeEntry(t, c, id, info)
	// User deleted the worktree dir out from under us.
	if err := os.RemoveAll(info.Path); err != nil {
		t.Fatalf("rm worktree: %v", err)
	}
	// Must not panic or error; prunes the stale admin entry.
	c.teardownWorktree(ctx, id)

	// The branch still exists (nothing captured/lost).
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", info.Branch).CombinedOutput()
	if !strings.Contains(string(out), string(id)) {
		t.Errorf("no-op teardown must preserve the branch; branch --list = %q", out)
	}
}

// --- Create/Connect orchestration (fake backend, real git) ------------------

func TestCreateStampsWorktree(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	be := newFakeBackend()
	c, _, _ := fakeClient(t, be)

	sess, err := c.Create(ctx, CreateOptions{ProjectPath: repo, ID: "claude-sdk-wt1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantWT := filepath.Join(c.stateDir, "worktrees", "claude-sdk-wt1")

	// The spec handed to the backend: WorkspacePath is the worktree, ProjectPath
	// stays the repo root.
	if be.gotSpec.WorkspacePath != wantWT {
		t.Errorf("spec WorkspacePath = %q, want worktree %q", be.gotSpec.WorkspacePath, wantWT)
	}
	if be.gotSpec.ProjectPath != repo {
		t.Errorf("spec ProjectPath = %q, want repo root %q", be.gotSpec.ProjectPath, repo)
	}
	// The session accessor and the persisted index entry both point at the worktree.
	if sess.WorktreePath() != wantWT {
		t.Errorf("Session.WorktreePath() = %q, want %q", sess.WorktreePath(), wantWT)
	}
	entry, err := c.index.Load("claude-sdk-wt1")
	if err != nil {
		t.Fatalf("index load: %v", err)
	}
	if entry.WorktreePath != wantWT || entry.WorktreeBranch != "sandbox/claude-sdk-wt1" || entry.RepoRoot == "" {
		t.Errorf("index worktree fields = {%q %q %q}, want worktree/branch/repoRoot set",
			entry.WorktreePath, entry.WorktreeBranch, entry.RepoRoot)
	}
	// The worktree dir was actually created.
	if _, err := os.Stat(filepath.Join(wantWT, "seed.txt")); err != nil {
		t.Errorf("worktree not materialized: %v", err)
	}
}

func TestCreateRollsBackWorktreeOnBackendFailure(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	be := newFakeBackend()
	be.createErr = errors.New("apiserver down")
	c, _, _ := fakeClient(t, be)

	if _, err := c.Create(ctx, CreateOptions{ProjectPath: repo, ID: "claude-sdk-rbfail"}); err == nil {
		t.Fatal("Create: want backend error, got nil")
	}
	// The worktree created before the failed cluster call was torn down: no dir,
	// no branch left behind.
	wantWT := filepath.Join(c.stateDir, "worktrees", "claude-sdk-rbfail")
	if _, err := os.Stat(wantWT); !os.IsNotExist(err) {
		t.Errorf("failed Create must roll back the worktree dir; stat err = %v", err)
	}
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "sandbox/claude-sdk-rbfail").CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("failed Create must roll back the auto-branch; branch --list = %q", out)
	}
}

func TestCreateWorktreeOffKeepsProjectPath(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	be := newFakeBackend()
	c, _, _ := fakeClient(t, be)

	sess, err := c.Create(ctx, CreateOptions{ProjectPath: repo, ID: "claude-sdk-off", Worktree: WorktreeOff})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if be.gotSpec.WorkspacePath != repo {
		t.Errorf("WorktreeOff spec WorkspacePath = %q, want repo root %q", be.gotSpec.WorkspacePath, repo)
	}
	if sess.WorktreePath() != "" {
		t.Errorf("WorktreeOff session must have no worktree, got %q", sess.WorktreePath())
	}
	if _, err := os.Stat(filepath.Join(c.stateDir, "worktrees", "claude-sdk-off")); !os.IsNotExist(err) {
		t.Error("WorktreeOff must not create a worktree dir")
	}
}

func TestWorktreeMissingForSync(t *testing.T) {
	dir := t.TempDir() // exists
	cases := []struct {
		name            string
		workspace, proj string
		want            bool
	}{
		{"non-git (equal paths)", "/work/repo", "/work/repo", false},
		{"worktree present", dir, "/work/repo", false},
		{"worktree missing", filepath.Join(dir, "gone"), "/work/repo", true},
		{"empty workspace", "", "/work/repo", false},
	}
	for _, tc := range cases {
		if got := worktreeMissingForSync(tc.workspace, tc.proj); got != tc.want {
			t.Errorf("%s: worktreeMissingForSync(%q,%q) = %v, want %v", tc.name, tc.workspace, tc.proj, got, tc.want)
		}
	}
}

// seedWorktreeEntry persists the index entry teardownWorktree reads.
func seedWorktreeEntry(t *testing.T, c *Client, id ID, info worktreeInfo) {
	t.Helper()
	if err := c.index.Save(string(id), index.Entry{
		SandboxSessionID: string(id),
		WorktreePath:     info.Path,
		WorktreeBranch:   info.Branch,
		RepoRoot:         info.RepoRoot,
	}); err != nil {
		t.Fatalf("seed index entry: %v", err)
	}
}

// An unborn HEAD (fresh `git init`, zero commits) cannot anchor `worktree add
// … HEAD`: Auto must fall back to no-worktree instead of failing the Create;
// On must error up front with the reason.
func TestCreateWorktreeUnbornHead(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cmd := exec.Command("git", "-C", dir, "init", "-b", "main")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	c, _ := worktreeClient(t)
	info, err := c.createWorktree(context.Background(), WorktreeAuto, dir, "claude-sdk-unborn1")
	if err != nil {
		t.Fatalf("Auto on unborn HEAD must fall back, got error: %v", err)
	}
	if info.Path != "" {
		t.Fatalf("Auto on unborn HEAD must not create a worktree, got %+v", info)
	}

	if _, err := c.createWorktree(context.Background(), WorktreeOn, dir, "claude-sdk-unborn2"); !errors.Is(err, ErrNotAGitRepo) {
		t.Fatalf("On with unborn HEAD: got %v, want ErrNotAGitRepo", err)
	}
}
