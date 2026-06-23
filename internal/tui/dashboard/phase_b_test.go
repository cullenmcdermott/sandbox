package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// --------------------------------------------------------------------------
// ApplyRunnerEvent: SSE-event to six-state mapping tests
// --------------------------------------------------------------------------

func TestApplyRunnerEvent(t *testing.T) {
	tests := []struct {
		name          string
		initialStatus SessionStatus
		initialPermID string
		ev            session.Event
		wantStatus    SessionStatus
		wantChanged   bool
		// wantPermID and checkPermID: only asserted when checkPermID is true.
		wantPermID  string
		checkPermID bool
	}{
		{
			name:          "turn.started makes idle session busy",
			initialStatus: StatusIdle,
			ev:            mkEvent(session.EventTurnStarted, nil),
			wantStatus:    StatusBusy,
			wantChanged:   true,
		},
		{
			name:          "turn.started clears pending permission ID",
			initialStatus: StatusWaiting,
			initialPermID: "stale-id",
			ev:            mkEvent(session.EventTurnStarted, nil),
			wantStatus:    StatusBusy,
			wantChanged:   true,
			wantPermID:    "",
			checkPermID:   true,
		},
		{
			name:          "permission.requested makes busy session waiting and captures ID",
			initialStatus: StatusBusy,
			ev: mkEvent(session.EventPermissionRequested, session.PermissionPayload{
				PermissionID: "perm-abc-123",
				Tool:         "Bash",
			}),
			wantStatus:  StatusWaiting,
			wantChanged: true,
			wantPermID:  "perm-abc-123",
			checkPermID: true,
		},
		{
			name:          "permission.resolved returns waiting session to busy and clears ID",
			initialStatus: StatusWaiting,
			initialPermID: "perm-abc-123",
			ev:            mkEvent(session.EventPermissionResolved, nil),
			wantStatus:    StatusBusy,
			wantChanged:   true,
			wantPermID:    "",
			checkPermID:   true,
		},
		{
			name:          "turn.completed makes busy session needs-input",
			initialStatus: StatusBusy,
			ev:            mkEvent(session.EventTurnCompleted, nil),
			wantStatus:    StatusNeedsInput,
			wantChanged:   true,
		},
		{
			name:          "turn.interrupted makes busy session needs-input",
			initialStatus: StatusBusy,
			ev:            mkEvent(session.EventTurnInterrupted, nil),
			wantStatus:    StatusNeedsInput,
			wantChanged:   true,
		},
		{
			name:          "turn.failed makes busy session failed",
			initialStatus: StatusBusy,
			ev:            mkEvent(session.EventTurnFailed, nil),
			wantStatus:    StatusFailed,
			wantChanged:   true,
		},
		{
			name:          "message.delta is a no-op for status",
			initialStatus: StatusBusy,
			ev:            mkEvent(session.EventMessageDelta, nil),
			wantStatus:    StatusBusy,
			wantChanged:   false,
		},
		{
			name:          "tool.started is a no-op for status",
			initialStatus: StatusBusy,
			ev:            mkEvent(session.EventToolStarted, nil),
			wantStatus:    StatusBusy,
			wantChanged:   false,
		},
		{
			name:          "turn.started on already-busy session: no change (idempotent)",
			initialStatus: StatusBusy,
			ev:            mkEvent(session.EventTurnStarted, nil),
			wantStatus:    StatusBusy,
			wantChanged:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sess := Session{
				State:               session.State{ID: "test-session"},
				DashStatus:          tc.initialStatus,
				PendingPermissionID: tc.initialPermID,
			}
			changed := ApplyRunnerEvent(&sess, tc.ev)
			if changed != tc.wantChanged {
				t.Errorf("changed=%v, want %v", changed, tc.wantChanged)
			}
			if sess.DashStatus != tc.wantStatus {
				t.Errorf("DashStatus=%v, want %v", sess.DashStatus, tc.wantStatus)
			}
			if tc.checkPermID && sess.PendingPermissionID != tc.wantPermID {
				t.Errorf("PendingPermissionID=%q, want %q", sess.PendingPermissionID, tc.wantPermID)
			}
		})
	}
}

// mkEvent is a helper that constructs a session.Event with JSON-encoded payload.
func mkEvent(typ session.EventType, payload interface{}) session.Event {
	var raw json.RawMessage
	if payload != nil {
		b, _ := json.Marshal(payload)
		raw = b
	}
	return session.Event{Type: typ, Payload: raw}
}

