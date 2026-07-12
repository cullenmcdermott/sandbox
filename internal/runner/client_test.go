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

// TestHealthProtocolVersion covers the CLI/runner protocol-version handshake
// (internal/runner.Client.Health / ProtocolVersion): a runner that reports its
// protocolVersion on /healthz must have that value observable afterward.
func TestHealthProtocolVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "protocolVersion": session.ProtocolVersion + 1})
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")
	if got := c.ProtocolVersion(); got != 0 {
		t.Fatalf("ProtocolVersion before any Health call = %d, want 0", got)
	}
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	if got, want := c.ProtocolVersion(), session.ProtocolVersion+1; got != want {
		t.Errorf("ProtocolVersion after Health = %d, want %d (mismatch not detected)", got, want)
	}
}

// TestHealthProtocolVersionMissing covers an old runner image that predates
// the protocolVersion field: it must decode to 0 (treated as "unknown/old" by
// callers), not fail Health outright (the runner is genuinely up).
func TestHealthProtocolVersionMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	if got := c.ProtocolVersion(); got != 0 {
		t.Errorf("ProtocolVersion with no protocolVersion field = %d, want 0", got)
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

func TestExec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/test/exec" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Command string `json:"command"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Command != "git status" {
			t.Errorf("command: got %q, want git status", body.Command)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"stdout":"clean\n","stderr":"","exitCode":0}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	res, err := c.Exec(context.Background(), session.Ref{ID: "test"}, "git status")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Stdout != "clean\n" || res.ExitCode != 0 {
		t.Errorf("exec result: %+v", res)
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
		// §8 wire contract: the runner reports turn activity on "activity" (NOT the
		// lifecycle "status") and the backend resume id on "agentSession".
		fmt.Fprintf(w, `{"id":"test","backend":"claude-sdk","projectPath":"/tmp","activity":"idle","agentSession":"sdk-uuid"}`)
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
	if st.Activity != session.ActivityIdle {
		t.Errorf("activity: got %q, want idle", st.Activity)
	}
	if st.AgentSessionID != "sdk-uuid" {
		t.Errorf("agentSession: got %q, want sdk-uuid", st.AgentSessionID)
	}
	// The runner does not report the lifecycle Status; it must stay empty.
	if st.Status != "" {
		t.Errorf("status: got %q, want empty (runner does not report lifecycle status)", st.Status)
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

// RV6: EventsPassive must request the stream with ?passive=1 (so the runner does
// not count it as an attached client for idle detection), while the active
// Events must NOT set the flag.
func TestEventsPassiveQueryParam(t *testing.T) {
	gotQuery := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/events") {
			http.NotFound(w, r)
			return
		}
		gotQuery <- r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	ref := session.Ref{ID: "test"}

	ctx1, cancel1 := context.WithCancel(context.Background())
	if _, err := c.Events(ctx1, ref, 0); err != nil {
		t.Fatalf("Events: %v", err)
	}
	activeQ := <-gotQuery
	cancel1()
	if strings.Contains(activeQ, "passive") {
		t.Errorf("active Events query = %q, must not contain passive", activeQ)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	if _, err := c.EventsPassive(ctx2, ref, 0); err != nil {
		t.Fatalf("EventsPassive: %v", err)
	}
	passiveQ := <-gotQuery
	cancel2()
	if !strings.Contains(passiveQ, "passive=1") {
		t.Errorf("EventsPassive query = %q, must contain passive=1", passiveQ)
	}
}

// errResponse configures how the test server replies for an error-path case.
type errResponse struct {
	status int
	body   string
	ctype  string // Content-Type; defaults to application/json when body is set
}

// serverFor returns a server that always responds with the given errResponse,
// regardless of path. Each error-path test owns its single call.
func serverFor(r errResponse) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if r.ctype != "" {
			w.Header().Set("Content-Type", r.ctype)
		}
		w.WriteHeader(r.status)
		if r.body != "" {
			fmt.Fprint(w, r.body)
		}
	}))
}

// invoke exercises one client method by name against the given client, returning
// the error (if any). It lets the table drive every method uniformly.
func invoke(c *Client, method string) error {
	ctx := context.Background()
	ref := session.Ref{ID: "test"}
	switch method {
	case "Health":
		return c.Health(ctx)
	case "StartTurn":
		_, err := c.StartTurn(ctx, ref, session.TurnInput{Prompt: "hi"})
		return err
	case "InterruptTurn":
		return c.InterruptTurn(ctx, ref, session.TurnRef{Session: "test", Turn: "turn-1"})
	case "ResolvePermission":
		return c.ResolvePermission(ctx, ref, session.PermissionDecision{Session: "test", Permission: "perm-1", Allow: true, Scope: "once"})
	case "SessionState":
		_, err := c.SessionState(ctx, ref)
		return err
	case "Exec":
		_, err := c.Exec(ctx, ref, "git status")
		return err
	case "Idle":
		_, err := c.Idle(ctx, ref)
		return err
	default:
		panic("unknown method " + method)
	}
}

