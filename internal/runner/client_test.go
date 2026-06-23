package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
}

func TestHealthAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health with token: %v", err)
	}

	cNoToken := New(srv.URL, "")
	if err := cNoToken.Health(context.Background()); err == nil {
		t.Fatal("expected error without token")
	}
}

func TestStartTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/test/turns" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		var input session.TurnInput
		json.NewDecoder(r.Body).Decode(&input)
		if input.Prompt != "hello" {
			t.Errorf("prompt: got %q, want hello", input.Prompt)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"turnId":"turn-1"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	ref := session.Ref{ID: "test"}
	turn, err := c.StartTurn(context.Background(), ref, session.TurnInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	if turn.Turn != "turn-1" {
		t.Errorf("turn id: got %q, want turn-1", turn.Turn)
	}
}

func TestInterruptTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/test/turns/turn-1/interrupt" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	ref := session.Ref{ID: "test"}
	turn := session.TurnRef{Session: "test", Turn: "turn-1"}
	if err := c.InterruptTurn(context.Background(), ref, turn); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
}

func TestResolvePermission(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/test/permissions/perm-1" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	ref := session.Ref{ID: "test"}
	dec := session.PermissionDecision{
		Session:    "test",
		Permission: "perm-1",
		Allow:      true,
		Scope:      "once",
	}
	if err := c.ResolvePermission(context.Background(), ref, dec); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

func TestSessionState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/test/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"test","backend":"claude-sdk","projectPath":"/tmp","status":"idle"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	ref := session.Ref{ID: "test"}
	st, err := c.SessionState(context.Background(), ref)
	if err != nil {
		t.Fatalf("session state: %v", err)
	}
	if st.ID != "test" || st.Backend != "claude-sdk" {
		t.Errorf("got id=%q backend=%q", st.ID, st.Backend)
	}
}

func TestEventsSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/events") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// Send two events
		ev1 := session.Event{Seq: 1, Type: session.EventSessionStarted, SessionID: "test"}
		data1, _ := json.Marshal(ev1)
		fmt.Fprintf(w, "data: %s\n\n", data1)
		flusher.Flush()

		ev2 := session.Event{Seq: 2, Type: session.EventTurnStarted, SessionID: "test"}
		data2, _ := json.Marshal(ev2)
		fmt.Fprintf(w, "data: %s\n\n", data2)
		flusher.Flush()
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	ref := session.Ref{ID: "test"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := c.Events(ctx, ref, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	var collected []session.Event
	for ev := range events {
		collected = append(collected, ev)
		if len(collected) >= 2 {
			cancel()
			break
		}
	}

	if len(collected) != 2 {
		t.Fatalf("got %d events, want 2", len(collected))
	}
	if collected[0].Type != session.EventSessionStarted {
		t.Errorf("event 0 type: got %s, want session.started", collected[0].Type)
	}
	if collected[1].Seq != 2 {
		t.Errorf("event 1 seq: got %d, want 2", collected[1].Seq)
	}
}