// --------------------------------------------------------------------------
// Live-status reducer: patching one session in the read-model
// --------------------------------------------------------------------------

func TestApplyRunnerEventPatchesOneSession(t *testing.T) {
	m := New(nil) // nil backend -- driven manually
	m.sessions = []Session{
		{State: session.State{ID: "sess-a", Status: session.StatusRunning}, DashStatus: StatusIdle},
		{State: session.State{ID: "sess-b", Status: session.StatusRunning}, DashStatus: StatusIdle},
	}

	// Apply a turn.started event to sess-a only.
	msg := RunnerEventMsg{
		ID:    "sess-a",
		Event: mkEvent(session.EventTurnStarted, nil),
	}
	// Simulate what handleRunnerEvent does: find and patch.
	for i, s := range m.sessions {
		if s.ID() == msg.ID {
			ApplyRunnerEvent(&m.sessions[i], msg.Event)
			break
		}
	}

	// sess-a should now be busy; sess-b should be unchanged.
	for _, s := range m.sessions {
		switch s.ID() {
		case "sess-a":
			if s.DashStatus != StatusBusy {
				t.Errorf("sess-a: got %v, want StatusBusy", s.DashStatus)
			}
		case "sess-b":
			if s.DashStatus != StatusIdle {
				t.Errorf("sess-b: got %v, want StatusIdle (unchanged)", s.DashStatus)
			}
		}
	}
}

func TestPermissionIDCaptureAndClear(t *testing.T) {
	sess := Session{
		State:      session.State{ID: "sess-perm"},
		DashStatus: StatusBusy,
	}

	// A permission request sets the ID and tool.
	changed := ApplyRunnerEvent(&sess, mkEvent(session.EventPermissionRequested, session.PermissionPayload{
		PermissionID: "perm-xyz",
		Tool:         "Edit",
	}))
	if !changed || sess.DashStatus != StatusWaiting {
		t.Fatalf("permission.requested: status=%v changed=%v", sess.DashStatus, changed)
	}
	if sess.PendingPermissionID != "perm-xyz" {
		t.Errorf("PendingPermissionID=%q, want perm-xyz", sess.PendingPermissionID)
	}
	if sess.PendingPermissionTool != "Edit" {
		t.Errorf("PendingPermissionTool=%q, want Edit", sess.PendingPermissionTool)
	}

	// Resolving the permission clears the ID and returns to busy.
	ApplyRunnerEvent(&sess, mkEvent(session.EventPermissionResolved, nil))
	if sess.DashStatus != StatusBusy {
		t.Errorf("after resolve: status=%v, want StatusBusy", sess.DashStatus)
	}
	if sess.PendingPermissionID != "" {
		t.Errorf("PendingPermissionID should be cleared, got %q", sess.PendingPermissionID)
	}

	// Completing the turn clears and sets needs-input.
	ApplyRunnerEvent(&sess, mkEvent(session.EventTurnCompleted, nil))
	if sess.DashStatus != StatusNeedsInput {
		t.Errorf("after complete: status=%v, want StatusNeedsInput", sess.DashStatus)
	}
}

// --------------------------------------------------------------------------
// Graceful degradation when connector fails
// --------------------------------------------------------------------------

// failingConnector is a Connector that always returns an error.
func failingConnector(ctx context.Context, ref session.Ref, projectPath string, _ func(ConnectStage)) (ConnectResult, error) {
	return ConnectResult{}, errors.New("runner unreachable: connection refused")
}

func TestLiveSSEStartFailsDegrades(t *testing.T) {
	// Build a model with a failing connector.
	m := New(nil)
	m.connector = failingConnector
	m.sessions = []Session{
		{
			State:      session.State{ID: "sess-down", Status: session.StatusRunning, PodReady: true},
			DashStatus: StatusIdle,
		},
	}

	// Running startLiveSSECmd returns a tea.Cmd; calling it synchronously
	// should produce nil (failed connector degrades gracefully, no crash).
	cmd := m.startLiveSSECmd(m.sessions[0])
	if cmd == nil {
		t.Fatal("startLiveSSECmd returned nil before attempting connection")
	}
	msg := cmd()
	if msg != nil {
		t.Errorf("expected nil msg on connector failure (graceful degradation), got %T: %v", msg, msg)
	}

	// The session status must still be the cluster-derived baseline (idle).
	if m.sessions[0].DashStatus != StatusIdle {
		t.Errorf("DashStatus after failure: got %v, want StatusIdle (graceful degradation)", m.sessions[0].DashStatus)
	}
}

