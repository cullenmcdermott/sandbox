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

// --- Wave 3: WorktreeStatus / ConvertToBranch / ReapWorktrees ----------------

// worktreeClientWithBackend is worktreeClient but exposes the fake backend so a
// reap test can drive its List (the live-session signal).
func worktreeClientWithBackend(t *testing.T, be *fakeBackend) (*Client, string) {
	t.Helper()
	stateDir := t.TempDir()
	c, err := New(WithBackend(be), WithStateDir(stateDir))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, stateDir
}

// setupWorktreeSession creates a real worktree for id, records the index entry,
// and returns a Session handle bound to it (as `attach`/Open would).
func setupWorktreeSession(t *testing.T, c *Client, repo, id string) *Session {
	t.Helper()
	info, err := c.createWorktree(context.Background(), WorktreeOn, repo, id)
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	seedWorktreeEntry(t, c, ID(id), info)
	return c.Open(ID(id))
}

// dirtyWorktree writes an uncommitted file into a worktree dir.
func dirtyWorktree(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatalf("dirty %s: %v", name, err)
	}
}

func TestWorktreeStatusCleanAndDirty(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)
	sess := setupWorktreeSession(t, c, repo, "claude-sdk-st")

	// Clean: fresh checkout of HEAD.
	st, err := sess.WorktreeStatus(ctx)
	if err != nil {
		t.Fatalf("WorktreeStatus (clean): %v", err)
	}
	if st.Dirty || len(st.Changed) != 0 {
		t.Errorf("clean worktree reported dirty: %+v", st)
	}
	if st.Branch != "sandbox/claude-sdk-st" {
		t.Errorf("branch = %q, want sandbox/claude-sdk-st", st.Branch)
	}
	if st.Path != filepath.Join(c.stateDir, "worktrees", "claude-sdk-st") {
		t.Errorf("path = %q", st.Path)
	}

	// Dirty: an added file shows up in Changed and flips Dirty.
	dirtyWorktree(t, st.Path, "new.txt")
	st, err = sess.WorktreeStatus(ctx)
	if err != nil {
		t.Fatalf("WorktreeStatus (dirty): %v", err)
	}
	if !st.Dirty {
		t.Error("dirty worktree not reported dirty")
	}
	if len(st.Changed) != 1 || st.Changed[0] != "new.txt" {
		t.Errorf("Changed = %v, want [new.txt]", st.Changed)
	}
}

func TestWorktreeStatusNoWorktreeAndMissing(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	c, _ := worktreeClient(t)

	// A session with no index entry (non-git / WorktreeOff) has no worktree.
	if _, err := c.Open(ID("claude-sdk-none")).WorktreeStatus(ctx); !errors.Is(err, ErrNoWorktree) {
		t.Fatalf("no worktree: got %v, want ErrNoWorktree", err)
	}

	// A recorded worktree whose dir is gone also errors ErrNoWorktree.
	repo := initRepo(t)
	sess := setupWorktreeSession(t, c, repo, "claude-sdk-miss")
	wt := filepath.Join(c.stateDir, "worktrees", "claude-sdk-miss")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("rm worktree: %v", err)
	}
	if _, err := sess.WorktreeStatus(ctx); !errors.Is(err, ErrNoWorktree) {
		t.Fatalf("missing dir: got %v, want ErrNoWorktree", err)
	}
}

func TestConvertToBranchCleanRenameOnly(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)
	sess := setupWorktreeSession(t, c, repo, "claude-sdk-cv")

	res, err := sess.ConvertToBranch(ctx, ConvertOptions{BranchName: "feat/nice"})
	if err != nil {
		t.Fatalf("ConvertToBranch (clean): %v", err)
	}
	if res.Committed || res.CommitSHA != "" {
		t.Errorf("clean convert must not commit: %+v", res)
	}
	if res.Branch != "feat/nice" {
		t.Errorf("Branch = %q, want feat/nice", res.Branch)
	}
	// The rename is reflected live in WorktreeStatus, and the old auto-branch is gone.
	st, err := sess.WorktreeStatus(ctx)
	if err != nil {
		t.Fatalf("WorktreeStatus after convert: %v", err)
	}
	if st.Branch != "feat/nice" {
		t.Errorf("post-convert branch = %q, want feat/nice", st.Branch)
	}
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "sandbox/claude-sdk-cv").CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("auto-branch should be renamed away, still present: %q", out)
	}
	// The index entry's branch was updated.
	entry, _ := c.index.Load("claude-sdk-cv")
	if entry.WorktreeBranch != "feat/nice" {
		t.Errorf("index WorktreeBranch = %q, want feat/nice", entry.WorktreeBranch)
	}
}

