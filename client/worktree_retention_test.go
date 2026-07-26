package client

// worktree_retention_test.go — the retention gates added to ReapWorktrees
// (T5). These run against REAL temp git repos, like the rest of the worktree
// suite: the gates are git questions ("is this branch merged?", "when was this
// last committed to?") and a stubbed git runner would only prove that the stub
// agrees with itself.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// commitInWorktree makes a real commit inside a worktree dir, so the branch
// carries work that is NOT on the base branch.
func commitInWorktree(t *testing.T, dir, name, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--no-gpg-sign", "-m", msg}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
}

// backdateEntry rewrites an index entry's timestamps so the age gate sees an
// old worktree without the test having to sleep.
func backdateEntry(t *testing.T, c *Client, id string, age time.Duration) {
	t.Helper()
	entry, err := c.index.Load(id)
	if err != nil {
		t.Fatalf("load entry %s: %v", id, err)
	}
	when := time.Now().Add(-age)
	entry.CreatedAt = when
	entry.LastActivity = when
	if err := c.index.Save(id, entry); err != nil {
		t.Fatalf("save entry %s: %v", id, err)
	}
}

// TestReapRetainsUnlandedByDefault is the headline semantics change: a branch
// carrying commits that are not on main is NOT reaped, even though its session
// is gone. Worktrees outlive sessions; unmerged work is the strongest signal
// that someone still wants the checkout.
func TestReapRetainsUnlandedByDefault(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClientWithBackend(t, newFakeBackend())
	setupWorktreeSession(t, c, repo, "claude-sdk-unlanded")
	dir := filepath.Join(c.stateDir, "worktrees", "claude-sdk-unlanded")
	commitInWorktree(t, dir, "feature.txt", "real work")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	rec := reapByID(reaped)["claude-sdk-unlanded"]
	if rec.Action != reapSkipped {
		t.Fatalf("action = %q, want skipped (unlanded work)", rec.Action)
	}
	if !strings.Contains(rec.Reason, "not on main") {
		t.Errorf("reason = %q, want it to name the unmerged commits", rec.Reason)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("unlanded worktree was removed: %v", err)
	}
}

// TestReapUnlandedOptIn: the caller can still ask for it explicitly.
func TestReapUnlandedOptIn(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClientWithBackend(t, newFakeBackend())
	setupWorktreeSession(t, c, repo, "claude-sdk-optin")
	dir := filepath.Join(c.stateDir, "worktrees", "claude-sdk-optin")
	commitInWorktree(t, dir, "feature.txt", "real work")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{ReapUnlanded: true})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	if rec := reapByID(reaped)["claude-sdk-optin"]; rec.Action != reapRemoved {
		t.Fatalf("action = %q, want removed with ReapUnlanded", rec.Action)
	}
	// The work still exists on the branch — reaping removes checkouts, not work.
	files, err := exec.Command("git", "-C", repo, "ls-tree", "--name-only", "sandbox/claude-sdk-optin").CombinedOutput()
	if err != nil || !strings.Contains(string(files), "feature.txt") {
		t.Errorf("branch lost the work; ls-tree=%q err=%v", files, err)
	}
}

// TestReapLandedBranchIsEligible: a branch with nothing unmerged is exactly
// what gc is for.
func TestReapLandedBranchIsEligible(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClientWithBackend(t, newFakeBackend())
	setupWorktreeSession(t, c, repo, "claude-sdk-landed")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	rec := reapByID(reaped)["claude-sdk-landed"]
	if rec.Action != reapRemoved {
		t.Fatalf("action = %q reason = %q, want removed", rec.Action, rec.Reason)
	}
}

// TestReapMinAgeRetainsRecent: the age gate keeps a fresh worktree even when
// its branch is fully merged.
func TestReapMinAgeRetainsRecent(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClientWithBackend(t, newFakeBackend())
	setupWorktreeSession(t, c, repo, "claude-sdk-fresh")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{MinAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	rec := reapByID(reaped)["claude-sdk-fresh"]
	if rec.Action != reapSkipped {
		t.Fatalf("action = %q, want skipped (too recent)", rec.Action)
	}
	if !strings.Contains(rec.Reason, "last touched") {
		t.Errorf("reason = %q, want an age explanation", rec.Reason)
	}
	if _, err := os.Stat(filepath.Join(c.stateDir, "worktrees", "claude-sdk-fresh")); err != nil {
		t.Errorf("recent worktree was removed: %v", err)
	}
}

// TestReapMinAgeUsesTheMostRecentSignal: an OLD index entry does not make a
// worktree eligible when the directory was touched a moment ago. The gate
// decides deletion, so the freshest signal wins.
func TestReapMinAgeUsesTheMostRecentSignal(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClientWithBackend(t, newFakeBackend())
	setupWorktreeSession(t, c, repo, "claude-sdk-mixed")
	// Index says "a year old"; the checkout itself was made just now.
	backdateEntry(t, c, "claude-sdk-mixed", 365*24*time.Hour)

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{MinAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	if rec := reapByID(reaped)["claude-sdk-mixed"]; rec.Action != reapSkipped {
		t.Fatalf("action = %q, want skipped — a recent mtime must veto a stale index entry", rec.Action)
	}
}

// TestReapMinAgeZeroDisablesTheGate keeps the pre-retention behavior available
// (and is what the existing reap tests rely on).
func TestReapMinAgeZeroDisablesTheGate(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClientWithBackend(t, newFakeBackend())
	setupWorktreeSession(t, c, repo, "claude-sdk-nogate")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{MinAge: 0})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	if rec := reapByID(reaped)["claude-sdk-nogate"]; rec.Action != reapRemoved {
		t.Fatalf("action = %q, want removed with the age gate off", rec.Action)
	}
}

// TestReapLiveGateStillWinsOverRetention: retention must not have weakened the
// safety gate. A live session's worktree is skipped for BEING LIVE, and the
// reason must say so — no retention reason may shadow it.
func TestReapLiveGateStillWins(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	be := newFakeBackend()
	be.listStates = []State{{ID: ID("claude-sdk-alive")}} // still live in the cluster
	c, _ := worktreeClientWithBackend(t, be)
	setupWorktreeSession(t, c, repo, "claude-sdk-alive")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{MinAge: 0, ReapUnlanded: true})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	rec := reapByID(reaped)["claude-sdk-alive"]
	if rec.Action != reapSkipped || rec.Reason != reasonLive {
		t.Fatalf("action=%q reason=%q, want skipped/%q", rec.Action, rec.Reason, reasonLive)
	}
	if _, err := os.Stat(filepath.Join(c.stateDir, "worktrees", "claude-sdk-alive")); err != nil {
		t.Errorf("live session's worktree was removed: %v", err)
	}
}

