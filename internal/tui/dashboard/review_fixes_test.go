package dashboard

// review_fixes_test.go — regression tests for the 2026-07 verified-findings
// batch: seq dedup on background re-ingest, queued-prompt preservation in
// parked models, group-view selection mapping, and connect-cancel staleness.

import (
	"context"
	"errors"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// HIGH-1: handleEvent must dedupe on seq. After a detach, the dashboard's
// passive stream (EventsPassive from the dashboard's own, staler cursor)
// re-feeds events the retained model already rendered while foreground; those
// replays must not duplicate transcript blocks.
func TestIngestDedupesSeqsAlreadySeenForeground(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	events := []session.Event{
		mkEventSeq(1, session.EventTurnStarted, nil),
		mkEventSeq(2, session.EventMessageStarted, map[string]any{}),
		mkEventSeq(3, session.EventMessageCompleted, map[string]any{"content": "hello there"}),
		mkEventSeq(4, session.EventTurnCompleted, nil),
	}
	// Foreground stream renders them once.
	for _, ev := range events {
		m.handleEvent(ev)
	}
	want := len(m.blocks)
	if want == 0 {
		t.Fatal("setup: foreground events produced no blocks")
	}
	if m.lastSeq != 4 {
		t.Fatalf("setup: lastSeq = %d, want 4", m.lastSeq)
	}

	// Detach: the background feed replays the same seqs into the retained model.
	for _, ev := range events {
		m.ingest(ev)
	}
	if got := len(m.blocks); got != want {
		t.Fatalf("background replay duplicated blocks: %d, want %d", got, want)
	}
	// New events past the cursor still apply.
	m.ingest(mkEventSeq(5, session.EventMessageCompleted, map[string]any{"content": "fresh"}))
	if got := len(m.blocks); got != want+1 {
		t.Fatalf("post-cursor event dropped: blocks = %d, want %d", got, want+1)
	}
}

// HIGH-2: a parked (background) model must keep its queued prompt when a
// background turn.completed arrives via ingest — ingest discards Cmds, so a
// flush there would mutate state (phantom busy) while the startTurnCmd is
// thrown away and the prompt silently lost.
func TestParkedModelKeepsQueuedPromptOnBackgroundTurnCompleted(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.turnActive = true
	m.queuedPrompt = "send me later"
	m.events = nil // parked/background: no live stream owned by this model

	m.ingest(mkEventSeq(7, session.EventTurnCompleted, nil))

	if m.queuedPrompt != "send me later" {
		t.Fatalf("background turn.completed lost the queued prompt: %q", m.queuedPrompt)
	}
	if m.turnActive {
		t.Fatal("background turn.completed left a phantom active turn")
	}
	if len(fc.startedPrompts) != 0 {
		t.Fatalf("background flush POSTed a turn whose Cmd should not run: %v", fc.startedPrompts)
	}
}

// HIGH-3: in group view the cursor indexes display rows (headers included), so
// selection/rename/archive must map through the same row accessor the renderer
// uses — not index visibleSessions with a row cursor.
func TestGroupViewSelectionMatchesCursorRow(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/r/alpha"}),
		SessionFromState(session.State{ID: "s2", Status: session.StatusRunning, ProjectPath: "/r/alpha"}),
		SessionFromState(session.State{ID: "s3", Status: session.StatusRunning, ProjectPath: "/r/beta"}),
	}
	m.toggleGroupView()
	if !m.groupView.open {
		t.Fatal("setup: group view did not open")
	}
	// Rows: [hdr alpha, s1, s2, hdr beta, s3]
	rows := m.visibleRows()
	if len(rows) != 5 {
		t.Fatalf("setup: rows = %d, want 5 (2 headers + 3 sessions)", len(rows))
	}

	// Cursor on a session row below a header: selection must be that row.
	m.cursor = 2 // s2 (old code returned visibleSessions[2] == s3)
	if sel := m.selectedSession(); sel == nil || sel.ID() != "s2" {
		t.Fatalf("cursor on row 2 selected %v, want s2", sel)
	}
	m.cursor = 4 // s3, below the second header (old code: index out of range → nil)
	if sel := m.selectedSession(); sel == nil || sel.ID() != "s3" {
		t.Fatalf("cursor on row 4 selected %v, want s3", sel)
	}

	// clampCursor must clamp against rows, not sessions: row 4 is valid.
	m.clampCursor()
	if m.cursor != 4 {
		t.Fatalf("clampCursor moved a valid row cursor: %d, want 4", m.cursor)
	}

	// Rename routes to the highlighted session (s3), not visibleSessions[cursor].
	m.openRename()
	if !m.renaming {
		t.Fatal("openRename did not enter rename mode")
	}
	m.renameBuf = "renamed-beta"
	m.commitRename()
	for _, s := range m.sessions {
		if s.ID() == "s3" && s.RenamedTitle != "renamed-beta" {
			t.Fatalf("rename missed s3: RenamedTitle = %q", s.RenamedTitle)
		}
		if s.ID() != "s3" && s.RenamedTitle != "" {
			t.Fatalf("rename hit the wrong session %s: %q", s.ID(), s.RenamedTitle)
		}
	}

	// Header row: toggleRepoGroup collapses the header's own group.
	m.cursor = 3 // "beta" header
	m.toggleRepoGroup()
	if m.groupView.repos["beta"] {
		t.Fatal("toggleRepoGroup on the beta header did not collapse beta")
	}
	if !m.groupView.repos["alpha"] {
		t.Fatal("toggleRepoGroup collapsed the wrong group (alpha)")
	}
}