func TestRunnerEventStreamEndedRetries(t *testing.T) {
	// Build a model with sessions; simulate stream ending while session is busy.
	// With the RV1 fix, a mid-turn drop on a still-Running pod first schedules a
	// reconnect (status preserved) rather than flipping straight to 'failed'.
	m := New(nil)
	m.sessions = []Session{
		{
			State:      session.State{ID: "sess-drop", Status: session.StatusRunning},
			DashStatus: StatusBusy,
		},
	}
	// Manually mark an SSE stream as open (already closed channel).
	ch := make(chan session.Event)
	close(ch)
	m.liveSSEChannels["sess-drop"] = ch
	m.liveSSECancels["sess-drop"] = func() {} // no-op cancel

	// Process a StreamEnded message.
	next, cmd := m.handleRunnerEvent(RunnerEventMsg{ID: "sess-drop", StreamEnded: true})
	if cmd == nil {
		t.Error("expected a reconnect cmd on mid-turn stream drop (RV1 retry)")
	}
	_ = next

	// Session was busy on a still-Running pod → reconnect first, status preserved
	// (no immediate false 'failed').
	for _, s := range m.sessions {
		if s.ID() == "sess-drop" {
			if s.DashStatus != StatusBusy {
				t.Errorf("after first drop: DashStatus=%v, want StatusBusy preserved during retry", s.DashStatus)
			}
			if s.PendingPermissionID != "" {
				t.Errorf("PendingPermissionID should be cleared on stream end")
			}
		}
	}

	// The SSE cancel and channel maps should be cleaned up.
	if _, exists := m.liveSSECancels["sess-drop"]; exists {
		t.Error("liveSSECancels entry should be removed on stream end")
	}
	if _, exists := m.liveSSEChannels["sess-drop"]; exists {
		t.Error("liveSSEChannels entry should be removed on stream end")
	}
}

func TestRunnerEventNoConnectorSkipsSSE(t *testing.T) {
	// No connector configured: applySeed should not start SSE for running sessions.
	m := New(nil) // connector is nil by default
	states := []session.State{
		{ID: "sess-a", Status: session.StatusRunning, PodReady: true},
	}
	_, cmds := m.applySeed(states)

	// No SSE cmds should be returned when connector is nil.
	if len(cmds) != 0 {
		t.Errorf("expected 0 SSE cmds with nil connector, got %d", len(cmds))
	}
}

// --------------------------------------------------------------------------
// ApplyRunnerEvent: full state-machine coverage (table-driven)
// --------------------------------------------------------------------------

func TestApplyRunnerEventStateMachine(t *testing.T) {
	// Tests that every documented transition produces the expected status,
	// regardless of the starting state (where the transition is defined).
	transitions := []struct {
		from    SessionStatus
		evType  session.EventType
		to      SessionStatus
		changed bool
	}{
		// Turn lifecycle
		{StatusIdle, session.EventTurnStarted, StatusBusy, true},
		{StatusNeedsInput, session.EventTurnStarted, StatusBusy, true},
		{StatusBusy, session.EventTurnCompleted, StatusNeedsInput, true},
		{StatusBusy, session.EventTurnInterrupted, StatusNeedsInput, true},
		{StatusBusy, session.EventTurnFailed, StatusFailed, true},
		// Permission cycle
		{StatusBusy, session.EventPermissionRequested, StatusWaiting, true},
		{StatusWaiting, session.EventPermissionResolved, StatusBusy, true},
		// No-ops
		{StatusBusy, session.EventMessageDelta, StatusBusy, false},
		{StatusBusy, session.EventToolStarted, StatusBusy, false},
		{StatusBusy, session.EventToolCompleted, StatusBusy, false},
		{StatusBusy, session.EventUsageUpdated, StatusBusy, false},
		// Idempotent: already busy stays busy (changed=false)
		{StatusBusy, session.EventTurnStarted, StatusBusy, false},
	}

	for _, tc := range transitions {
		name := string(tc.evType) + "_from_" + tc.from.String()
		t.Run(name, func(t *testing.T) {
			sess := Session{
				State:      session.State{ID: "state-machine"},
				DashStatus: tc.from,
			}
			changed := ApplyRunnerEvent(&sess, mkEvent(tc.evType, nil))
			if sess.DashStatus != tc.to {
				t.Errorf("status: got %v, want %v", sess.DashStatus, tc.to)
			}
			if changed != tc.changed {
				t.Errorf("changed: got %v, want %v", changed, tc.changed)
			}
		})
	}
}