func TestConvertToBranchDirtyCommitsThenRenames(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)
	sess := setupWorktreeSession(t, c, repo, "claude-sdk-cvd")
	wt := filepath.Join(c.stateDir, "worktrees", "claude-sdk-cvd")
	dirtyWorktree(t, wt, "work.txt")

	res, err := sess.ConvertToBranch(ctx, ConvertOptions{BranchName: "fix/thing", Message: "save work"})
	if err != nil {
		t.Fatalf("ConvertToBranch (dirty): %v", err)
	}
	if !res.Committed || res.CommitSHA == "" {
		t.Errorf("dirty convert must commit + return SHA: %+v", res)
	}
	// The branch reflects the rename and is now clean; the committed file is reachable.
	st, err := sess.WorktreeStatus(ctx)
	if err != nil {
		t.Fatalf("WorktreeStatus after dirty convert: %v", err)
	}
	if st.Branch != "fix/thing" {
		t.Errorf("post-convert branch = %q, want fix/thing", st.Branch)
	}
	if st.Dirty {
		t.Errorf("worktree should be clean after convert, Changed=%v", st.Changed)
	}
	files, err := exec.Command("git", "-C", repo, "ls-tree", "--name-only", "fix/thing").CombinedOutput()
	if err != nil || !strings.Contains(string(files), "work.txt") {
		t.Errorf("committed work not on branch; ls-tree=%q err=%v", files, err)
	}
	// The commit message is the approved one.
	log, _ := exec.Command("git", "-C", repo, "log", "-1", "--format=%s", "fix/thing").CombinedOutput()
	if strings.TrimSpace(string(log)) != "save work" {
		t.Errorf("commit subject = %q, want %q", strings.TrimSpace(string(log)), "save work")
	}
}

func TestConvertToBranchDirtyEmptyMessageErrors(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)
	sess := setupWorktreeSession(t, c, repo, "claude-sdk-cve")
	dirtyWorktree(t, filepath.Join(c.stateDir, "worktrees", "claude-sdk-cve"), "x.txt")

	if _, err := sess.ConvertToBranch(ctx, ConvertOptions{BranchName: "feat/x"}); !errors.Is(err, ErrWorktreeDirty) {
		t.Fatalf("dirty + empty message: got %v, want ErrWorktreeDirty", err)
	}
	// Nothing was renamed: the auto-branch still exists.
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "sandbox/claude-sdk-cve").CombinedOutput()
	if !strings.Contains(string(out), "claude-sdk-cve") {
		t.Errorf("failed convert must not rename the branch; branch --list=%q", out)
	}
}

func TestConvertToBranchInvalidNameRejected(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)
	sess := setupWorktreeSession(t, c, repo, "claude-sdk-cvi")

	if _, err := sess.ConvertToBranch(ctx, ConvertOptions{BranchName: "bad..name"}); !errors.Is(err, ErrInvalidBranchName) {
		t.Fatalf("invalid ref: got %v, want ErrInvalidBranchName", err)
	}
}

func TestConvertToBranchNameTaken(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClient(t)
	sess := setupWorktreeSession(t, c, repo, "claude-sdk-cvt")
	// Pre-create the target branch in the shared repo.
	if out, err := exec.Command("git", "-C", repo, "branch", "feat/taken").CombinedOutput(); err != nil {
		t.Fatalf("pre-create branch: %v: %s", err, out)
	}

	if _, err := sess.ConvertToBranch(ctx, ConvertOptions{BranchName: "feat/taken"}); !errors.Is(err, ErrBranchNameTaken) {
		t.Fatalf("taken target: got %v, want ErrBranchNameTaken", err)
	}
	// The auto-branch is untouched (no -M force).
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "sandbox/claude-sdk-cvt").CombinedOutput()
	if !strings.Contains(string(out), "claude-sdk-cvt") {
		t.Errorf("taken-target convert must not rename; branch --list=%q", out)
	}
}

func TestReapDryRunMutatesNothing(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	be := newFakeBackend() // List returns nil ⇒ no live sessions
	c, _ := worktreeClientWithBackend(t, be)
	clean := setupWorktreeSession(t, c, repo, "claude-sdk-dr1")
	dirty := setupWorktreeSession(t, c, repo, "claude-sdk-dr2")
	_ = clean
	_ = dirty
	dirtyWorktree(t, filepath.Join(c.stateDir, "worktrees", "claude-sdk-dr2"), "d.txt")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{DryRun: true})
	if err != nil {
		t.Fatalf("ReapWorktrees dry-run: %v", err)
	}
	byID := reapByID(reaped)
	if byID["claude-sdk-dr1"].Action != reapRemoved {
		t.Errorf("clean dry-run action = %q, want removed", byID["claude-sdk-dr1"].Action)
	}
	if byID["claude-sdk-dr2"].Action != reapCommittedRemoved {
		t.Errorf("dirty dry-run action = %q, want committed-then-removed", byID["claude-sdk-dr2"].Action)
	}
	// Nothing mutated: both dirs and index entries survive, no reap commit landed.
	for _, id := range []string{"claude-sdk-dr1", "claude-sdk-dr2"} {
		if _, err := os.Stat(filepath.Join(c.stateDir, "worktrees", id)); err != nil {
			t.Errorf("dry-run removed dir %s: %v", id, err)
		}
		if _, err := c.index.Load(id); err != nil {
			t.Errorf("dry-run dropped index entry %s: %v", id, err)
		}
	}
	log, _ := exec.Command("git", "-C", repo, "log", "-1", "--format=%s", "sandbox/claude-sdk-dr2").CombinedOutput()
	if strings.TrimSpace(string(log)) != "seed" {
		t.Errorf("dry-run must not commit; branch tip = %q, want seed", strings.TrimSpace(string(log)))
	}
}

