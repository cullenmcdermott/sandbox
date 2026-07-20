//go:build e2e

// pane_e2e_test.go drives the claude-pane seam end-to-end across the real
// CLI↔runner surface: the internal/runner WebSocket pane client
// (Client.AttachPane / PaneStream) plus the SSE events client, against an
// in-process fake pane runner that faithfully implements the slice of
// docs/runner-api.md the pane touches — the upgrade authorization order
// (404 path → 401 token → 404 id → 409 backend, all pre-upgrade), lazy child
// spawn on first attach, scrollback replay as one binary frame, JSON resize
// control frames, single-attacher preemption (4001), child-exit close (4002)
// with a synthetic turn-abort, observer-shaped normalized events on the SSE
// log, and the POST /turns 409 for a supervise-only backend.
//
// Like turn_e2e_test.go this is build-tagged `e2e` (run via `just e2e`) and
// binds localhost ports, so run with the command-sandbox disabled.
package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// fakePaneRunner is the in-process claude-pane runner: one session, one lazy
// "interactive child" (scripted command→output/event responses), a scrollback
// ring retained across attaches and child restarts, and an SSE event log fed
// by observer-shaped emissions. All state is mutex-guarded; the test reads it
// in-process where the wire contract has no readback (e.g. PTY geometry).
type fakePaneRunner struct {
	sid     session.ID
	token   string
	backend string // "claude-pane" (default) — set differently to exercise the 409

	mu         sync.Mutex
	scrollback []byte
	cur        *websocket.Conn
	spawned    bool
	cols, rows int
	events     []session.Event
	seq        uint64
	turnSeq    int
	turnOpen   bool
}

func newFakePaneRunner(sid session.ID, token string) *fakePaneRunner {
	return &fakePaneRunner{sid: sid, token: token, backend: session.BackendClaudePane}
}

// emit appends one normalized event to the log (append-before-stream: callers
// hold mu and append before any pane output is echoed, so once the test has
// read the echoed output the events are guaranteed present). turnID "" means
// no turn attribution.
func (f *fakePaneRunner) emit(turnID string, t session.EventType, payload any) {
	raw, _ := json.Marshal(payload)
	f.seq++
	f.events = append(f.events, session.Event{
		Seq:       f.seq,
		Time:      time.Now().UTC().Format(time.RFC3339),
		SessionID: f.sid,
		TurnID:    session.TurnID(turnID),
		Type:      t,
		Payload:   raw,
	})
}

// output records child output into the scrollback ring and forwards it to the
// attached socket (mirroring the supervisor's onData: ring first, then send).
func (f *fakePaneRunner) output(s string) {
	f.scrollback = append(f.scrollback, s...)
	if f.cur != nil {
		_ = f.cur.WriteMessage(websocket.BinaryMessage, []byte(s))
	}
}

// handleCommand is the scripted "interactive child". Each binary input frame
// is one command line (the real child gets raw keystrokes; a frame-per-line
// keeps the fake deterministic without a line-discipline emulation):
//
//	run <x> → a full observed turn: turn.started → message.delta →
//	          turn.completed, echoing progress into the pane
//	work    → opens a turn that never completes (crash-abort setup)
//	die     → the child exits: any open turn gets the supervisor's synthetic
//	          abort (turn.interrupted), the socket closes 4002, and the next
//	          attach "respawns" (scrollback retained, per the real ring)
func (f *fakePaneRunner) handleCommand(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(line, "run "):
		arg := strings.TrimPrefix(line, "run ")
		f.turnSeq++
		id := fmt.Sprintf("pane-turn-%d", f.turnSeq)
		f.emit(id, session.EventTurnStarted, map[string]any{"prompt": arg})
		f.emit(id, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "running " + arg, Delta: true})
		f.emit(id, session.EventTurnCompleted, session.MessagePayload{Role: "assistant", Content: "did " + arg})
		f.output("ran: " + arg + "\r\ndone(" + id + ")\r\n")
	case line == "work":
		f.turnSeq++
		id := fmt.Sprintf("pane-turn-%d", f.turnSeq)
		f.turnOpen = true
		f.emit(id, session.EventTurnStarted, map[string]any{"prompt": "work"})
		f.output("working…\r\n")
	case line == "die":
		if f.turnOpen {
			id := fmt.Sprintf("pane-turn-%d", f.turnSeq)
			f.emit(id, session.EventTurnInterrupted, map[string]any{"reason": "pane process exited (code=1 signal=null)"})
			f.turnOpen = false
		}
		f.spawned = false
		if f.cur != nil {
			msg := websocket.FormatCloseMessage(4002, "pane process exited")
			_ = f.cur.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
			_ = f.cur.Close()
			f.cur = nil
		}
	default:
		f.output("unknown: " + line + "\r\n")
	}
}

