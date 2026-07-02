package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
func TestProtocolVersionWarningMismatch(t *testing.T) {
	rc := healthServer(t, session.ProtocolVersion+1)
	w := protocolVersionWarning(rc)
	if w == "" {
		t.Fatal("protocolVersionWarning with mismatched version = \"\", want a non-empty warning")
	}
}

// TestProtocolVersionWarningMissing: an old runner image that predates the
// protocolVersion field (Health decodes it to 0) must also warn — a missing
// field is skew, not "assume compatible".
func TestProtocolVersionWarningMissing(t *testing.T) {
	rc := healthServer(t, 0)
	w := protocolVersionWarning(rc)
	if w == "" {
		t.Fatal("protocolVersionWarning with no reported version = \"\", want a non-empty warning")
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
