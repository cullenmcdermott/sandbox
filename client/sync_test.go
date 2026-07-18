package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// TestSessionKeyDirRejectsTraversal mirrors internal/index's regression:
// sessionKeyDir must reject session ids that escape the state root via path
// traversal before any os.RemoveAll / os.WriteFile runs on the joined path.
// (sessionKeyDir feeds ensureSSHKey, which writes the private key, and
// RemoveLocalState, which RemoveAlls the directory.) [V12] The empty id and "."
// are included because filepath.Rel(root, root) == "." resolves to the state root
// itself — the whole-tree-delete case the charset + rel=="." guards close.
func TestSessionKeyDirRejectsTraversal(t *testing.T) {
	c := &Client{stateDir: t.TempDir()}

	bad := []string{"", ".", "../../etc", "..", "a/../../b", "../sibling", "/abs/escape/../../..", "UPPER", "has space"}
	for _, id := range bad {
		dir, err := c.sessionKeyDir(id)
		if err == nil {
			t.Errorf("sessionKeyDir(%q) = %q, nil; want a rejection error", id, dir)
			continue
		}
		// Either the charset guard ("invalid session id") or the traversal guard
		// ("escapes session root") may fire first; both are valid rejections.
		if !strings.Contains(err.Error(), "escapes") && !strings.Contains(err.Error(), "invalid session id") {
			t.Errorf("sessionKeyDir(%q) error = %v; want an id-rejection error", id, err)
		}
	}

	good := []string{"session-a", "claude-sdk-test", "abc123"}
	for _, id := range good {
		if _, err := c.sessionKeyDir(id); err != nil {
			t.Errorf("sessionKeyDir(%q) = %v; want nil for a well-formed id", id, err)
		}
	}
}

// TestRemoveLocalStateEmptyIDLeavesRootIntact is the [V12] regression: an empty
// or "." session id must never cause RemoveLocalState to os.RemoveAll the entire
// state root. sessionKeyDir now rejects it, so RemoveLocalState leaves the root's
// contents (other sessions' keys, the index, ssh config) untouched.
func TestRemoveLocalStateEmptyIDLeavesRootIntact(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // keep sshConfig()'s ~/.ssh work hermetic
	root := t.TempDir()
	// Seed a sibling artifact that must survive.
	survivor := filepath.Join(root, "other-session")
	if err := os.MkdirAll(survivor, 0o700); err != nil {
		t.Fatal(err)
	}
	c := &Client{stateDir: root, index: index.New(root)}

	for _, id := range []string{"", "."} {
		c.RemoveLocalState(ID(id)) // sessionKeyDir rejects the id → no os.RemoveAll(root)
		if _, err := os.Stat(survivor); err != nil {
			t.Fatalf("RemoveLocalState(%q) deleted state-root contents: %v", id, err)
		}
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("RemoveLocalState(%q) deleted the state root itself: %v", id, err)
		}
	}
}

// TestSSHConfigPathIsInsideStateDir pins the per-session SSH include location to
// <stateDir>/ssh/config — INSIDE the state dir, where the client's post-migration
// sshConfig() writes it. [V13] The CLI's local `sandbox sync --terminate` helper
// computes the same path via this shared function, so it can never drift back to
// the pre-migration sibling dir (which silently no-op'd alias removals).
func TestSSHConfigPathIsInsideStateDir(t *testing.T) {
	root := "/Users/x/.local/share/sandbox/remote-sessions"
	want := filepath.Join(root, "ssh", "config")
	if got := SSHConfigPath(root); got != want {
		t.Errorf("SSHConfigPath(%q) = %q, want %q (must be inside the state dir)", root, got, want)
	}
	// It must NOT be the pre-migration sibling location.
	sibling := filepath.Join(filepath.Dir(root), "ssh", "config")
	if got := SSHConfigPath(root); got == sibling {
		t.Errorf("SSHConfigPath returned the stale sibling path %q", sibling)
	}
}

// TestWaitOpencodeReadyAnyStatus: waitOpencodeReady treats any HTTP response
// (even non-2xx) as ready, because a response at all proves the pod-side server
// answered.
func TestWaitOpencodeReadyAnyStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // 401 still means "listening"
	}))
	defer srv.Close()

	if err := waitOpencodeReady(context.Background(), srv.URL+"/"); err != nil {
		t.Fatalf("ready probe failed against a live server: %v", err)
	}
}

// TestWaitOpencodeReadyTransportError: a transport error (nothing listening) is
// retried until the context expires, rather than false-passing the way a bare TCP
// dial did.
func TestWaitOpencodeReadyTransportError(t *testing.T) {
	// Bind then immediately close to get a port nothing is listening on.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/"
	srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := waitOpencodeReady(ctx, url); err == nil {
		t.Fatal("ready probe should not pass when nothing is listening")
	}
}
