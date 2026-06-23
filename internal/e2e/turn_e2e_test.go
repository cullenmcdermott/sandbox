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
	"net/http"
	"net/http/httptest"
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

// fakeRunner implements the turns + events endpoints of the runner contract.
func fakeRunner(t *testing.T, sid session.ID, turn session.TurnID, events []session.Event) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/turns"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"turnId": string(turn)})

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events"):
			after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Errorf("ResponseWriter is not a Flusher")
				return
			}
			for _, ev := range events {
				if ev.Seq <= after { // honor after=<seq> replay
					continue
				}
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
			// End the stream so the client channel closes and the test can assert.

		default:
			http.NotFound(w, r)
		}
	}))
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