var paneUpgrader = websocket.Upgrader{}

// handler serves the faithful route surface: /healthz, the pane upgrade with
// the real authorization order, POST /turns (409 — supervise-only backend),
// and the SSE events replay. Unmodeled routes fail loud, like the turn fake.
func (f *fakePaneRunner) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/healthz" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "protocolVersion": session.ProtocolVersion})
			return
		}

		// Pane upgrade — authorization decided pre-upgrade as plain HTTP, in the
		// real order (server.ts evaluatePaneUpgrade): exact path 404, then token
		// 401 (BEFORE the id match, so a bad token cannot probe which id is
		// live), then id 404, then backend 409.
		if strings.HasSuffix(path, "/pane") && r.Method == http.MethodGet {
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/sessions/"), "/pane")
			if f.token != "" && r.Header.Get("Authorization") != "Bearer "+f.token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if session.ID(id) != f.sid {
				http.Error(w, "session not found", http.StatusNotFound)
				return
			}
			if f.backend != session.BackendClaudePane {
				http.Error(w, "backend "+f.backend+" has no interactive pane", http.StatusConflict)
				return
			}
			conn, err := paneUpgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			f.attach(conn)
			return
		}

		// Bearer auth on the remaining routes.
		if f.token != "" && r.Header.Get("Authorization") != "Bearer "+f.token {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// POST /turns: claude-pane is supervise-only — the 3.5 contract.
		if m := turnsPathRe.FindStringSubmatch(path); m != nil && r.Method == http.MethodPost {
			writeJSONErr(w, http.StatusConflict, fmt.Sprintf("backend %s does not accept runner turns", f.backend))
			return
		}

		// GET /events: replay the current log after `after`, then end the stream
		// (same shape the turn fake uses; the client channel closes so collect()
		// can assert).
		if r.Method == http.MethodGet && strings.HasSuffix(path, "/events") {
			var after uint64
			if raw := r.URL.Query().Get("after"); raw != "" {
				fmt.Sscanf(raw, "%d", &after)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			f.mu.Lock()
			evs := append([]session.Event(nil), f.events...)
			f.mu.Unlock()
			for _, ev := range evs {
				if ev.Seq <= after {
					continue
				}
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
			return
		}

		notImplemented(w, r)
	}
}

// attach mirrors the supervisor: preempt the current socket (4001), lazily
// "spawn" the child on the first-ever attach (banner output), replay the
// retained scrollback as ONE binary frame, then serve the read loop.
func (f *fakePaneRunner) attach(conn *websocket.Conn) {
	f.mu.Lock()
	if f.cur != nil && f.cur != conn {
		msg := websocket.FormatCloseMessage(4001, "replaced by a new pane attach")
		_ = f.cur.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
		_ = f.cur.Close()
	}
	f.cur = conn
	if !f.spawned {
		f.spawned = true
		// Spawn output lands in the ring BEFORE the snapshot send, so the very
		// first attach receives the banner as its replay frame too.
		f.scrollback = append(f.scrollback, "claude e2e fake ready\r\n> "...)
	}
	snap := append([]byte(nil), f.scrollback...)
	f.mu.Unlock()
	if len(snap) > 0 {
		_ = conn.WriteMessage(websocket.BinaryMessage, snap)
	}

	go func() {
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				// Identity check, like server.ts onGone: only the active socket
				// detaches on its own close; a preempted one must not clear the
				// newcomer.
				f.mu.Lock()
				if f.cur == conn {
					f.cur = nil
				}
				f.mu.Unlock()
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				f.handleCommand(strings.TrimRight(string(data), "\r\n"))
			case websocket.TextMessage:
				var ctl struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				// Malformed control frames are ignored, like parsePaneControl.
				if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" && ctl.Cols > 0 && ctl.Rows > 0 {
					f.mu.Lock()
					f.cols, f.rows = ctl.Cols, ctl.Rows
					f.mu.Unlock()
				}
			}
		}
	}()
}

func (f *fakePaneRunner) geometry() (cols, rows int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cols, f.rows
}

// readUntil reads the pane byte stream until the accumulated output contains
// want (the frame boundaries are not part of the contract), or fails the test.
func readUntil(t *testing.T, ps *runner.PaneStream, want string) string {
	t.Helper()
	var got strings.Builder
	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(got.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in pane output; got %q", want, got.String())
		}
		n, err := ps.Read(buf)
		if err != nil {
			t.Fatalf("pane read while waiting for %q: %v (got %q)", want, err, got.String())
		}
		got.Write(buf[:n])
	}
	return got.String()
}

