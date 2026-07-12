//go:build e2e

// Package e2e drives a full turn across the real CLI↔runner seam (the
// internal/runner HTTP+SSE client) against an in-process fake runner that
// faithfully implements the slice of docs/runner-api.md the client touches.
// Unlike the unit tests in internal/runner (which check one method each), this
// asserts the whole normalized event sequence a real turn produces, plus the
// after=<seq> replay contract used on reconnect.
//
// Build-tagged `e2e` so the default `go test ./...` stays fast; run via
// `just e2e` (included in `just check`). httptest binds a localhost port, so run
// with the command-sandbox disabled (see CLAUDE.md).
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// scriptedTurn is the canonical normalized event sequence a simple turn emits:
// turn.started → message.* → tool.* → turn.completed. Seqs are 1-based and
// contiguous so the replay assertion can index by seq.
func scriptedTurn(sid session.ID, turn session.TurnID) []session.Event {
	mk := func(seq uint64, t session.EventType, payload any) session.Event {
		raw, _ := json.Marshal(payload)
		return session.Event{
			Seq:       seq,
			Time:      "2026-06-22T00:00:0" + strconv.FormatUint(seq, 10) + "Z",
			SessionID: sid,
			TurnID:    turn,
			Type:      t,
			Payload:   raw,
		}
	}
	exit := 0
	return []session.Event{
		mk(1, session.EventTurnStarted, map[string]any{"prompt": "list the files"}),
		mk(2, session.EventMessageStarted, session.MessagePayload{Role: "assistant", Content: ""}),
		mk(3, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "I'll list", Delta: true}),
		mk(4, session.EventMessageCompleted, session.MessagePayload{Role: "assistant", Content: "I'll list them."}),
		mk(5, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "tu_1", Input: json.RawMessage(`{"command":"ls"}`)}),
		mk(6, session.EventToolCompleted, session.ToolPayload{Tool: "Bash", ToolUseID: "tu_1", Output: "a.go\nb.go", ExitCode: &exit}),
		mk(7, session.EventTurnCompleted, session.MessagePayload{Role: "assistant", Content: "Done."}),
	}
}

// maxBodyBytes mirrors the runner's 1 MiB request-body cap (httputil.ts).
const maxBodyBytes = 1 << 20

// fakeOpts configures the fake runner's faithfulness knobs. The zero value is a
// claude-sdk backend with no auth and no active turn — the happy-path setup the
// full-turn and replay tests use.
type fakeOpts struct {
	sid     session.ID
	turn    session.TurnID
	events  []session.Event
	token   string // if non-empty, non-/healthz requests must carry `Bearer <token>` or 401
	backend string // "claude-sdk" (default), "opencode-server", "supervise-only"
	busy    bool   // opencode-server synthetic-busy (status:busy, no registered turn) → 409
	active  bool   // a registered runner turn is already active → 409 (R4 gate)
}

// writeJSONErr writes the runner's canonical `{"error": "..."}` body at `status`.
func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// notImplemented is the LOUD stub for routes the real server serves but this fake
// deliberately does not model (status/idle/permissions/exec/autopilot/interrupt/
// list). It returns 501 with a distinctive, greppable body so a future test that
// starts exercising such a route fails visibly instead of silently reading a
// wrong-but-plausible 404 — the whole point of the [F4] faithfulness residual.
func notImplemented(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": fmt.Sprintf("e2e fake runner does not implement %s %s; see runner/src/server.ts", r.Method, r.URL.Path),
		"fake":  "e2e",
	})
}

// eventsHead returns the max seq in the scripted log (the fake's `lastSeq`).
func eventsHead(events []session.Event) uint64 {
	var head uint64
	for _, ev := range events {
		if ev.Seq > head {
			head = ev.Seq
		}
	}
	return head
}

var turnsPathRe = regexp.MustCompile(`^/sessions/([^/]+)/turns$`)

