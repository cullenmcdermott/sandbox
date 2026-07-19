package transcript

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

var update = flag.Bool("update", false, "update golden files")

// ---- event/render helpers ---------------------------------------------------

func ev(seq uint64, t client.EventType, payload any) client.Event {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			panic(err)
		}
		raw = b
	}
	return client.Event{Seq: seq, Type: t, Payload: raw}
}

// render returns the ANSI-stripped transcript body at the given size.
func render(m *Model, w, h int) string {
	m.SetSize(w, h)
	m.GotoTop()
	return ansi.Strip(m.Render())
}

func newModel(t *testing.T, opts ...Option) *Model {
	t.Helper()
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	m := New(opts...)
	t.Cleanup(m.Close)
	return m
}

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("transcript missing %q in:\n%s", want, got)
	}
}

// ---- a test per event kind --------------------------------------------------

func TestApplyMessageLifecycle(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventMessageStarted, client.MessagePayload{Role: "assistant"}))
	m.Apply(ev(2, client.EventMessageDelta, client.MessagePayload{Role: "assistant", Content: "Hello, ", Delta: true}))
	m.Apply(ev(3, client.EventMessageDelta, client.MessagePayload{Role: "assistant", Content: "world.", Delta: true}))
	// Mid-stream, the live tail shows the coalesced buffer.
	contains(t, render(m, 100, 20), "Hello, world.")
	m.Apply(ev(4, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "Hello, world."}))
	out := render(m, 100, 20)
	contains(t, out, "Hello, world.")
	contains(t, out, "⏺") // finished assistant carries the bullet grammar
}

func TestApplyReasoningLifecycle(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventReasoningStarted, client.MessagePayload{}))
	m.Apply(ev(2, client.EventReasoningDelta, client.MessagePayload{Content: "Thinking about it."}))
	contains(t, render(m, 100, 20), "Thinking") // live "∴ Thinking" tail
	m.Apply(ev(3, client.EventReasoningCompleted, client.MessagePayload{Content: "First reason.\nSecond reason."}))
	out := render(m, 100, 20)
	contains(t, out, "∴") // committed "∴ Thought"
	contains(t, out, "First reason.")
}

func TestApplyUserEcho(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventMessageCompleted, client.MessagePayload{Role: "user", Content: "do the thing"}))
	out := render(m, 100, 20)
	contains(t, out, "> do the thing")
}

func TestApplyUserEchoDedup(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Submit("do the thing") // optimistic user block
	before := m.Len()
	m.Apply(ev(1, client.EventMessageCompleted, client.MessagePayload{Role: "user", Content: "do the thing"}))
	if m.Len() != before {
		t.Errorf("user echo duplicated the optimistic block: len %d -> %d", before, m.Len())
	}
}

func TestApplyToolLifecycle(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventToolStarted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Input: json.RawMessage(`{"command":"go test ./..."}`)}))
	out := render(m, 100, 20)
	contains(t, out, "⏺")
	contains(t, out, "Bash")
	ec := 0
	m.Apply(ev(2, client.EventToolCompleted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Output: "ok\tpkg\t0.2s\nPASS", ExitCode: &ec}))
	contains(t, render(m, 100, 20), "Bash")
}

func TestApplyToolFailed(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventToolStarted, client.ToolPayload{Tool: "Read", ToolUseID: "r1", Input: json.RawMessage(`{"file_path":"/etc/missing"}`)}))
	m.Apply(ev(2, client.EventToolFailed, client.ToolPayload{Tool: "Read", ToolUseID: "r1", Error: "no such file"}))
	contains(t, render(m, 100, 20), "no such file")
}

func TestApplyToolProgressSetsElapsed(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventToolStarted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Input: json.RawMessage(`{"command":"sleep"}`)}))
	sec := 12.0
	m.Apply(ev(2, client.EventToolProgress, client.ToolPayload{ToolUseID: "t1", ElapsedSeconds: &sec}))
	contains(t, render(m, 100, 20), "12s")
}

func TestApplyToolDeltaPreviewsArg(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventToolStarted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1"}))
	m.Apply(ev(2, client.EventToolDelta, client.ToolPayload{ToolUseID: "t1", PartialJSON: `{"command":"npm install`}))
	contains(t, render(m, 100, 20), "npm install")
}