// TestE2EPaneSmoke is the full claude-pane pass across the seam: attach (WS
// upgrade + initial resize + lazy spawn + replay), drive a scripted prompt,
// observe the observer-shaped turn events over SSE, detach, reattach with
// scrollback replay, and SSE replay from a cursor.
func TestE2EPaneSmoke(t *testing.T) {
	const sid = session.ID("pane-e2e")
	f := newFakePaneRunner(sid, "tok")
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	client := runner.New(srv.URL, "tok")
	ref := session.Ref{ID: sid}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Attach with an initial geometry: the runner spawns the child lazily
	// and replays the banner; the resize control frame lands server-side.
	ps, err := client.AttachPane(ctx, ref, 120, 40)
	if err != nil {
		t.Fatalf("AttachPane: %v", err)
	}
	readUntil(t, ps, "claude e2e fake ready")
	waitFor(t, func() bool { c, r := f.geometry(); return c == 120 && r == 40 }, "initial resize control frame")

	// 2. A later resize propagates too.
	if err := ps.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	waitFor(t, func() bool { c, r := f.geometry(); return c == 100 && r == 30 }, "resize control frame")

	// 3. Drive a scripted prompt through the pane; the echoed completion
	// proves the events were appended (append-before-echo in the fake).
	if _, err := ps.Write([]byte("run build\r")); err != nil {
		t.Fatalf("pane write: %v", err)
	}
	readUntil(t, ps, "done(pane-turn-1)")

	// 4. The normalized observer events are on the SSE log, in order.
	got := collect(t, mustEvents(t, ctx, client, ref, 0))
	wantTypes := []session.EventType{session.EventTurnStarted, session.EventMessageDelta, session.EventTurnCompleted}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count: got %d want %d (%v)", len(got), len(wantTypes), types(got))
	}
	for i, wt := range wantTypes {
		if got[i].Type != wt {
			t.Errorf("event %d: got %s want %s", i, got[i].Type, wt)
		}
	}
	var started struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(got[0].Payload, &started)
	if started.Prompt != "build" {
		t.Errorf("turn.started prompt: got %q want %q", started.Prompt, "build")
	}

	// 5. Detach (clean close): the child keeps running server-side.
	_ = ps.Close()
	waitFor(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.cur == nil }, "server observed the detach")
	f.mu.Lock()
	spawned := f.spawned
	f.mu.Unlock()
	if !spawned {
		t.Fatal("detach must leave the child running")
	}

	// 6. Reattach: the retained scrollback replays as the first frame —
	// banner AND the prior turn's output, without re-running anything.
	ps2, err := client.AttachPane(ctx, ref, 120, 40)
	if err != nil {
		t.Fatalf("re-AttachPane: %v", err)
	}
	replay := readUntil(t, ps2, "done(pane-turn-1)")
	if !strings.Contains(replay, "claude e2e fake ready") {
		t.Errorf("reattach replay missing the banner: %q", replay)
	}

	// 7. SSE replay from a cursor: after=1 yields exactly the later events.
	rep := collect(t, mustEvents(t, ctx, client, ref, 1))
	if len(rep) != 2 || rep[0].Seq != 2 || rep[1].Type != session.EventTurnCompleted {
		t.Errorf("after=1 replay: got %v", types(rep))
	}

	// 8. Programmatic turns stay rejected (the 3.5 contract).
	if _, err := client.StartTurn(ctx, ref, session.TurnInput{Prompt: "nope"}); err == nil || !strings.Contains(err.Error(), "does not accept runner turns") {
		t.Errorf("StartTurn against claude-pane: got %v, want the 409 supervise-only rejection", err)
	}
	_ = ps2.Close()
}

// TestE2EPanePreemption: a second attach preempts the first (4001 →
// ErrPanePreempted on the old stream) and receives the replay itself.
func TestE2EPanePreemption(t *testing.T) {
	const sid = session.ID("pane-e2e")
	f := newFakePaneRunner(sid, "tok")
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	client := runner.New(srv.URL, "tok")
	ref := session.Ref{ID: sid}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ps1, err := client.AttachPane(ctx, ref, 80, 24)
	if err != nil {
		t.Fatalf("attach 1: %v", err)
	}
	readUntil(t, ps1, "ready")

	ps2, err := client.AttachPane(ctx, ref, 80, 24)
	if err != nil {
		t.Fatalf("attach 2: %v", err)
	}
	readUntil(t, ps2, "ready") // the newcomer gets the replay

	// The preempted stream's read loop ends with the sentinel.
	buf := make([]byte, 256)
	var rerr error
	for rerr == nil {
		_, rerr = ps1.Read(buf)
	}
	if !errors.Is(rerr, runner.ErrPanePreempted) {
		t.Errorf("preempted read error: got %v, want ErrPanePreempted", rerr)
	}
	_ = ps2.Close()
}