func TestReapCleanOrphanRemoved(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	be := newFakeBackend()
	c, _ := worktreeClientWithBackend(t, be)
	setupWorktreeSession(t, c, repo, "claude-sdk-ro")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	if rec := reapByID(reaped)["claude-sdk-ro"]; rec.Action != reapRemoved {
		t.Fatalf("action = %q, want removed", rec.Action)
	}
	// Dir gone, index entry dropped, branch preserved (I2).
	if _, err := os.Stat(filepath.Join(c.stateDir, "worktrees", "claude-sdk-ro")); !os.IsNotExist(err) {
		t.Errorf("clean orphan dir not removed: %v", err)
	}
	if _, err := c.index.Load("claude-sdk-ro"); err == nil {
		t.Error("clean orphan index entry not dropped")
	}
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "sandbox/claude-sdk-ro").CombinedOutput()
	if !strings.Contains(string(out), "claude-sdk-ro") {
		t.Errorf("reap must preserve the branch; branch --list=%q", out)
	}
}

func TestReapDirtyOrphanCommittedThenRemoved(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	be := newFakeBackend()
	c, _ := worktreeClientWithBackend(t, be)
	setupWorktreeSession(t, c, repo, "claude-sdk-rd")
	dirtyWorktree(t, filepath.Join(c.stateDir, "worktrees", "claude-sdk-rd"), "wip.txt")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	rec := reapByID(reaped)["claude-sdk-rd"]
	if rec.Action != reapCommittedRemoved || rec.CommitSHA == "" {
		t.Fatalf("action=%q sha=%q, want committed-then-removed + sha", rec.Action, rec.CommitSHA)
	}
	if _, err := os.Stat(filepath.Join(c.stateDir, "worktrees", "claude-sdk-rd")); !os.IsNotExist(err) {
		t.Errorf("dirty orphan dir not removed after commit: %v", err)
	}
	// The WIP work is reachable on the branch.
	files, err := exec.Command("git", "-C", repo, "ls-tree", "--name-only", "sandbox/claude-sdk-rd").CombinedOutput()
	if err != nil || !strings.Contains(string(files), "wip.txt") {
		t.Errorf("WIP work not preserved on branch; ls-tree=%q err=%v", files, err)
	}
	log, _ := exec.Command("git", "-C", repo, "log", "-1", "--format=%s", "sandbox/claude-sdk-rd").CombinedOutput()
	if !strings.Contains(string(log), "WIP at reap") {
		t.Errorf("reap WIP commit subject = %q", strings.TrimSpace(string(log)))
	}
}

func TestReapSkipsLiveSession(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	be := newFakeBackend()
	be.listStates = []State{{ID: ID("claude-sdk-live")}} // still live in the cluster
	c, _ := worktreeClientWithBackend(t, be)
	setupWorktreeSession(t, c, repo, "claude-sdk-live")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	if rec := reapByID(reaped)["claude-sdk-live"]; rec.Action != reapSkipped {
		t.Fatalf("live session action = %q, want skipped", rec.Action)
	}
	if _, err := os.Stat(filepath.Join(c.stateDir, "worktrees", "claude-sdk-live")); err != nil {
		t.Errorf("live-session worktree must not be removed: %v", err)
	}
}

func TestReapSkipsNonWorktreeJunk(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	be := newFakeBackend()
	c, _ := worktreeClientWithBackend(t, be)
	junk := filepath.Join(c.worktreesRoot(), "junkdir")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatalf("mkdir junk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(junk, "loose.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write junk file: %v", err)
	}

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	if rec := reapByID(reaped)["junkdir"]; rec.Action != reapSkipped {
		t.Fatalf("junk dir action = %q, want skipped", rec.Action)
	}
	if _, err := os.Stat(junk); err != nil {
		t.Errorf("junk dir must be left in place: %v", err)
	}
}

// reapByID indexes a reap report by session id for assertions.
func reapByID(reaped []ReapedWorktree) map[string]ReapedWorktree {
	m := make(map[string]ReapedWorktree, len(reaped))
	for _, r := range reaped {
		m[r.SessionID] = r
	}
	return m
}