// --------------------------------------------------------------------------
// B2: Background SSE lifecycle during attach/detach
// --------------------------------------------------------------------------

// ORACLE: when attachReadyMsg arrives, the dashboard cancels its background SSE
// for that session — so at most one SSE client exists at a time (B2).
func TestAttachReadyCancelsDashboardSSE(t *testing.T) {
	sess := Session{
		State: session.State{ID: "sess-attach", Status: session.StatusRunning, PodReady: true},
	}

	app := NewApp(nil, nil, nil)
	app.dashboard.seeded = true
	app.dashboard.sessions = []Session{sess}

	// Manually seed a fake SSE cancel so we can detect it was called.
	cancelled := false
	app.dashboard.liveSSECancels["sess-attach"] = func() { cancelled = true }
	ch := make(chan session.Event)
	app.dashboard.liveSSEChannels["sess-attach"] = ch

	// Fire attachReadyMsg (no opencode creds → transcript path).
	_, _ = app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})

	if !cancelled {
		t.Error("attachReadyMsg should have cancelled the dashboard's background SSE for the attached session")
	}
	if _, exists := app.dashboard.liveSSECancels["sess-attach"]; exists {
		t.Error("liveSSECancels entry should be removed after cancelLiveSSE")
	}
}

// ORACLE: when the transcript is detached via detachMsg, startLiveSSECmd is
// called for the previously-attached session — so the dashboard regains live
// status coverage (B2). We can't observe the connector call without a real
// connector, so we verify that the returned Cmd is non-nil (it is set by
// startLiveSSECmd when the connector is non-nil).
func TestDetachMsgRestartsSSE(t *testing.T) {
	sseStarted := make(chan session.ID, 1)
	fakeConnector := func(ctx context.Context, ref session.Ref, projectPath string, _ func(ConnectStage)) (ConnectResult, error) {
		sseStarted <- ref.ID
		// Return a fake client with a working Events method so startLiveSSECmd
		// can build the liveSSEReadyMsg.
		ch := make(chan session.Event)
		close(ch) // closed channel → stream ends immediately; good enough
		return ConnectResult{Client: &fakeRunnerClient{events: ch}}, nil
	}

	sess := Session{
		State: session.State{ID: "sess-detach", Status: session.StatusRunning, PodReady: true},
	}

	app := NewApp(nil, fakeConnector, nil)
	app.dashboard.seeded = true
	app.dashboard.sessions = []Session{sess}
	// Wire up the connector on the dashboard model too so startLiveSSECmd works.
	app.dashboard.connector = fakeConnector

	// Pretend we're in transcript screen for sess-detach.
	fc := &fakeRunnerClient{}
	tr := NewTranscript(fc, sess, nil)
	app.transcript = tr
	app.screen = ScreenTranscript

	// Fire detachMsg.
	_, cmd := app.Update(detachMsg{})

	if app.transcript != nil {
		t.Error("detachMsg should clear a.transcript")
	}
	if app.screen != ScreenDashboard {
		t.Errorf("detachMsg should return to ScreenDashboard, got %v", app.screen)
	}
	if cmd == nil {
		t.Error("detachMsg should return a non-nil cmd (startLiveSSECmd)")
	}
}

// --------------------------------------------------------------------------
// B3: Transcript view/input state survives detach→reattach cycle
// --------------------------------------------------------------------------

// ORACLE: ParkedTranscriptState captures composeBuf, queuedPrompt, search query,
// and permMode from a TranscriptModel (B3).
func TestParkStateCaptures(t *testing.T) {
	sess := Session{State: session.State{ID: "s1"}}
	m := NewTranscript(&fakeRunnerClient{}, sess, nil)
	m.composeBuf = "draft text"
	m.composeCursor = 5
	m.queuedPrompt = "queued"
	m.search = searchModel{open: true, query: "foo bar"}
	m.mode = modeDefault

	ps := m.ParkState()
	if ps.composeBuf != "draft text" {
		t.Errorf("composeBuf: got %q, want %q", ps.composeBuf, "draft text")
	}
	if ps.composeCursor != 5 {
		t.Errorf("composeCursor: got %d, want 5", ps.composeCursor)
	}
	if ps.queuedPrompt != "queued" {
		t.Errorf("queuedPrompt: got %q, want %q", ps.queuedPrompt, "queued")
	}
	if ps.searchQuery != "foo bar" {
		t.Errorf("searchQuery: got %q, want %q", ps.searchQuery, "foo bar")
	}
	if ps.mode != modeDefault {
		t.Errorf("mode: got %v, want modeAskEveryTime", ps.mode)
	}
}

