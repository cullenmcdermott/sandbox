package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestArmAutopilot covers PUT /sessions/:id/autopilot: the request body mirrors
// AutopilotRequest (camelCase wire names), the bearer token is sent, and the
// 200 /status body is decoded — including the capabilities.autopilot bit.
func TestArmAutopilot(t *testing.T) {
	budget := int64(100000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/test/autopilot" || r.Method != http.MethodPut {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("auth header: got %q, want Bearer token", got)
		}
		// The runner reads camelCase wire names; assert the body round-trips.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode arm body: %v", err)
		}
		if body["kind"] != "loop" || body["prompt"] != "keep going" {
			t.Errorf("arm body kind/prompt: %+v", body)
		}
		if body["intervalMs"] != float64(5000) {
			t.Errorf("arm body intervalMs: got %v, want 5000", body["intervalMs"])
		}
		if body["maxIterations"] != float64(50) {
			t.Errorf("arm body maxIterations: got %v, want 50", body["maxIterations"])
		}
		ov, _ := body["overrides"].(map[string]any)
		if ov["model"] != "opus" || ov["effort"] != "high" || ov["mode"] != "acceptEdits" {
			t.Errorf("arm body overrides: %+v", ov)
		}
		if body["tokenBudget"] != float64(100000) {
			t.Errorf("arm body tokenBudget: got %v, want 100000", body["tokenBudget"])
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"id":"test","backend":"claude-sdk","status":"busy","capabilities":{"autopilot":true}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	st, err := c.ArmAutopilot(context.Background(), session.Ref{ID: "test"}, session.AutopilotRequest{
		Kind:          session.AutopilotKindLoop,
		Prompt:        "keep going",
		Sentinel:      "ALL_DONE",
		IntervalMs:    5000,
		Overrides:     session.AutopilotOverrides{Model: "opus", Effort: "high", Mode: "acceptEdits"},
		MaxIterations: 50,
		TokenBudget:   &budget,
	})
	if err != nil {
		t.Fatalf("arm: %v", err)
	}
	if !st.Capabilities.Autopilot {
		t.Errorf("capabilities.autopilot not parsed: %+v", st.Capabilities)
	}
}

// TestArmAutopilotUnsupported: a backend without a runner driver answers 409,
// mapped to ErrAutopilotUnsupported so the CLI can fall back to its local driver.
func TestArmAutopilotUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintf(w, `{"error":"backend has no autopilot driver"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	_, err := c.ArmAutopilot(context.Background(), session.Ref{ID: "test"}, session.AutopilotRequest{
		Kind: session.AutopilotKindLoop, Prompt: "x",
	})
	if !errors.Is(err, ErrAutopilotUnsupported) {
		t.Fatalf("arm 409: got %v, want ErrAutopilotUnsupported", err)
	}
}

// TestArmAutopilotBadRequest: a 400 (invalid field) is surfaced verbatim, not
// mapped to a sentinel, so the typed runner message reaches the user.
func TestArmAutopilotBadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"kind must be 'loop' or 'goal'"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	_, err := c.ArmAutopilot(context.Background(), session.Ref{ID: "test"}, session.AutopilotRequest{Kind: "bogus", Prompt: "x"})
	if err == nil {
		t.Fatal("arm 400: want error")
	}
	if errors.Is(err, ErrAutopilotUnsupported) || errors.Is(err, ErrAutopilotNotArmed) {
		t.Errorf("arm 400 must not map to a sentinel: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "kind must be") {
		t.Errorf("arm 400 should surface the server message, got %q", got)
	}
}

// TestDisarmAutopilot covers DELETE /sessions/:id/autopilot: it uses DELETE and
// decodes the /status body.
func TestDisarmAutopilot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/test/autopilot" || r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"id":"test","status":"idle","capabilities":{"autopilot":true}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	if _, err := c.DisarmAutopilot(context.Background(), session.Ref{ID: "test"}); err != nil {
		t.Fatalf("disarm: %v", err)
	}
}

// TestDisarmAutopilotNotArmed: disarming a never-armed session answers 404,
// mapped to ErrAutopilotNotArmed so an idempotent caller can treat it as success.
func TestDisarmAutopilotNotArmed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error":"no autopilot spec to disarm"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	_, err := c.DisarmAutopilot(context.Background(), session.Ref{ID: "test"})
	if !errors.Is(err, ErrAutopilotNotArmed) {
		t.Fatalf("disarm 404: got %v, want ErrAutopilotNotArmed", err)
	}
}

// TestSessionStateCapabilities: the capability bit is parsed from GET /status
// (the read the TUI uses to pick the autopilot code path).
func TestSessionStateCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"test","backend":"claude-sdk","status":"idle","capabilities":{"autopilot":true}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	st, err := c.SessionState(context.Background(), session.Ref{ID: "test"})
	if err != nil {
		t.Fatalf("session state: %v", err)
	}
	if !st.Capabilities.Autopilot {
		t.Errorf("capabilities.autopilot: got false, want true")
	}
}