func TestApplyToolDiff(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	in := json.RawMessage(`{"file_path":"x.go","old_string":"a := 1","new_string":"a := 2"}`)
	m.Apply(ev(1, client.EventToolStarted, client.ToolPayload{Tool: "Edit", ToolUseID: "e1", Input: in}))
	m.Apply(ev(2, client.EventToolCompleted, client.ToolPayload{Tool: "Edit", ToolUseID: "e1"}))
	if !m.ToggleExpand() {
		t.Fatal("edit tool card did not expand (no diff attached)")
	}
	out := render(m, 100, 20)
	contains(t, out, "a := 2")
}

func TestApplySubagent(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventToolStarted, client.ToolPayload{Tool: "Task", ToolUseID: "task1", AgentName: "Explore", Input: json.RawMessage(`{"description":"find the flake"}`)}))
	m.Apply(ev(2, client.EventToolStarted, client.ToolPayload{Tool: "Grep", ToolUseID: "c1", ParentToolUseID: "task1", Input: json.RawMessage(`{"pattern":"flake"}`)}))
	m.Apply(ev(3, client.EventMessageDelta, client.MessagePayload{ParentToolUseID: "task1", Content: "the flake is in reconnect"}))
	m.Apply(ev(4, client.EventToolCompleted, client.ToolPayload{Tool: "Grep", ToolUseID: "c1", ParentToolUseID: "task1", Output: "7 matches"}))
	m.Apply(ev(5, client.EventToolCompleted, client.ToolPayload{Tool: "Task", ToolUseID: "task1"}))
	out := render(m, 120, 24)
	contains(t, out, "⊟ Task")
	contains(t, out, "find the flake")
	contains(t, out, "Grep")
	contains(t, out, "the flake is in reconnect")
	// The Task completed OK → its glyph is the check.
	contains(t, out, "✓")
}

func TestApplyPermissionRequestedResolved(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventPermissionRequested, client.PermissionPayload{PermissionID: "p1", Tool: "Bash", Input: json.RawMessage(`{"command":"rm -rf x"}`)}))
	if p := m.PendingPermission(); p == nil || p.Tool != "Bash" {
		t.Fatalf("pending permission not surfaced: %+v", p)
	}
	contains(t, render(m, 100, 20), "Bash")
	m.Apply(ev(2, client.EventPermissionResolved, client.PermissionPayload{PermissionID: "p1", Tool: "Bash", Decision: "allow-once"}))
	if m.PendingPermission() != nil {
		t.Error("pending permission not cleared on resolve")
	}
	contains(t, render(m, 100, 20), "permission approved")
}

func TestApproveInvokesCallback(t *testing.T) {
	var gotID, gotScope string
	m := newModel(t, WithMarkdown(false), WithApprove(func(id, scope string) { gotID, gotScope = id, scope }))
	m.Apply(ev(1, client.EventPermissionRequested, client.PermissionPayload{PermissionID: "p9", Tool: "Bash", Input: json.RawMessage(`{}`)}))
	m.Approve("session")
	if gotID != "p9" || gotScope != "session" {
		t.Errorf("approve callback got (%q,%q), want (p9,session)", gotID, gotScope)
	}
	if m.PendingPermission() != nil {
		t.Error("approve did not clear the pending permission")
	}
}

func TestApplyTodos(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventTodoUpdated, client.TodoUpdatedPayload{Todos: []client.TodoItem{
		{Content: "Run tests", Status: "completed"},
		{Content: "Fix flake", ActiveForm: "Fixing the flake", Status: "in_progress"},
	}}))
	before := m.Len()
	contains(t, render(m, 100, 20), "Run tests")
	// A second update replaces the single pinned block (does not append).
	m.Apply(ev(2, client.EventTodoUpdated, client.TodoUpdatedPayload{Todos: []client.TodoItem{
		{Content: "Run tests", Status: "completed"},
		{Content: "Fix flake", Status: "completed"},
	}}))
	if m.Len() != before {
		t.Errorf("todo.updated appended a second block: len %d -> %d", before, m.Len())
	}
}

func TestApplyContextCompacted(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventContextCompacted, client.ContextCompactedPayload{PreTokens: 120000, PostTokens: 40000}))
	contains(t, render(m, 100, 20), "context compacted")
}

func TestApplyError(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventError, client.ErrorPayload{Message: "boom"}))
	contains(t, render(m, 100, 20), "error: boom")
}