// ORACLE: RestoreParkedState applies the saved state to a fresh transcript (B3).
func TestRestoreParkedStateApplies(t *testing.T) {
	sess := Session{State: session.State{ID: "s1"}}
	m := NewTranscript(&fakeRunnerClient{}, sess, nil)

	ps := ParkedTranscriptState{
		composeBuf:    "restored draft",
		composeCursor: 7,
		queuedPrompt:  "restored queued",
		searchQuery:   "hello",
		mode:          modeDefault,
	}
	m.RestoreParkedState(ps)

	if m.composeBuf != "restored draft" {
		t.Errorf("composeBuf: got %q", m.composeBuf)
	}
	if m.composeCursor != 7 {
		t.Errorf("composeCursor: got %d", m.composeCursor)
	}
	if m.queuedPrompt != "restored queued" {
		t.Errorf("queuedPrompt: got %q", m.queuedPrompt)
	}
	if !m.search.open {
		t.Error("search should be open after restore with non-empty query")
	}
	if m.search.query != "hello" {
		t.Errorf("search.query: got %q", m.search.query)
	}
	if m.mode != modeDefault {
		t.Errorf("mode: got %v", m.mode)
	}
}

// ORACLE: after attachReadyMsg following a detachMsg for the same session, the
// new transcript has the compose buffer from the previous attach (B3).
func TestParkedStateRestoredOnReattach(t *testing.T) {
	sess := Session{
		State: session.State{ID: "sess-park", Status: session.StatusRunning, PodReady: true},
	}

	app := NewApp(nil, nil, nil)
	app.dashboard.seeded = true
	app.dashboard.sessions = []Session{sess}

	// Build a transcript with a compose buffer and simulate attaching.
	tr := NewTranscript(&fakeRunnerClient{}, sess, nil)
	tr.composeBuf = "half-typed message"
	tr.queuedPrompt = "queued msg"
	app.transcript = tr
	app.screen = ScreenTranscript

	// Detach: state should be parked.
	app.Update(detachMsg{})
	if app.transcript != nil {
		t.Fatal("transcript not cleared on detach")
	}
	if _, ok := app.parkedTranscripts["sess-park"]; !ok {
		t.Fatal("no parked state stored after detach")
	}

	// Re-attach: state should be restored.
	app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})
	if app.screen != ScreenTranscript {
		t.Fatalf("screen = %v after re-attach, want ScreenTranscript", app.screen)
	}
	if app.transcript == nil {
		t.Fatal("transcript nil after re-attach")
	}
	if app.transcript.composeBuf != "half-typed message" {
		t.Errorf("composeBuf after reattach: got %q, want %q", app.transcript.composeBuf, "half-typed message")
	}
	if app.transcript.queuedPrompt != "queued msg" {
		t.Errorf("queuedPrompt after reattach: got %q, want %q", app.transcript.queuedPrompt, "queued msg")
	}
	// Parked entry should be consumed after restore.
	if _, ok := app.parkedTranscripts["sess-park"]; ok {
		t.Error("parked state should be consumed after restore")
	}
}

// --------------------------------------------------------------------------
// B8: Previously-dropped event types are now handled
// --------------------------------------------------------------------------

func sendEvent(m *TranscriptModel, evType session.EventType, payload any) {
	raw, _ := json.Marshal(payload)
	m.handleEvent(session.Event{Type: evType, Payload: json.RawMessage(raw)})
}

// ORACLE: ReasoningStarted→ReasoningDelta→ReasoningCompleted produces a
// blockReasoning block in the transcript. [B8]
func TestReasoningEventsProduceBlock(t *testing.T) {
	sess := Session{State: session.State{ID: "r1"}}
	m := NewTranscript(&fakeRunnerClient{}, sess, nil)

	sendEvent(m, session.EventReasoningStarted, nil)
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "I should think about "})
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "this carefully."})
	sendEvent(m, session.EventReasoningCompleted, nil)

	var found bool
	for _, b := range m.blocks {
		if b.kind == blockReasoning {
			if !strings.Contains(b.text, "I should think about this carefully.") {
				t.Errorf("reasoning block text mismatch: got %q", b.text)
			}
			found = true
		}
	}
	if !found {
		t.Error("no blockReasoning block appended after ReasoningCompleted")
	}
	if m.reasoning {
		t.Error("m.reasoning should be false after ReasoningCompleted")
	}
}