// MEDIUM-7: connectUpdateMsg values from a cancelled connect attempt are stale
// and must be dropped — a trailing "context canceled" failure must not surface
// as an error, and a trailing ready must not attach.
func TestConnectCancelDropsStaleUpdates(t *testing.T) {
	connectorEntered := make(chan struct{})
	connector := func(ctx context.Context, ref session.Ref, projectPath string, onStage func(ConnectStage, string)) (ConnectResult, error) {
		close(connectorEntered)
		<-ctx.Done() // park until the user cancels
		return ConnectResult{}, ctx.Err()
	}
	app := NewApp(nil, connector, nil)
	app.width, app.height = 100, 40

	sess := Session{State: session.State{ID: "c1", Backend: "claude-sdk", ProjectPath: "/p"}, Title: "p"}
	_, cmd := app.Update(attachMsg{sess: sess})
	if app.screen != ScreenConnecting {
		t.Fatalf("screen = %v, want ScreenConnecting", app.screen)
	}
	if cmd == nil {
		t.Fatal("attachMsg produced no connect command")
	}
	ch := app.connectCh
	<-connectorEntered

	// Any key cancels the in-flight connect and returns to the dashboard.
	app.Update(keyMsg("esc"))
	if app.screen != ScreenDashboard {
		t.Fatalf("cancel: screen = %v, want ScreenDashboard", app.screen)
	}

	// The cancelled goroutine still emits its terminal failure; the in-flight
	// drain would deliver it. It must be dropped as stale — no error surfaces.
	stale, ok := <-ch
	if !ok {
		t.Fatal("expected the cancelled connect to emit a terminal update")
	}
	if stale.failed == nil || !errors.Is(stale.failed.err, context.Canceled) {
		t.Fatalf("expected a context.Canceled failure, got %+v", stale)
	}
	app.Update(stale)
	if app.connectErr != nil {
		t.Fatalf("stale cancel failure surfaced as an error: %v", app.connectErr)
	}
	if app.dashboard.connectErr != nil {
		t.Fatalf("stale cancel failure reached the dashboard: %v", app.dashboard.connectErr)
	}
	if app.screen != ScreenDashboard {
		t.Fatalf("stale failure flipped the screen: %v", app.screen)
	}

	// A stale ready (from a replaced attempt) must not attach either.
	staleReady := connectUpdateMsg{
		gen:   app.connectGen - 1,
		ready: &attachReadyMsg{sess: sess, client: &fakeRunnerClient{}},
	}
	app.Update(staleReady)
	if app.screen != ScreenDashboard || app.transcript != nil {
		t.Fatalf("stale ready attached: screen=%v transcript=%v", app.screen, app.transcript)
	}
}
