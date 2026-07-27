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

// The CLI's offline client must REFUSE cluster work, not silently succeed at
// it: a future edit that reaches for a cluster operation from an index-only
// command should fail loudly at the call rather than quietly reintroduce the
// kubeconfig dependency these commands exist without.
//
// This asserts the CLI's own wiring — that newOfflineClient really hands back an
// offline client — through the PUBLIC client.ErrOffline. The backend itself is
// an unexported SDK detail (client/offline.go), and its per-method refusals are
// pinned there by TestOfflineDoesNotLoadKubeconfigAndClusterMethodsFailTyped;
// duplicating that method-by-method here would just re-test the SDK.
func TestOfflineClientRefusesClusterOperations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KUBECONFIG", filepath.Join(home, "no-such-kubeconfig.yaml"))

	c, err := newOfflineClient()
	if err != nil {
		t.Fatalf("newOfflineClient: %v", err)
	}
	ctx := context.Background()

	if _, err := c.List(ctx); !errors.Is(err, client.ErrOffline) {
		t.Errorf("List err = %v, want client.ErrOffline", err)
	}
	if _, err := c.Watch(ctx); !errors.Is(err, client.ErrOffline) {
		t.Errorf("Watch err = %v, want client.ErrOffline", err)
	}
}
