package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// worktree_wave1_test.go covers the wave-1 WorkspacePath data-model split and
// the state-dir layout break: the local Mutagen endpoints derive from the
// workspace path, the worktree root derives from the state dir, and the ssh
// include dir migrates from its old sibling location into the state dir. Wave 1
// does not create worktrees, so WorkspacePath always equals ProjectPath here.

// TestStartProjectSyncUsesWorkspaceEndpoints pins that both Mutagen endpoints
// (local alpha ProjectPath and remote beta RemotePath) are the WORKSPACE path,
// so a future per-session worktree syncs the worktree dir — not the repo root —
// on both sides (the transcript-resumability identity, design §2.2/§4.2).
func TestStartProjectSyncUsesWorkspaceEndpoints(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	be := newFakeBackend()
	c, _, _ := fakeClient(t, be)

	_, spec, err := c.startProjectSync(context.Background(), "claude-sdk-abc123", "/work/repo", "/keys/id_ed25519", 12345)
	if err != nil {
		t.Fatalf("startProjectSync: %v", err)
	}
	if spec.ProjectPath != "/work/repo" || spec.RemotePath != "/work/repo" {
		t.Errorf("sync endpoints = alpha %q beta %q, want both /work/repo (the workspace path)", spec.ProjectPath, spec.RemotePath)
	}
}

// TestWorktreesRoot pins the worktree root as a child of the state dir, so every
// sandbox artifact (index, keys, ssh, worktrees) stays under one root and a
// WithStateDir consumer keeps them inside their app dir.
func TestWorktreesRoot(t *testing.T) {
	be := newFakeBackend()
	dir := t.TempDir()
	c, err := New(WithBackend(be), WithStateDir(dir))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := c.worktreesRoot(), filepath.Join(dir, "worktrees"); got != want {
		t.Errorf("worktreesRoot() = %q, want %q", got, want)
	}
}

// TestMigrateSSHDir exercises the one-time relocation of the ssh include dir from
// its old sibling location (dir(stateDir)/ssh) into the state dir (stateDir/ssh),
// including the ~/.ssh/config Include rewrite (preserving the C5 quoted form).
func TestMigrateSSHDir(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "remote-sessions")
	oldDir := filepath.Join(root, "ssh") // sibling of the state dir
	oldConfig := filepath.Join(oldDir, "config")
	newInclude := filepath.Join(stateDir, "ssh", "config")

	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatalf("seed old ssh dir: %v", err)
	}
	if err := os.WriteFile(oldConfig, []byte("Host sandbox-x\n  Port 1\n"), 0o600); err != nil {
		t.Fatalf("seed old config: %v", err)
	}
	userCfg := filepath.Join(root, "user_ssh_config")
	if err := os.WriteFile(userCfg, []byte(fmt.Sprintf("Include %q\n\nHost other\n  HostName h\n", oldConfig)), 0o600); err != nil {
		t.Fatalf("seed user config: %v", err)
	}

	be := newFakeBackend()
	c, err := New(WithBackend(be), WithStateDir(stateDir))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.migrateSSHDir(newInclude, userCfg)

	// The dir (and its contents) moved to the new location; the old one is gone.
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("old ssh dir should be gone after migration, stat err = %v", err)
	}
	moved, err := os.ReadFile(newInclude)
	if err != nil {
		t.Fatalf("new include not present after migration: %v", err)
	}
	if string(moved) != "Host sandbox-x\n  Port 1\n" {
		t.Errorf("migrated config content = %q, want the seeded block", moved)
	}
	// The Include line now points at the new path (quoted), and nothing else moved.
	uc, err := os.ReadFile(userCfg)
	if err != nil {
		t.Fatalf("read user config: %v", err)
	}
	wantInclude := fmt.Sprintf("Include %q", newInclude)
	if got := string(uc); !strings.Contains(got, wantInclude) {
		t.Errorf("user config missing rewritten include %q; got:\n%s", wantInclude, got)
	}
	if got := string(uc); strings.Contains(got, fmt.Sprintf("Include %q", oldConfig)) {
		t.Errorf("user config still references the old include path; got:\n%s", got)
	}
	if got := string(uc); !strings.Contains(got, "Host other") {
		t.Errorf("migration clobbered unrelated ssh config; got:\n%s", got)
	}

	// Idempotent: a second run is a no-op (new dir now exists) and must not error
	// or duplicate anything.
	c.migrateSSHDir(newInclude, userCfg)
	if _, err := os.Stat(newInclude); err != nil {
		t.Errorf("second migrateSSHDir disturbed the new include: %v", err)
	}
}