// ORACLE: EventSessionStatusChanged updates m.status. [B8]
func TestSessionStatusChangedUpdatesStatus(t *testing.T) {
	sess := Session{State: session.State{ID: "s1"}}
	m := NewTranscript(&fakeRunnerClient{}, sess, nil)

	sendEvent(m, session.EventSessionStatusChanged, session.SessionStatusPayload{Status: "busy"})
	if m.status != StatusBusy {
		t.Errorf("status after busy: got %v, want StatusBusy", m.status)
	}

	sendEvent(m, session.EventSessionStatusChanged, session.SessionStatusPayload{Status: "idle"})
	if m.status != StatusNeedsInput {
		t.Errorf("status after idle: got %v, want StatusNeedsInput", m.status)
	}

	sendEvent(m, session.EventSessionStatusChanged, session.SessionStatusPayload{Status: "error"})
	if m.status != StatusFailed {
		t.Errorf("status after error: got %v, want StatusFailed", m.status)
	}
}

// ORACLE: tool.delta streams the tool's INPUT JSON (input_json_delta), not its
// output. It updates the newest running card's arg as the input materializes;
// the finalized tool.started overwrites arg with the parsed value. [B8/C2]
func TestToolDeltaUpdatesRunningCard(t *testing.T) {
	sess := Session{State: session.State{ID: "t1"}}
	m := NewTranscript(&fakeRunnerClient{}, sess, nil)

	// A tool starts (streaming content_block_start: input not yet known).
	m.startToolCard("Bash", "")
	if len(m.pendingTools) == 0 {
		t.Fatal("no pending tool after startToolCard")
	}
	idx := m.pendingTools[len(m.pendingTools)-1]

	// The tool's input JSON streams in as deltas.
	sendEvent(m, session.EventToolDelta, session.ToolPayload{PartialJSON: `{"command":`})
	sendEvent(m, session.EventToolDelta, session.ToolPayload{PartialJSON: `"go test"}`})
	if m.blocks[idx].tool.arg == "" {
		t.Error("tool.delta should have updated the running card arg with the streamed input")
	}
}

// ORACLE: TodoUpdated produces an info block — it is no longer silently
// dropped. [B8]
func TestInfoEventsPreviouslyDropped(t *testing.T) {
	sess := Session{State: session.State{ID: "i1"}}
	m := NewTranscript(&fakeRunnerClient{}, sess, nil)
	before := len(m.blocks)

	sendEvent(m, session.EventTodoUpdated, nil)

	added := len(m.blocks) - before
	if added != 1 {
		t.Errorf("expected 1 info block for todo event, got %d", added)
	}
	if m.blocks[before].kind != blockInfo {
		t.Errorf("expected blockInfo, got %v", m.blocks[before].kind)
	}
}

// --------------------------------------------------------------------------
// B9: Streaming tail preserved on mid-turn SSE drop
// --------------------------------------------------------------------------

// ORACLE: When SSE drops while m.streaming is true with buffered text,
// tStreamEndedMsg must call finalizeStreaming() so the partial text is
// committed to a blockAssistant block before reconnect. Without the fix,
// the buffer would be discarded when EventMessageStarted reset assistantBuf.
func TestStreamingTailPreservedOnSSEDrop(t *testing.T) {
	sess := Session{State: session.State{ID: "b9"}}
	m := NewTranscript(&fakeRunnerClient{}, sess, nil)

	// Simulate a turn starting and partial deltas arriving.
	sendEvent(m, session.EventMessageStarted, nil)
	sendEvent(m, session.EventMessageDelta, session.MessagePayload{Content: "partial response"})

	if !m.streaming {
		t.Fatal("expected m.streaming to be true after MessageDelta")
	}
	if m.assistantBuf.String() != "partial response" {
		t.Fatalf("buf = %q, want %q", m.assistantBuf.String(), "partial response")
	}
	blocksBefore := len(m.blocks)

	// Now simulate SSE drop (no finalizeStreaming from the turn end events).
	m.Update(tStreamEndedMsg{})

	// The partial text must have been committed to a block.
	var found bool
	for _, b := range m.blocks[blocksBefore:] {
		if b.kind == blockAssistant && strings.Contains(b.text, "partial response") {
			found = true
		}
	}
	if !found {
		t.Error("streaming tail lost on SSE drop: no blockAssistant with partial text added")
	}
	if m.streaming {
		t.Error("m.streaming should be false after tStreamEndedMsg")
	}
}