func TestApplySessionStatusError(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventSessionStatusChanged, client.SessionStatusPayload{Status: "error", Reason: "runner crashed"}))
	contains(t, render(m, 100, 20), "session error: runner crashed")
}

func TestApplyTerminating(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventSessionTerminating, client.TerminatingPayload{Reason: "pod terminating (SIGTERM)"}))
	contains(t, render(m, 100, 20), "pod terminating")
}

func TestApplyTurnInterrupted(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventTurnStarted, client.TurnStartedPayload{}))
	m.Apply(ev(2, client.EventTurnInterrupted, client.TurnInterruptedPayload{Reason: "client interrupt"}))
	contains(t, render(m, 100, 20), "Interrupted by user")
}

func TestApplyTurnFailed(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventTurnStarted, client.TurnStartedPayload{}))
	m.Apply(ev(2, client.EventTurnFailed, client.TurnFailedPayload{Message: "rate limited"}))
	contains(t, render(m, 100, 20), "rate limited")
}

func TestApplyDrainsRunningToolOnTurnEnd(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventTurnStarted, client.TurnStartedPayload{}))
	m.Apply(ev(2, client.EventToolStarted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Input: json.RawMessage(`{"command":"x"}`)}))
	// Turn ends before the tool result arrives — the card must not stay running.
	m.Apply(ev(3, client.EventTurnCompleted, client.TurnCompletedPayload{}))
	out := render(m, 100, 20)
	// A drained card carries the boundary reason as its result summary and no
	// longer shows the live "running…" clock.
	contains(t, out, "no result")
	if strings.Contains(out, "running") {
		t.Errorf("drained tool card still shows a running state:\n%s", out)
	}
}

func TestFooterOnTurnCompleted(t *testing.T) {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m := newModel(t, WithMarkdown(false), WithBackend("anthropic"), WithNow(clk.now))
	m.Apply(ev(1, client.EventSessionStarted, client.SessionStartedPayload{Model: "claude-opus-4-8"}))
	m.Apply(ev(2, client.EventModelsAvailable, client.ModelsAvailablePayload{Models: []client.ModelInfo{{Value: "claude-opus-4-8", DisplayName: "Opus 4.8"}}}))
	m.Apply(ev(3, client.EventTurnStarted, client.TurnStartedPayload{}))
	clk.t = clk.t.Add(12 * time.Second)
	m.Apply(ev(4, client.EventUsageUpdated, client.UsagePayload{InputTokens: 3100, OutputTokens: 820, TotalCostUSD: 0.04}))
	m.Apply(ev(5, client.EventTurnCompleted, client.TurnCompletedPayload{}))
	out := render(m, 120, 24)
	contains(t, out, "◇ Opus 4.8 · via anthropic · 12s · ↑3.1k ↓820 · $0.04")
}

// ---- replay dedup -----------------------------------------------------------

func TestReplayDedup(t *testing.T) {
	script := []client.Event{
		ev(1, client.EventTurnStarted, client.TurnStartedPayload{}),
		ev(2, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "done"}),
		ev(3, client.EventToolStarted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Input: json.RawMessage(`{"command":"x"}`)}),
		ev(4, client.EventToolCompleted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Output: "ok"}),
	}
	m := newModel(t, WithMarkdown(false))
	for _, e := range script {
		m.Apply(e)
	}
	first := render(m, 100, 24)
	lenAfterFirst := m.Len()

	// Re-feed the exact same events (a dashboard passive stream resuming from a
	// stale cursor): every one is at or below the seq cursor, so all are dropped.
	for _, e := range script {
		m.Apply(e)
	}
	if m.Len() != lenAfterFirst {
		t.Errorf("replay duplicated blocks: len %d -> %d", lenAfterFirst, m.Len())
	}
	if second := render(m, 100, 24); second != first {
		t.Errorf("replay changed the transcript:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestReplayBoundary(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.BeginReplay(3)
	if !m.Replaying() {
		t.Fatal("BeginReplay did not enter replay mode")
	}
	m.Apply(ev(1, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "a"}))
	m.Apply(ev(2, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "b"}))
	if !m.Replaying() {
		t.Error("still catching up (seq 2 < attach 3) but not replaying")
	}
	m.Apply(ev(3, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "c"}))
	if m.Replaying() {
		t.Error("crossed attachSeq (3) but still replaying")
	}
	// The explicit stream.live marker also clears replay.
	m2 := newModel(t, WithMarkdown(false))
	m2.BeginReplay(99)
	m2.Apply(ev(0, client.EventStreamLive, nil))
	if m2.Replaying() {
		t.Error("stream.live did not clear replay")
	}
}

