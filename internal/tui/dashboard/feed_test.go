package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// feedEvent builds a normalized event for the feed reducer at a given seq.
func feedEvent(seq uint64, t session.EventType, payload any) session.Event {
	raw, _ := json.Marshal(payload)
	return session.Event{Seq: seq, Type: t, Payload: raw}
}

// feedTexts returns each entry's kind+text for structural assertions.
func feedKinds(m *feedModel) []feedKind {
	out := make([]feedKind, len(m.items))
	for i, it := range m.items {
		out[i] = it.kind
	}
	return out
}

// TestFeedReducesATurn: a full turn (prompt → streamed reply → tool → complete)
// yields a user entry, one assistant entry (deltas coalesced then finalized to
// the authoritative text), and a one-line tool entry with its result.
func TestFeedReducesATurn(t *testing.T) {
	m := newFeedModel(session.Ref{ID: "s"}, "s", "claude")
	m.SetSize(80, 20)

	m.ingest(feedEvent(1, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "fix the build"}))
	m.ingest(feedEvent(2, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "On ", Delta: true}))
	m.ingest(feedEvent(3, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "it", Delta: true}))
	m.ingest(feedEvent(4, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "t1", Input: json.RawMessage(`{"command":"go build ./..."}`)}))
	zero := 0
	m.ingest(feedEvent(5, session.EventToolCompleted, session.ToolPayload{Tool: "Bash", ToolUseID: "t1", Output: "ok", ExitCode: &zero}))
	m.ingest(feedEvent(6, session.EventMessageCompleted, session.MessagePayload{Role: "assistant", Content: "On it — done."}))
	m.ingest(feedEvent(7, session.EventTurnCompleted, session.TurnCompletedPayload{}))

	kinds := feedKinds(m)
	want := []feedKind{feedUser, feedAssistant, feedTool}
	if len(kinds) != len(want) {
		t.Fatalf("feed entries = %v, want kinds %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("entry %d kind = %d, want %d (all: %v)", i, kinds[i], want[i], kinds)
		}
	}
	// Assistant finalized to the authoritative text, streaming cursor cleared.
	if m.items[1].text != "On it — done." || m.items[1].streaming {
		t.Errorf("assistant entry = %q streaming=%v, want finalized text", m.items[1].text, m.items[1].streaming)
	}
	// Tool folded its result into the single line.
	if got := m.items[2].text; !strings.Contains(got, "Bash — go build ./...") {
		t.Errorf("tool entry = %q, want a one-line Bash summary", got)
	}
}

// TestFeedStreamingCursorWhileMidTurn: before finalization the assistant entry
// carries the streaming cursor and the coalesced text.
func TestFeedStreamingCursorWhileMidTurn(t *testing.T) {
	m := newFeedModel(session.Ref{ID: "s"}, "s", "claude")
	m.SetSize(80, 20)
	m.ingest(feedEvent(1, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "hi"}))
	m.ingest(feedEvent(2, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "thinking", Delta: true}))
	if m.stream == nil || !m.stream.streaming || m.stream.text != "thinking" {
		t.Fatalf("mid-stream assistant entry = %+v, want streaming 'thinking'", m.stream)
	}
	if !strings.Contains(m.stream.Render(80), "▍") {
		t.Errorf("streaming entry should render a trailing cursor: %q", m.stream.Render(80))
	}
}

// TestFeedIgnoresSubagentStreams: a subagent's parented message/tool events stay
// out of the monitor (they would interleave with the main reply).
func TestFeedIgnoresSubagentStreams(t *testing.T) {
	m := newFeedModel(session.Ref{ID: "s"}, "s", "claude")
	m.SetSize(80, 20)
	m.ingest(feedEvent(1, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "go"}))
	m.ingest(feedEvent(2, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "child", Delta: true, ParentToolUseID: "task1"}))
	m.ingest(feedEvent(3, session.EventToolStarted, session.ToolPayload{Tool: "Read", ParentToolUseID: "task1", Input: json.RawMessage(`{"file_path":"/a/b.go"}`)}))
	if k := feedKinds(m); len(k) != 1 || k[0] != feedUser {
		t.Fatalf("subagent-parented events leaked into the feed: %v", k)
	}
}