// TestE2EPaneChildExit: a child death mid-turn closes the socket with 4002
// (ErrPaneChildExited) and the log carries the supervisor's synthetic abort;
// the next attach "respawns" with the scrollback intact.
func TestE2EPaneChildExit(t *testing.T) {
	const sid = session.ID("pane-e2e")
	f := newFakePaneRunner(sid, "tok")
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	client := runner.New(srv.URL, "tok")
	ref := session.Ref{ID: sid}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ps, err := client.AttachPane(ctx, ref, 80, 24)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	readUntil(t, ps, "ready")
	if _, err := ps.Write([]byte("work\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	readUntil(t, ps, "working…")
	if _, err := ps.Write([]byte("die\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 256)
	var rerr error
	for rerr == nil {
		_, rerr = ps.Read(buf)
	}
	if !errors.Is(rerr, runner.ErrPaneChildExited) {
		t.Fatalf("read after child exit: got %v, want ErrPaneChildExited", rerr)
	}

	// The synthetic abort is in the log (turn.started then turn.interrupted).
	got := collect(t, mustEvents(t, ctx, client, ref, 0))
	if len(got) != 2 || got[0].Type != session.EventTurnStarted || got[1].Type != session.EventTurnInterrupted {
		t.Fatalf("child-exit log: got %v, want [turn.started turn.interrupted]", types(got))
	}
	var abort struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(got[1].Payload, &abort)
	if !strings.Contains(abort.Reason, "pane process exited") {
		t.Errorf("synthetic abort reason: got %q", abort.Reason)
	}

	// Reattach after the exit: respawn path, retained scrollback replays.
	ps2, err := client.AttachPane(ctx, ref, 80, 24)
	if err != nil {
		t.Fatalf("reattach after exit: %v", err)
	}
	readUntil(t, ps2, "working…")
	_ = ps2.Close()
}

// TestE2EPaneUpgradeRejections pins the pre-upgrade authorization statuses in
// the real order: bad token 401 (before the id match), wrong id 404, non-pane
// backend 409 — all surfaced through the real client as status errors.
func TestE2EPaneUpgradeRejections(t *testing.T) {
	const sid = session.ID("pane-e2e")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("bad token → 401 even with a wrong id", func(t *testing.T) {
		f := newFakePaneRunner(sid, "tok")
		srv := httptest.NewServer(f.handler(t))
		t.Cleanup(srv.Close)
		bad := runner.New(srv.URL, "wrong")
		_, err := bad.AttachPane(ctx, session.Ref{ID: "other"}, 0, 0)
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatalf("got %v, want a 401 status error (token checked before the id)", err)
		}
	})

	t.Run("wrong id → 404", func(t *testing.T) {
		f := newFakePaneRunner(sid, "tok")
		srv := httptest.NewServer(f.handler(t))
		t.Cleanup(srv.Close)
		c := runner.New(srv.URL, "tok")
		_, err := c.AttachPane(ctx, session.Ref{ID: "other"}, 0, 0)
		if err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("got %v, want a 404 status error", err)
		}
	})

	t.Run("non-pane backend → 409", func(t *testing.T) {
		f := newFakePaneRunner(sid, "tok")
		f.backend = session.BackendOpenCode
		srv := httptest.NewServer(f.handler(t))
		t.Cleanup(srv.Close)
		c := runner.New(srv.URL, "tok")
		_, err := c.AttachPane(ctx, session.Ref{ID: sid}, 0, 0)
		if err == nil || !strings.Contains(err.Error(), "409") {
			t.Fatalf("got %v, want a 409 status error", err)
		}
	})
}

// --- small helpers ----------------------------------------------------------

func mustEvents(t *testing.T, ctx context.Context, c *runner.Client, ref session.Ref, after uint64) <-chan session.Event {
	t.Helper()
	ch, err := c.Events(ctx, ref, after)
	if err != nil {
		t.Fatalf("Events(after=%d): %v", after, err)
	}
	return ch
}

func types(evs []session.Event) []session.EventType {
	out := make([]session.EventType, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type
	}
	return out
}

// waitFor polls cond (used for server-side state the wire has no readback for,
// e.g. the recorded PTY geometry) with a bounded deadline.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