// TestReapForeignNamespaceReasonReported: the [V1] ownership gate keeps working
// and now explains itself.
func TestReapForeignNamespaceReasonReported(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClientWithBackend(t, newFakeBackend())
	setupWorktreeSession(t, c, repo, "claude-sdk-foreignr")
	entry, err := c.index.Load("claude-sdk-foreignr")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	entry.Namespace = "some-other-namespace"
	if err := c.index.Save("claude-sdk-foreignr", entry); err != nil {
		t.Fatalf("save: %v", err)
	}

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	rec := reapByID(reaped)["claude-sdk-foreignr"]
	if rec.Action != reapSkipped || rec.Reason != reasonForeignNS {
		t.Fatalf("action=%q reason=%q, want skipped/%q", rec.Action, rec.Reason, reasonForeignNS)
	}
}

// TestReapBaseBranchOverride: "landed" can be measured against a caller-named
// ref, for repos whose integration branch is not main.
func TestReapBaseBranchOverride(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClientWithBackend(t, newFakeBackend())
	setupWorktreeSession(t, c, repo, "claude-sdk-base")
	dir := filepath.Join(c.stateDir, "worktrees", "claude-sdk-base")
	commitInWorktree(t, dir, "feature.txt", "work")
	// A `develop` branch pointing AT the session's tip: measured against it,
	// nothing is unlanded.
	if out, err := exec.Command("git", "-C", repo, "branch", "develop", "sandbox/claude-sdk-base").CombinedOutput(); err != nil {
		t.Fatalf("create develop: %v: %s", err, out)
	}

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{BaseBranch: "develop"})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	if rec := reapByID(reaped)["claude-sdk-base"]; rec.Action != reapRemoved {
		t.Fatalf("action=%q reason=%q, want removed (merged into develop)", rec.Action, rec.Reason)
	}
}

// TestReapUnknownBaseBranchRetains is the fail-safe: when "landed" cannot be
// determined, the worktree stays. Deleting a checkout because git could not be
// interrogated is the one outcome this must never produce.
func TestReapUnknownBaseBranchRetains(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	c, _ := worktreeClientWithBackend(t, newFakeBackend())
	setupWorktreeSession(t, c, repo, "claude-sdk-nobase")

	reaped, err := c.ReapWorktrees(ctx, ReapOptions{BaseBranch: "no-such-branch"})
	if err != nil {
		t.Fatalf("ReapWorktrees: %v", err)
	}
	rec := reapByID(reaped)["claude-sdk-nobase"]
	if rec.Action != reapSkipped {
		t.Fatalf("action = %q, want skipped when the base is unresolvable", rec.Action)
	}
	if !strings.Contains(rec.Reason, "base branch") {
		t.Errorf("reason = %q, want it to name the undeterminable base", rec.Reason)
	}
}

// TestWorktreeAgeUnknownRetains covers the "no signal at all" path directly:
// an entry with no timestamps and a directory that does not exist.
func TestWorktreeAgeUnknownRetains(t *testing.T) {
	requireGit(t)
	_, known := worktreeAge(context.Background(), realGitRunner, filepath.Join(t.TempDir(), "gone"), index.Entry{})
	if known {
		t.Error("age reported as known with no signals available")
	}
	if got := ageReason(0, false, time.Hour); !strings.Contains(got, "unknown") {
		t.Errorf("ageReason(unknown) = %q", got)
	}
}

// TestRoundDur pins the human-facing duration rendering used in reap reports.
func TestRoundDur(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
		{8 * 24 * time.Hour, "8d"},
	} {
		if got := roundDur(tc.d); got != tc.want {
			t.Errorf("roundDur(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
