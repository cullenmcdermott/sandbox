package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/list"
)

func transcriptSession() Session {
	return Session{
		State:      session.State{ID: "s1", ProjectPath: "/x/proj", Backend: "claude-sdk"},
		Title:      "proj",
		DashStatus: StatusNeedsInput,
	}
}

// --------------------------------------------------------------------------
// Diff stat extraction
// --------------------------------------------------------------------------

func TestPermissionDiffStat(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		input    string
		adds     int
		dels     int
		numLines int
	}{
		// Minimal LCS diff: "a"→"a\nb" keeps "a" as context and adds only "b".
		{"edit", "Edit", `{"old_string":"a","new_string":"a\nb"}`, 1, 0, 2},
		{"write", "Write", `{"content":"l1\nl2\nl3"}`, 3, 0, 3},
		// edit1: +b (a is context); edit2: x\ny→z is −x −y +z.
		{"multiedit", "MultiEdit", `{"edits":[{"old_string":"a","new_string":"a\nb"},{"old_string":"x\ny","new_string":"z"}]}`, 2, 2, 5},
		{"non-edit", "Bash", `{"command":"ls"}`, 0, 0, 0},
		{"malformed", "Edit", `not json`, 0, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adds, dels, lines := permissionDiffStat(tc.tool, json.RawMessage(tc.input))
			if adds != tc.adds || dels != tc.dels {
				t.Errorf("got +%d −%d, want +%d −%d", adds, dels, tc.adds, tc.dels)
			}
			if len(lines) != tc.numLines {
				t.Errorf("got %d diff lines, want %d", len(lines), tc.numLines)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Permission flow
// --------------------------------------------------------------------------

func TestTranscriptPermissionFlow(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	ev := session.Event{
		Type:    session.EventPermissionRequested,
		Payload: json.RawMessage(`{"permissionId":"p1","tool":"Edit","input":{"old_string":"a","new_string":"a\nb"}}`),
	}
	m.handleEvent(ev)

	if m.pending == nil {
		t.Fatal("permission.requested did not set pending")
	}
	if m.pending.id != "p1" || m.pending.tool != "Edit" {
		t.Errorf("pending = %+v", m.pending)
	}
	if m.pending.adds != 1 || m.pending.dels != 0 {
		t.Errorf("diff stat = +%d −%d, want +1 −0", m.pending.adds, m.pending.dels)
	}
	if m.status != StatusWaiting {
		t.Errorf("status = %v, want StatusWaiting", m.status)
	}

	// Approve: pending clears and the decision is dispatched to the client.
	cmd := m.resolvePermission(true)
	if m.pending != nil {
		t.Error("pending not cleared on approve")
	}
	if cmd == nil {
		t.Fatal("approve returned no command")
	}
	cmd()
	if len(fc.resolved) != 1 || !fc.resolved[0].Allow || fc.resolved[0].Permission != "p1" {
		t.Errorf("ResolvePermission not dispatched correctly: %+v", fc.resolved)
	}
}

func TestTranscriptViewDiffToggle(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	m.handleEvent(session.Event{
		Type:    session.EventPermissionRequested,
		Payload: json.RawMessage(`{"permissionId":"p1","tool":"Edit","input":{"old_string":"old","new_string":"new"}}`),
	})

	if m.showDiff {
		t.Fatal("diff should start collapsed")
	}
	// enter toggles the diff view while a permission is pending.
	m.handleKey(keyMsg("enter"))
	if !m.showDiff {
		t.Error("enter did not expand the diff view")
	}
	if !strings.Contains(m.permBox, "new") {
		t.Errorf("expanded permission box missing diff content:\n%s", m.permBox)
	}
}

// --------------------------------------------------------------------------
// Prompt submission
// --------------------------------------------------------------------------

func TestTranscriptSubmitStartsTurn(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.input.SetValue("hello there")
	cmd := m.submit()
	if cmd == nil {
		t.Fatal("submit returned no command")
	}
	if m.status != StatusBusy {
		t.Errorf("status = %v, want StatusBusy after submit", m.status)
	}
	if got := m.input.Value(); got != "" {
		t.Errorf("input not cleared after submit: %q", got)
	}
	// submit batches the StartTurn POST with the working-indicator tick; run the
	// batched commands and confirm the turn was dispatched to the client.
	execCmd(cmd)
	if len(fc.startedPrompts) != 1 || fc.startedPrompts[0] != "hello there" {
		t.Errorf("StartTurn not dispatched: %v", fc.startedPrompts)
	}
}

// execCmd runs a command, unwrapping a tea.Batch into its children. The
// StartTurn child hits the fake synchronously; the work-tick child is a timer
// that returns after its interval (fine for a single test).
func execCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				c()
			}
		}
	}
}

