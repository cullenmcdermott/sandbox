package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// healthServer spins up a runner-like /healthz that reports the given
// protocolVersion (0 means "field omitted", mirroring a pre-handshake runner).
func healthServer(t *testing.T, protocolVersion int) *runner.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		body := map[string]any{"status": "ok"}
		if protocolVersion != 0 {
			body["protocolVersion"] = protocolVersion
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	c := runner.New(srv.URL, "test-token")
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	return c
}

// TestProtocolVersionWarningMatch covers the HIGH-severity gap this guards:
// the CLI must detect (and warn on, not silently ignore) a runner that speaks
// a different wire protocol version. A matching version must be silent — no
// warning noise on the steady-state happy path.
func TestProtocolVersionWarningMatch(t *testing.T) {
	rc := healthServer(t, session.ProtocolVersion)
	if w := protocolVersionWarning(rc); w != "" {
		t.Errorf("protocolVersionWarning with matching version = %q, want \"\"", w)
	}
}

// TestProtocolVersionWarningMismatch: a runner reporting a different version
// must produce a non-empty, human-readable advisory (warn, don't refuse — the
// caller (Connect) still proceeds and surfaces this via Connection.Warning).
// The advisory must be ACTIONABLE: it names both versions (so "old runner pod
// still on v1" is diagnosable at a glance — the §8 break renamed status/
// claudeSession wire fields, which silently decode empty against the wrong
// counterpart) and tells the user how to fix it (update the runner image /
// re-create the session).
func TestProtocolVersionWarningMismatch(t *testing.T) {
	remote := session.ProtocolVersion + 1
	rc := healthServer(t, remote)
	w := protocolVersionWarning(rc)
	if w == "" {
		t.Fatal("protocolVersionWarning with mismatched version = \"\", want a non-empty warning")
	}
	for _, must := range []string{
		fmt.Sprintf("v%d", remote),                  // the runner's version
		fmt.Sprintf("v%d", session.ProtocolVersion), // the CLI's expected version
		"runner image",                              // the remedy: update/pull the image
		"re-create",                                 // ... or re-create the session
	} {
		if !strings.Contains(w, must) {
			t.Errorf("mismatch warning is not actionable: missing %q in %q", must, w)
		}
	}
}

// TestProtocolVersionWarningMissing: an old runner image that predates the
// protocolVersion field (Health decodes it to 0) must also warn — a missing
// field is skew, not "assume compatible". Like the mismatch advisory, it must
// name the CLI's expected version and the remedy.
func TestProtocolVersionWarningMissing(t *testing.T) {
	rc := healthServer(t, 0)
	w := protocolVersionWarning(rc)
	if w == "" {
		t.Fatal("protocolVersionWarning with no reported version = \"\", want a non-empty warning")
	}
	for _, must := range []string{
		fmt.Sprintf("v%d", session.ProtocolVersion),
		"runner image",
		"re-create",
	} {
		if !strings.Contains(w, must) {
			t.Errorf("missing-version warning is not actionable: missing %q in %q", must, w)
		}
	}
}

func TestAppendWarning(t *testing.T) {
	cases := []struct {
		existing, addition, want string
	}{
		{"", "", ""},
		{"", "a", "a"},
		{"a", "", "a"},
		{"a", "b", "a; b"},
	}
	for _, c := range cases {
		if got := appendWarning(c.existing, c.addition); got != c.want {
			t.Errorf("appendWarning(%q, %q) = %q, want %q", c.existing, c.addition, got, c.want)
		}
	}
}
