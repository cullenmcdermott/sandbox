package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
)

// seedIndexSession writes one session record into the default index location
// under home, so ResolveSessions has something to find.
func seedIndexSession(t *testing.T, home, id, title, worktree string) {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "sandbox", "remote-sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"sandboxSessionId": id,
		"backend":          "claude-pane",
		"projectPath":      "/proj",
		"renamedTitle":     title,
		"namespace":        "agent-sessions",
		"sandboxName":      id,
		"worktreePath":     worktree,
		"worktreeBranch":   "b",
	}
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The index-only commands (`worktree path`, shell completion) must resolve with
// NO usable kubeconfig — `cd $(sandbox worktree path …)` and a TAB press both
// have to work on a plane. They used to be built with newClient(), which
// resolves a kubeconfig at construction and fails without one before reading a
// single line of the index; completion swallowed that into "no suggestions", so
// TAB just went dead.
//
// KUBECONFIG points at a path that does not exist: any construction path that
// consults a kubeconfig fails under it, so this pins the absence of that
// dependency rather than merely the happy case.
func TestOfflineClientResolvesWithoutAKubeconfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on windows
	t.Setenv("KUBECONFIG", filepath.Join(home, "no-such-kubeconfig.yaml"))
	seedIndexSession(t, home, "claude-pane-demo-abc123", "offline demo", "/wt/demo")

	c, err := newOfflineClient()
	if err != nil {
		t.Fatalf("newOfflineClient with no kubeconfig: %v", err)
	}
	matches, err := c.ResolveSessions(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ResolveSessions: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1", len(matches))
	}
	if got := matches[0].WorktreePath; got != "/wt/demo" {
		t.Errorf("worktree path = %q, want /wt/demo", got)
	}
}

// The offline backend must REFUSE cluster work, not silently succeed at it: a
// future edit that reaches for a cluster operation from an index-only command
// should fail loudly at the call rather than quietly reintroduce the kubeconfig
// dependency this file exists to remove.
func TestOfflineBackendRefusesClusterOperations(t *testing.T) {
	b := offlineBackend{}
	ctx := context.Background()
	ref := client.Ref{}

	if _, err := b.List(ctx); !errors.Is(err, errOffline) {
		t.Errorf("List err = %v, want errOffline", err)
	}
	if _, err := b.Status(ctx, ref); !errors.Is(err, errOffline) {
		t.Errorf("Status err = %v, want errOffline", err)
	}
	if err := b.Destroy(ctx, ref); !errors.Is(err, errOffline) {
		t.Errorf("Destroy err = %v, want errOffline", err)
	}
	if err := b.Suspend(ctx, ref); !errors.Is(err, errOffline) {
		t.Errorf("Suspend err = %v, want errOffline", err)
	}
	if _, err := b.RunnerToken(ctx, ref); !errors.Is(err, errOffline) {
		t.Errorf("RunnerToken err = %v, want errOffline", err)
	}
	if _, err := b.Watch(ctx); !errors.Is(err, errOffline) {
		t.Errorf("Watch err = %v, want errOffline", err)
	}
}