func TestTranscriptSubmitIgnoresBlank(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	m.input.SetValue("   ")
	if cmd := m.submit(); cmd != nil {
		t.Error("blank prompt should not start a turn")
	}
}

// --------------------------------------------------------------------------
// Header
// --------------------------------------------------------------------------

func TestTranscriptHeaderShowsAgentAndStatus(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	h := m.renderHeader()
	if !strings.Contains(h, "claude-sdk") {
		t.Errorf("header missing agent: %q", h)
	}
	// The chat header uses action-oriented labels (T12): a finished turn reads
	// "ready for input", not the internal "needs-input".
	if !strings.Contains(h, "ready for input") {
		t.Errorf("header missing status: %q", h)
	}
	if !strings.Contains(h, "proj") {
		t.Errorf("header missing title/project: %q", h)
	}
}

// --------------------------------------------------------------------------
// Permission grace period (B4 / chat-rendering §2.6)
// --------------------------------------------------------------------------

func TestPermissionGracePeriod(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	old := nowFunc
	nowFunc = func() time.Time { return base }
	defer func() { nowFunc = old }()

	mk := func(since, lastKey time.Time) *TranscriptModel {
		m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
		m.width, m.height = 80, 24
		m.layout()
		m.pending = &transcriptPermission{id: "perm-1", tool: "Bash", since: since}
		m.lastKeyAt = lastKey
		return m
	}

	// Type-ahead: the box just appeared and the user was mid-keystroke → an `a`
	// in flight must NOT auto-approve (pending stays set).
	m := mk(base, base)
	m.handleKey(keyMsg("a"))
	if m.pending == nil {
		t.Error("type-ahead 'a' auto-answered the permission during the grace window")
	}

	// Quiet: the box has been visible and input idle past the quiet window → a
	// deliberate `a` resolves it (resolvePermission clears pending).
	idle := base.Add(-permissionGraceQuiet - time.Millisecond)
	m2 := mk(idle, idle)
	m2.handleKey(keyMsg("a"))
	if m2.pending != nil {
		t.Error("deliberate 'a' after the quiet window did not resolve the permission")
	}

	// Hard cap: even with input still active (last key just now), once the box
	// has been visible past the cap it becomes answerable.
	m3 := mk(base.Add(-permissionGraceCap-time.Millisecond), base)
	m3.handleKey(keyMsg("a"))
	if m3.pending != nil {
		t.Error("permission not answerable after the hard cap")
	}
}

// TestStreamingTailUsesIncrementalRender verifies A2: the live streaming tail
// uses a persistent AssistantItem with StreamingMarkdown instead of creating a
// new item (and doing a full glamour re-render) per delta.
func TestStreamingTailUsesIncrementalRender(t *testing.T) {
	m := &TranscriptModel{body: list.New()}
	m.handleEvent(session.Event{Type: session.EventMessageStarted})
	if m.streamAI == nil {
		t.Fatal("streamAI not created on EventMessageStarted")
	}

	// Feed deltas.
	m.handleEvent(session.Event{Type: session.EventMessageDelta, Payload: jsonPayload(session.MessagePayload{Content: "Hello"})})
	m.handleEvent(session.Event{Type: session.EventMessageDelta, Payload: jsonPayload(session.MessagePayload{Content: " world"})})

	// The streamAI should still exist and its content should reflect the buffer.
	if m.streamAI == nil {
		t.Fatal("streamAI nil after deltas")
	}
	if m.assistantBuf.String() != "Hello world" {
		t.Errorf("assistantBuf = %q, want 'Hello world'", m.assistantBuf.String())
	}

	// Render the streaming tail via blockItem (mirrors what the list does).
	it := &blockItem{m: m, idx: -1, streaming: true}
	out := it.Render(80)
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "world") {
		t.Errorf("stream render missing content: %q", out)
	}

	// Streaming ends: streamAI should be cleared.
	m.handleEvent(session.Event{Type: session.EventMessageCompleted, Payload: jsonPayload(session.MessagePayload{Content: "Hello world"})})
	if m.streamAI != nil {
		t.Error("streamAI not cleared on EventMessageCompleted")
	}
}

func jsonPayload(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