// opPrefix is the error-message prefix each method uses (see statusError calls
// in client.go). Used to assert the right operation is named in the error.
var opPrefix = map[string]string{
	"Health":            "runner health",
	"StartTurn":         "runner start turn",
	"InterruptTurn":     "runner interrupt turn",
	"ResolvePermission": "runner resolve permission",
	"SessionState":      "runner session state",
	"Exec":              "runner exec",
	"Idle":              "runner idle",
}

// TestErrorPathStatusSurfaced asserts that every method turns a non-2xx response
// into an error that names the operation, the status code, and — when the runner
// sends a {"error":...} body — the server's message. This is the regression
// guard for the opaque "status 409"/"status 404" bug reports.
func TestErrorPathStatusSurfaced(t *testing.T) {
	methods := []string{"Health", "StartTurn", "InterruptTurn", "ResolvePermission", "SessionState", "Exec", "Idle"}

	cases := []struct {
		name        string
		resp        errResponse
		wantStatus  string // substring that must appear (status code)
		wantMessage string // substring that must appear (server message), "" if none
	}{
		{
			name:        "json error body",
			resp:        errResponse{status: http.StatusConflict, body: `{"error":"turn already running"}`},
			wantStatus:  "409",
			wantMessage: "turn already running",
		},
		{
			name:       "no body",
			resp:       errResponse{status: http.StatusNotFound},
			wantStatus: "404",
		},
		{
			name:        "plain text body",
			resp:        errResponse{status: http.StatusBadGateway, body: "upstream is down", ctype: "text/plain"},
			wantStatus:  "502",
			wantMessage: "upstream is down",
		},
	}

	for _, m := range methods {
		for _, tc := range cases {
			t.Run(m+"/"+tc.name, func(t *testing.T) {
				srv := serverFor(tc.resp)
				defer srv.Close()
				c := New(srv.URL, "token")

				err := invoke(c, m)
				if err == nil {
					t.Fatalf("%s: expected error for status %d, got nil", m, tc.resp.status)
				}
				msg := err.Error()
				if !strings.Contains(msg, opPrefix[m]) {
					t.Errorf("%s: error %q missing op prefix %q", m, msg, opPrefix[m])
				}
				if !strings.Contains(msg, tc.wantStatus) {
					t.Errorf("%s: error %q missing status %q", m, msg, tc.wantStatus)
				}
				if tc.wantMessage != "" && !strings.Contains(msg, tc.wantMessage) {
					t.Errorf("%s: error %q does not surface server message %q", m, msg, tc.wantMessage)
				}
			})
		}
	}
}

// TestErrorPathMalformedSuccessBody asserts that a 2xx response with a body the
// client cannot decode (truncated/garbage JSON) is reported as a decode error
// rather than being silently swallowed. Only methods that decode a response
// body are covered (Health/InterruptTurn/ResolvePermission have no body to
// decode).
func TestErrorPathMalformedSuccessBody(t *testing.T) {
	methods := []string{"StartTurn", "SessionState", "Exec", "Idle"}
	for _, m := range methods {
		t.Run(m, func(t *testing.T) {
			srv := serverFor(errResponse{status: http.StatusOK, body: `{not json`})
			defer srv.Close()
			c := New(srv.URL, "token")

			err := invoke(c, m)
			if err == nil {
				t.Fatalf("%s: expected decode error for malformed body, got nil", m)
			}
			if !strings.Contains(err.Error(), opPrefix[m]) {
				t.Errorf("%s: decode error %q missing op prefix %q", m, err.Error(), opPrefix[m])
			}
		})
	}
}

// TestEventsErrorStatusSurfaced asserts the SSE open path also folds the
// server's error body into the returned error (the connect path can't decode a
// body into events, so a clear error is the only signal the caller gets).
func TestEventsErrorStatusSurfaced(t *testing.T) {
	srv := serverFor(errResponse{status: http.StatusServiceUnavailable, body: `{"error":"session suspended"}`})
	defer srv.Close()
	c := New(srv.URL, "token")

	_, err := c.Events(context.Background(), session.Ref{ID: "test"}, 0)
	if err == nil {
		t.Fatal("expected error from Events on 503, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "runner events") || !strings.Contains(msg, "503") || !strings.Contains(msg, "session suspended") {
		t.Errorf("Events error %q missing op/status/message", msg)
	}
}