// fakeRunnerHandler builds the faithful router. It mirrors the SHAPES the real
// runner (runner/src/server.ts) returns for the slice of routes the CLI's HTTP
// client touches — status codes, `{"error":...}` bodies, the POST /turns 409
// turnRejectReason set, SSE `after=` replay + the B5 over-head clamp, the auth
// 401 shape, and the 413 body — so the build-tagged e2e can't drift green against
// a runner whose contract has moved. Cross-checked against
// runner/test/server-http.test.ts, the F4 real-server suite.
//
// Not modeled (out of the fake's scope; each fails loud via notImplemented rather
// than lying): the seq-0 persist-failure delivery bypass (events.ts — the fake
// never fails a persist, so it has no seq-0 events), delta compaction, heartbeats,
// and the `: replay-complete` boundary comment (the client doesn't require it).
func fakeRunnerHandler(t *testing.T, o fakeOpts) http.HandlerFunc {
	t.Helper()
	backend := o.backend
	if backend == "" {
		backend = "claude-sdk"
	}
	head := eventsHead(o.events)
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// healthz: unauthenticated, mirrors healthzBody().
		if path == "/healthz" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "protocolVersion": session.ProtocolVersion})
			return
		}

		// Bearer auth on every other route (real: authOk before dispatch).
		if o.token != "" && r.Header.Get("Authorization") != "Bearer "+o.token {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// POST /sessions/:id/turns
		if m := turnsPathRe.FindStringSubmatch(path); m != nil && r.Method == http.MethodPost {
			if session.ID(m[1]) != o.sid {
				writeJSONErr(w, http.StatusNotFound, "session not found")
				return
			}
			// supervise-only (no Agent) short-circuits before the body read.
			if backend == "supervise-only" {
				writeJSONErr(w, http.StatusConflict, fmt.Sprintf("backend %s does not accept runner turns", backend))
				return
			}
			// 1 MiB body cap → 413, mirroring readBody/BodyTooLargeError.
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
			if err == nil && len(body) > maxBodyBytes {
				writeJSONErr(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			var req struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSONErr(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			if req.Prompt == "" {
				writeJSONErr(w, http.StatusBadRequest, "prompt is required")
				return
			}
			// turnRejectReason 409 gate (runner/src/turns.ts), verbatim messages.
			if o.active {
				writeJSONErr(w, http.StatusConflict, "a turn is already active; interrupt it before starting a new one")
				return
			}
			if backend == "opencode-server" && o.busy {
				writeJSONErr(w, http.StatusConflict, "the opencode session is busy; interrupt the active turn before starting a new one")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"turnId": string(o.turn)})
			return
		}

		// GET /sessions/:id/events (SSE)
		if r.Method == http.MethodGet && strings.HasSuffix(path, "/events") {
			q := r.URL.Query()
			afterSeq := head // absent/empty `after` resumes from head (new events only)
			if raw := q.Get("after"); raw != "" {
				n, err := strconv.ParseInt(raw, 10, 64)
				if err != nil || n < 0 { // R8: non-integer / negative → 400
					writeJSONErr(w, http.StatusBadRequest, "after must be a non-negative integer")
					return
				}
				// B5: clamp a cursor beyond the head down to the head so live
				// events aren't silently swallowed.
				if n > int64(head) {
					afterSeq = head
				} else {
					afterSeq = uint64(n)
				}
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Errorf("ResponseWriter is not a Flusher")
				return
			}
			for _, ev := range o.events {
				if ev.Seq <= afterSeq {
					continue
				}
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
			// End the stream so the client channel closes and the test can assert.
			return
		}

		// Routes the real server serves but the fake doesn't model → LOUD 501.
		if strings.HasPrefix(path, "/sessions") {
			notImplemented(w, r)
			return
		}
		// Genuinely-unknown path → 404 `{"error":"not found"}` (real notFound()).
		writeJSONErr(w, http.StatusNotFound, "not found")
	}
}

// fakeRunner boots the faithful router on an httptest server with the happy-path
// defaults (claude-sdk, no auth, no active turn) used by the full-turn + replay
// tests.
func fakeRunner(t *testing.T, sid session.ID, turn session.TurnID, events []session.Event) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(fakeRunnerHandler(t, fakeOpts{sid: sid, turn: turn, events: events}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, ch <-chan session.Event) []session.Event {
	t.Helper()
	var got []session.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("timed out collecting events (got %d)", len(got))
		}
	}
}

func TestE2EFullTurnSequence(t *testing.T) {
	const sid, turn = session.ID("sess-e2e"), session.TurnID("turn-1")
	want := scriptedTurn(sid, turn)
	srv := fakeRunner(t, sid, turn, want)

	client := runner.New(srv.URL, "")
	ref := session.Ref{ID: sid}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Start a turn over the real HTTP path.
	tref, err := client.StartTurn(ctx, ref, session.TurnInput{Prompt: "list the files"})
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if tref.Turn != turn {
		t.Fatalf("StartTurn turn id: got %q want %q", tref.Turn, turn)
	}

	// 2. Consume the SSE stream through the real parser.
	ch, err := client.Events(ctx, ref, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	got := collect(t, ch)

	// 3. The normalized type sequence must match exactly.
	wantTypes := []session.EventType{
		session.EventTurnStarted, session.EventMessageStarted, session.EventMessageDelta,
		session.EventMessageCompleted, session.EventToolStarted, session.EventToolCompleted,
		session.EventTurnCompleted,
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count: got %d want %d", len(got), len(wantTypes))
	}
	for i, wt := range wantTypes {
		if got[i].Type != wt {
			t.Errorf("event %d type: got %s want %s", i, got[i].Type, wt)
		}
		if got[i].Seq != uint64(i+1) {
			t.Errorf("event %d seq: got %d want %d", i, got[i].Seq, i+1)
		}
	}

	// 4. Payloads decode against the generated/validated structs.
	var tool session.ToolPayload
	if err := json.Unmarshal(got[5].Payload, &tool); err != nil {
		t.Fatalf("decode tool.completed payload: %v", err)
	}
	if tool.Tool != "Bash" || tool.Output != "a.go\nb.go" {
		t.Errorf("tool payload: got %+v", tool)
	}
	if tool.ExitCode == nil || *tool.ExitCode != 0 {
		t.Errorf("tool exitCode: got %v want 0", tool.ExitCode)
	}
}

func TestE2EReplayAfterSeq(t *testing.T) {
	const sid, turn = session.ID("sess-e2e"), session.TurnID("turn-1")
	srv := fakeRunner(t, sid, turn, scriptedTurn(sid, turn))

	client := runner.New(srv.URL, "")
	ref := session.Ref{ID: sid}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Reconnect from seq 4: must replay only events 5,6,7 (the tool + completion).
	ch, err := client.Events(ctx, ref, 4)
	if err != nil {
		t.Fatalf("Events(after=4): %v", err)
	}
	got := collect(t, ch)

	if len(got) != 3 {
		t.Fatalf("replay count: got %d want 3", len(got))
	}
	if got[0].Seq != 5 {
		t.Errorf("first replayed seq: got %d want 5", got[0].Seq)
	}
	if got[len(got)-1].Type != session.EventTurnCompleted {
		t.Errorf("last replayed type: got %s want turn.completed", got[len(got)-1].Type)
	}
}

// TestE2EFakeRunnerFaithfulness pins the fake runner's route→status/body shapes
// against the real server (runner/src/server.ts, mirrored by the F4 suite
// runner/test/server-http.test.ts) so the build-tagged e2e can't quietly drift
// green while the runner contract moves. It drives the fake with a raw HTTP
// client (not the runner client) so it can send the malformed/oversized/wrong-
// token/gate-tripping requests the happy-path client never issues.
func TestE2EFakeRunnerFaithfulness(t *testing.T) {
	const sid, turn = session.ID("sess-e2e"), session.TurnID("turn-1")
	events := scriptedTurn(sid, turn) // head seq = 7

	// do issues one buffered request; the fake ends every response (SSE included)
	// so ReadAll never blocks.
	do := func(srv *httptest.Server, method, path, token, body string) (int, string) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rdr)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	// errorField decodes the runner's `{"error":...}` body.
	errorField := func(body string) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			return ""
		}
		if s, ok := m["error"].(string); ok {
			return s
		}
		return ""
	}

	t.Run("healthz reports the protocol version, unauthenticated", func(t *testing.T) {
		srv := httptest.NewServer(fakeRunnerHandler(t, fakeOpts{sid: sid, turn: turn, events: events, token: "tok"}))
		defer srv.Close()
		status, body := do(srv, http.MethodGet, "/healthz", "", "") // no token
		if status != http.StatusOK {
			t.Fatalf("healthz status: got %d want 200", status)
		}
		var h struct {
			Status          string `json:"status"`
			ProtocolVersion int    `json:"protocolVersion"`
		}
		if err := json.Unmarshal([]byte(body), &h); err != nil {
			t.Fatalf("healthz decode: %v (%s)", err, body)
		}
		if h.Status != "ok" || h.ProtocolVersion != session.ProtocolVersion {
			t.Errorf("healthz body: got %+v want {ok %d}", h, session.ProtocolVersion)
		}
	})

	t.Run("auth 401 shape precedes route dispatch", func(t *testing.T) {
		srv := httptest.NewServer(fakeRunnerHandler(t, fakeOpts{sid: sid, turn: turn, events: events, token: "tok"}))
		defer srv.Close()
		// No token on a route that would otherwise 501 → auth wins with 401.
		status, body := do(srv, http.MethodGet, "/sessions/"+string(sid)+"/status", "", "")
		if status != http.StatusUnauthorized {
			t.Fatalf("no-token status: got %d want 401 (body=%s)", status, body)
		}
		if got := errorField(body); got != "unauthorized" {
			t.Errorf("401 error field: got %q want %q", got, "unauthorized")
		}
	})

	// Table of turns/events/unknown-route pins. status → expected code; wantErr is
	// a substring the `{"error":...}` field must contain ("" for the SSE 200s).
	cases := []struct {
		name       string
		opts       fakeOpts
		method     string
		path       string
		body       string
		wantStatus int
		wantErr    string
	}{
		{"turns wrong session → 404 session not found", fakeOpts{sid: sid, turn: turn, events: events}, http.MethodPost, "/sessions/other/turns", `{"prompt":"hi"}`, http.StatusNotFound, "session not found"},
		{"turns supervise-only → 409 does not accept", fakeOpts{sid: sid, turn: turn, events: events, backend: "supervise-only"}, http.MethodPost, "/sessions/" + string(sid) + "/turns", `{"prompt":"hi"}`, http.StatusConflict, "does not accept runner turns"},
		{"turns missing prompt → 400", fakeOpts{sid: sid, turn: turn, events: events}, http.MethodPost, "/sessions/" + string(sid) + "/turns", `{"notPrompt":1}`, http.StatusBadRequest, "prompt is required"},
		{"turns invalid JSON → 400", fakeOpts{sid: sid, turn: turn, events: events}, http.MethodPost, "/sessions/" + string(sid) + "/turns", `{not json`, http.StatusBadRequest, "invalid JSON body"},
		{"turns active-turn gate → 409", fakeOpts{sid: sid, turn: turn, events: events, active: true}, http.MethodPost, "/sessions/" + string(sid) + "/turns", `{"prompt":"hi"}`, http.StatusConflict, "a turn is already active"},
		{"turns opencode-busy gate → 409", fakeOpts{sid: sid, turn: turn, events: events, backend: "opencode-server", busy: true}, http.MethodPost, "/sessions/" + string(sid) + "/turns", `{"prompt":"hi"}`, http.StatusConflict, "opencode session is busy"},
		{"events non-integer after → 400", fakeOpts{sid: sid, turn: turn, events: events}, http.MethodGet, "/sessions/" + string(sid) + "/events?after=abc", "", http.StatusBadRequest, "non-negative integer"},
		{"events negative after → 400", fakeOpts{sid: sid, turn: turn, events: events}, http.MethodGet, "/sessions/" + string(sid) + "/events?after=-5", "", http.StatusBadRequest, "non-negative integer"},
		{"unimplemented real route → 501 loud", fakeOpts{sid: sid, turn: turn, events: events}, http.MethodGet, "/sessions/" + string(sid) + "/idle", "", http.StatusNotImplemented, "does not implement"},
		{"unknown route → 404 not found", fakeOpts{sid: sid, turn: turn, events: events}, http.MethodGet, "/nope", "", http.StatusNotFound, "not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(fakeRunnerHandler(t, tc.opts))
			defer srv.Close()
			status, body := do(srv, tc.method, tc.path, "", tc.body)
			if status != tc.wantStatus {
				t.Fatalf("status: got %d want %d (body=%s)", status, tc.wantStatus, body)
			}
			if tc.wantErr != "" && !strings.Contains(errorField(body), tc.wantErr) {
				t.Errorf("error body: got %q want substring %q", errorField(body), tc.wantErr)
			}
		})
	}

	t.Run("413 oversized body reaches the client", func(t *testing.T) {
		srv := httptest.NewServer(fakeRunnerHandler(t, fakeOpts{sid: sid, turn: turn, events: events}))
		defer srv.Close()
		big := `{"prompt":"` + strings.Repeat("x", maxBodyBytes+1024) + `"}`
		status, body := do(srv, http.MethodPost, "/sessions/"+string(sid)+"/turns", "", big)
		if status != http.StatusRequestEntityTooLarge {
			t.Fatalf("413 status: got %d want 413", status)
		}
		if got := errorField(body); got != "request body too large" {
			t.Errorf("413 error field: got %q want %q", got, "request body too large")
		}
	})

	t.Run("B5 clamp: after beyond head yields no replay, stream still 200", func(t *testing.T) {
		srv := httptest.NewServer(fakeRunnerHandler(t, fakeOpts{sid: sid, turn: turn, events: events}))
		defer srv.Close()
		status, body := do(srv, http.MethodGet, "/sessions/"+string(sid)+"/events?after=999", "", "")
		if status != http.StatusOK {
			t.Fatalf("clamp status: got %d want 200", status)
		}
		if strings.Contains(body, "data: ") {
			t.Errorf("clamp: expected no replayed events beyond head, got: %q", body)
		}
	})
}