// ---- unknown-event degradation ---------------------------------------------

func TestUnknownEventDegradesGracefully(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	m.Apply(ev(1, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "hello"}))
	before := render(m, 100, 20)
	lenBefore := m.Len()

	// An event type this reducer does not model must be a no-op (no panic, no
	// stray block). Chrome-only events (rate_limit) fall here too.
	m.Apply(client.Event{Seq: 2, Type: client.EventType("totally.unknown.v9"), Payload: json.RawMessage(`{"weird":true}`)})
	m.Apply(ev(3, client.EventRateLimitUpdated, client.RateLimitPayload{Available: true}))
	m.Apply(ev(4, client.EventWorkspaceStatus, client.WorkspaceStatusPayload{Branch: "main"}))

	if m.Len() != lenBefore {
		t.Errorf("unknown/chrome events added blocks: len %d -> %d", lenBefore, m.Len())
	}
	if after := render(m, 100, 20); after != before {
		t.Errorf("unknown/chrome events changed the transcript body:\n%s", after)
	}
}

func TestMalformedPayloadDoesNotPanic(t *testing.T) {
	m := newModel(t, WithMarkdown(false))
	// Deliberately broken payloads for known types: the reducer must not panic.
	bad := []client.EventType{
		client.EventMessageCompleted, client.EventToolStarted, client.EventToolCompleted,
		client.EventPermissionRequested, client.EventTodoUpdated, client.EventUsageUpdated,
		client.EventSessionStarted, client.EventContextCompacted, client.EventError,
	}
	for i, tp := range bad {
		m.Apply(client.Event{Seq: uint64(i + 1), Type: tp, Payload: json.RawMessage(`not json`)})
	}
	_ = render(m, 100, 20) // must not panic
}

// ---- fixed-script golden ----------------------------------------------------

// TestGoldenScript feeds a representative full turn as a fixed []client.Event
// and snapshots the ANSI-stripped transcript — the event-sourced analogue of the
// tui/chat item golden.
func TestGoldenScript(t *testing.T) {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m := newModel(t, WithBackend("anthropic"), WithNow(clk.now))

	ec := 0
	script := []client.Event{
		ev(1, client.EventSessionStarted, client.SessionStartedPayload{Model: "claude-opus-4-8"}),
		ev(2, client.EventModelsAvailable, client.ModelsAvailablePayload{Models: []client.ModelInfo{{Value: "claude-opus-4-8", DisplayName: "Opus 4.8"}}}),
		ev(3, client.EventTurnStarted, client.TurnStartedPayload{Prompt: "run the tests and fix the flake"}),
		ev(4, client.EventMessageCompleted, client.MessagePayload{Role: "user", Content: "run the tests and fix the flake"}),
		ev(5, client.EventReasoningCompleted, client.MessagePayload{Content: "The flake smells like a backoff race.\nLet me run the suite first."}),
		ev(6, client.EventToolStarted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Input: json.RawMessage(`{"command":"go test ./..."}`)}),
		ev(7, client.EventToolCompleted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Output: "ok\tpkg/one\t0.2s\nPASS", ExitCode: &ec}),
		ev(8, client.EventTodoUpdated, client.TodoUpdatedPayload{Todos: []client.TodoItem{
			{Content: "Run the suite", Status: "completed"},
			{Content: "Fix the flake", ActiveForm: "Fixing the flake", Status: "in_progress"},
		}}),
		ev(9, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "All tests pass. The backoff race is fixed."}),
		ev(10, client.EventUsageUpdated, client.UsagePayload{InputTokens: 3100, OutputTokens: 820, TotalCostUSD: 0.04}),
	}
	for _, e := range script {
		if e.Type == client.EventTurnStarted {
			// advance the clock so the footer shows a stable elapsed
			m.Apply(e)
			clk.t = clk.t.Add(12 * time.Second)
			continue
		}
		m.Apply(e)
	}
	m.Apply(ev(11, client.EventTurnCompleted, client.TurnCompletedPayload{}))

	got := render(m, 100, 40)
	checkGolden(t, "script_100x40.txt", got)
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", name, err)
	}
	if string(want) != got {
		t.Errorf("golden %s mismatch (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}

// clock is a controllable time source for deterministic tests.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