// TestFeedInterruptNotice: a synthetic turn-abort renders as a calm sentence
// appended after the interrupted content, with no bracketed debug tags.
func TestFeedInterruptNotice(t *testing.T) {
	m := newFeedModel(session.Ref{ID: "s"}, "s", "claude")
	m.SetSize(80, 20)
	m.ingest(feedEvent(1, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "work"}))
	m.ingest(feedEvent(2, session.EventTurnInterrupted, session.TurnInterruptedPayload{Reason: "pane process exited"}))
	last := m.items[len(m.items)-1]
	if last.kind != feedNotice {
		t.Fatalf("last entry kind = %d, want feedNotice", last.kind)
	}
	if !strings.Contains(last.text, "Turn interrupted — pane process exited.") {
		t.Errorf("interrupt notice = %q", last.text)
	}
	// The entry TEXT (what the user reads, before ANSI styling) is a plain
	// sentence with no bracketed debug tags.
	if strings.ContainsAny(last.text, "[]") {
		t.Errorf("notice must read as a calm sentence, no brackets: %q", last.text)
	}
}

// TestFeedSeqDedup: a replayed event (seq at or below the cursor) is ignored, so
// a reconnect's after=lastSeq catch-up can't double-append.
func TestFeedSeqDedup(t *testing.T) {
	m := newFeedModel(session.Ref{ID: "s"}, "s", "claude")
	m.SetSize(80, 20)
	m.ingest(feedEvent(3, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "one"}))
	m.ingest(feedEvent(2, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "stale"})) // replay
	m.ingest(feedEvent(3, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "dup"}))   // dup
	if k := feedKinds(m); len(k) != 1 {
		t.Fatalf("seq dedup failed: %d entries, want 1", len(k))
	}
	if m.items[0].text != "one" {
		t.Errorf("kept the wrong entry: %q", m.items[0].text)
	}
}

// TestFeedScrollbarTransient: at the bottom the gutter is blank; scrolled up a
// thumb appears (the transient-scrollbar contract).
func TestFeedScrollbarTransient(t *testing.T) {
	m := newFeedModel(session.Ref{ID: "s"}, "s", "claude")
	m.SetSize(40, 6) // small body so content overflows
	for i := uint64(1); i <= 20; i++ {
		m.ingest(feedEvent(i, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "prompt line"}))
		m.ingest(feedEvent(100+i, session.EventTurnCompleted, session.TurnCompletedPayload{}))
	}
	m.bottom()
	if strings.Contains(m.bodyView(), "█") {
		t.Errorf("at the bottom the scrollbar thumb must be hidden:\n%s", m.bodyView())
	}
	m.scroll(-5)
	// After scrolling up, the thumb should appear somewhere in the gutter.
	// (kit.Scrollbar renders a block thumb; assert the body grew a gutter column.)
	up := m.bodyView()
	if lipglossWidthFirstLine(up) <= 40-1 {
		t.Errorf("scrolled-up feed should show a scrollbar gutter column:\n%s", up)
	}
}

// lipglossWidthFirstLine returns the display width of the first rendered line.
func lipglossWidthFirstLine(s string) int {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return len([]rune(strings.TrimRight(s, " ")))
}

