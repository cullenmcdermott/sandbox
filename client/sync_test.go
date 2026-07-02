package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSessionKeyDirRejectsTraversal mirrors internal/index's regression:
// sessionKeyDir must reject session ids that escape the state root via path
// traversal before any os.RemoveAll / os.WriteFile runs on the joined path.
// (sessionKeyDir feeds ensureSSHKey, which writes the private key, and
// RemoveLocalState, which RemoveAlls the directory.)
func TestSessionKeyDirRejectsTraversal(t *testing.T) {
	c := &Client{stateDir: t.TempDir()}

	bad := []string{"../../etc", "..", "a/../../b", "../sibling", "/abs/escape/../../.."}
	for _, id := range bad {
		dir, err := c.sessionKeyDir(id)
		if err == nil {
			t.Errorf("sessionKeyDir(%q) = %q, nil; want an escape error", id, dir)
			continue
		}
		if !strings.Contains(err.Error(), "escapes") {
			t.Errorf("sessionKeyDir(%q) error = %v; want an 'escapes session root' error", id, err)
		}
	}

	good := []string{"session-a", "claude-sdk-test", "abc123"}
	for _, id := range good {
		if _, err := c.sessionKeyDir(id); err != nil {
			t.Errorf("sessionKeyDir(%q) = %v; want nil for a well-formed id", id, err)
		}
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