// TestGoldenFeed snapshots a representative feed (prompt + reply + two tools +
// a calm notice) so unintended visual changes fail. Regenerate with -update.
func TestGoldenFeed(t *testing.T) {
	withDeterministicRender(t, func() {
		m := newFeedModel(session.Ref{ID: "alpha"}, "alpha", "claude")
		m.SetSize(100, 24)
		m.ingest(feedEvent(1, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "add a health endpoint and run the tests"}))
		m.ingest(feedEvent(2, session.EventMessageCompleted, session.MessagePayload{Role: "assistant", Content: "I'll add the endpoint and run the suite."}))
		m.ingest(feedEvent(3, session.EventToolStarted, session.ToolPayload{Tool: "Edit", ToolUseID: "t1", Input: json.RawMessage(`{"file_path":"/work/alpha/server/health.go"}`)}))
		m.ingest(feedEvent(4, session.EventToolCompleted, session.ToolPayload{Tool: "Edit", ToolUseID: "t1", Output: "updated"}))
		zero := 0
		m.ingest(feedEvent(5, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "t2", Input: json.RawMessage(`{"command":"go test ./..."}`)}))
		m.ingest(feedEvent(6, session.EventToolCompleted, session.ToolPayload{Tool: "Bash", ToolUseID: "t2", Output: "ok\nok\nok", ExitCode: &zero}))
		m.ingest(feedEvent(7, session.EventMessageCompleted, session.MessagePayload{Role: "assistant", Content: "Done — the tests pass."}))
		m.ingest(feedEvent(8, session.EventTurnCompleted, session.TurnCompletedPayload{}))
		m.bottom()
		golden.RequireEqual(t, []byte(m.View()))
	})
}

// TestAppViewFeedNavigation drives the feed through the App: v opens the feed
// for an external-pane session, live events flow via the tap, esc returns to
// the dashboard, and enter attaches the pane. Proves the feed is read-only
// (a printable key does nothing) and renders for opencode too (5.3).
func TestAppViewFeedNavigation(t *testing.T) {
	app := NewApp(nil, nil, nil)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	// Two external-pane backends prove the feed is backend-agnostic (5.3).
	for _, backend := range []string{session.BackendClaudePane, session.BackendOpenCode} {
		t.Run(backend, func(t *testing.T) {
			sess := Session{State: session.State{ID: session.ID("s-" + backend), Backend: backend}, AutoTitle: "watch me"}

			// v → feed. Opening kicks off the one-shot history fetch (L3), so
			// the feed is in the awaiting state until its result arrives.
			app.Update(viewFeedMsg{sess: sess})
			if app.screen != ScreenFeed || app.feed == nil {
				t.Fatalf("viewFeedMsg did not open the feed (screen=%v feed=%v)", app.screen, app.feed)
			}
			if !app.feedAwaitingHistory {
				t.Fatal("opening the feed must await the history fetch")
			}
			if app.windowTitle() != "watch me" {
				t.Errorf("feed window title = %q, want the session title", app.windowTitle())
			}

			// Complete the history handshake (empty history: a fresh session).
			app.Update(feedHistoryMsg{id: sess.ID(), gen: app.feedHistoryGen, complete: true})

			// A live event on the matching stream reaches the feed via the tap.
			app.Update(RunnerEventBatchMsg{ID: sess.ID(), Events: []session.Event{
				feedEvent(1, session.EventTurnStarted, session.TurnStartedPayload{Prompt: "do the thing"}),
			}})
			if len(app.feed.items) != 1 || app.feed.items[0].kind != feedUser {
				t.Fatalf("live tap did not reach the feed: %v", feedKinds(app.feed))
			}

			// Read-only: a printable key submits nothing and changes no state.
			before := len(app.feed.items)
			app.Update(keyMsg("x"))
			if app.screen != ScreenFeed || len(app.feed.items) != before {
				t.Errorf("feed must be read-only; a printable key changed state")
			}

			// esc → back to the dashboard.
			app.Update(keyMsg("esc"))
			if app.screen != ScreenDashboard || app.feed != nil {
				t.Fatalf("esc did not leave the feed (screen=%v)", app.screen)
			}

			// Re-open, then enter attaches the pane (feed's one session action).
			app.Update(viewFeedMsg{sess: sess})
			_, cmd := app.Update(keyMsg("enter"))
			if app.feed != nil {
				t.Error("attach from feed should release the feed model")
			}
			if cmd == nil {
				t.Error("enter on the feed should emit an attach command")
			}
		})
	}
}
