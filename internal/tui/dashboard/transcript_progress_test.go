package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// progressBase is a fixed wall clock for the elapsed/exit-code tests, swapped
// into nowFunc so re-anchoring and rendered durations are deterministic.
var progressBase = time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

// pinNow swaps nowFunc to a fixed clock for the duration of a test.
func pinNow(t *testing.T, at time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = old })
}

func intp(v int) *int { return &v }

// sendProgressAt injects a tool.progress event carrying an RFC3339 Event.Time, so
// the replay-anchoring path (applyToolProgress back-dates from the event's
// recorded time, not nowFunc) can be exercised deterministically.
func sendProgressAt(m *TranscriptModel, p session.ToolPayload, at time.Time) {
	raw, _ := json.Marshal(p)
	m.handleEvent(session.Event{
		Type:    session.EventToolProgress,
		Time:    at.Format(time.RFC3339),
		Payload: json.RawMessage(raw),
	})
}

// TestToolProgressReanchorsFlatCard asserts a tool.progress heartbeat re-anchors
// a flat card's startedAt to nowFunc()-elapsedSeconds (server elapsed wins over
// the local create-time anchor, which matters after an attach/replay).
func TestToolProgressReanchorsFlatCard(t *testing.T) {
	pinNow(t, progressBase)
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "p1"}}, nil)

	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "tool-1"})
	idx, ok := m.flatTools["tool-1"]
	if !ok {
		t.Fatal("flat card not indexed after tool.started")
	}
	if !m.blocks[idx].tool.startedAt.Equal(progressBase) {
		t.Fatalf("create-time anchor = %v, want %v", m.blocks[idx].tool.startedAt, progressBase)
	}

	sendEvent(m, session.EventToolProgress, session.ToolPayload{ToolUseID: "tool-1", ElapsedSeconds: fptr(12)})
	want := progressBase.Add(-12 * time.Second)
	if !m.blocks[idx].tool.startedAt.Equal(want) {
		t.Errorf("re-anchored startedAt = %v, want %v", m.blocks[idx].tool.startedAt, want)
	}
}

// TestToolProgressReanchorsSubagentCard asserts progress on a Task's toolUseId
// re-anchors the subagent card's elapsed clock.
func TestToolProgressReanchorsSubagentCard(t *testing.T) {
	pinNow(t, progressBase)
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "p2"}}, nil)

	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Task", ToolUseID: "task-1", AgentName: "explorer"})
	sub := m.subagents["task-1"]
	if sub == nil {
		t.Fatal("subagent card not created")
	}
	if !sub.startedAt.Equal(progressBase) {
		t.Fatalf("create-time anchor = %v, want %v", sub.startedAt, progressBase)
	}

	sendEvent(m, session.EventToolProgress, session.ToolPayload{ToolUseID: "task-1", ElapsedSeconds: fptr(8)})
	want := progressBase.Add(-8 * time.Second)
	if !sub.startedAt.Equal(want) {
		t.Errorf("re-anchored subagent startedAt = %v, want %v", sub.startedAt, want)
	}
}

// TestToolProgressUnknownOrChildDropped asserts progress for an unknown id, and
// for a subagent CHILD id (deliberately unhandled), is dropped without panic and
// without mutating any card.
func TestToolProgressUnknownOrChildDropped(t *testing.T) {
	pinNow(t, progressBase)
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "p3"}}, nil)

	// A subagent with a child tool: the child is indexed in childIndex only.
	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Task", ToolUseID: "task-1", AgentName: "explorer"})
	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Grep", ToolUseID: "child-1", ParentToolUseID: "task-1"})
	child := m.childIndex["child-1"]
	if child == nil {
		t.Fatal("child card not indexed")
	}

	// Unknown id: no panic, nothing to anchor.
	sendEvent(m, session.EventToolProgress, session.ToolPayload{ToolUseID: "nope", ElapsedSeconds: fptr(5)})
	// Child id: deliberately dropped — child cards carry no elapsed clock.
	sendEvent(m, session.EventToolProgress, session.ToolPayload{ToolUseID: "child-1", ElapsedSeconds: fptr(5)})
	if !child.startedAt.IsZero() {
		t.Errorf("child startedAt mutated by progress = %v, want zero", child.startedAt)
	}

	// A progress event missing elapsed or id is a no-op (guards the nil deref).
	sendEvent(m, session.EventToolProgress, session.ToolPayload{ToolUseID: "task-1"})
	sendEvent(m, session.EventToolProgress, session.ToolPayload{ElapsedSeconds: fptr(5)})
}

// elbowOf returns the visible text of a tool card's ⎿-elbow line.
func elbowOf(m *TranscriptModel, c *toolCard) string {
	lines := strings.Split(m.renderToolCard(c, 60), "\n")
	if len(lines) < 2 {
		return ""
	}
	return stripANSICodes(lines[1])
}

// TestRunningCardElapsedRender asserts a running card renders a live elapsed
// clock once it has run ≥2s, and the bare word under the threshold.
func TestRunningCardElapsedRender(t *testing.T) {
	pinNow(t, progressBase)
	m := toolCardTM(t)

	c := &toolCard{tool: "Bash", arg: "make", status: toolRunning, startedAt: progressBase.Add(-12 * time.Second)}
	if elbow := elbowOf(m, c); !strings.Contains(elbow, "running… (12s)") {
		t.Errorf("elbow = %q, want it to contain %q", elbow, "running… (12s)")
	}

	// Under 2s: bare "running…" (no elapsed parenthetical on the elbow).
	c2 := &toolCard{tool: "Bash", arg: "make", status: toolRunning, startedAt: progressBase.Add(-1 * time.Second)}
	elbow := elbowOf(m, c2)
	if !strings.Contains(elbow, "running…") {
		t.Errorf("elbow = %q, want it to contain %q", elbow, "running…")
	}
	if strings.Contains(elbow, "(") {
		t.Errorf("elbow = %q, want no elapsed parenthetical under the 2s threshold", elbow)
	}

	// Zero startedAt (e.g. an orphan card): bare "running…", never a clock.
	c3 := &toolCard{tool: "Bash", arg: "make", status: toolRunning}
	if elbow := elbowOf(m, c3); !strings.Contains(elbow, "running…") || strings.Contains(elbow, "(") {
		t.Errorf("zero-startedAt elbow = %q, want bare running…", elbow)
	}
}

// TestToolCardExitCodeRender asserts a completed Bash card prefixes its elbow
// with the exit code ("exit N · <summary>"), for both success and failure, and
// that a nil exit code renders exactly as before.
func TestToolCardExitCodeRender(t *testing.T) {
	m := toolCardTM(t)

	ok := &toolCard{tool: "Bash", arg: "make test", status: toolOK, summary: "3 lines", exitCode: intp(0)}
	if elbow := elbowOf(m, ok); !strings.Contains(elbow, "exit 0 · 3 lines") {
		t.Errorf("ok elbow = %q, want it to contain %q", elbow, "exit 0 · 3 lines")
	}

	fail := &toolCard{tool: "Bash", arg: "make test", status: toolErr, summary: "boom", exitCode: intp(1)}
	if elbow := elbowOf(m, fail); !strings.Contains(elbow, "exit 1 · boom") {
		t.Errorf("fail elbow = %q, want it to contain %q", elbow, "exit 1 · boom")
	}

	// exitCode present but no summary: just "exit N".
	bare := &toolCard{tool: "Bash", arg: "true", status: toolOK, exitCode: intp(0)}
	if elbow := elbowOf(m, bare); !strings.Contains(elbow, "exit 0") || strings.Contains(elbow, "·") {
		t.Errorf("bare-exit elbow = %q, want %q with no separator", elbow, "exit 0")
	}

	// nil exitCode: unchanged — the summary alone, no "exit" prefix.
	nilEC := &toolCard{tool: "Bash", arg: "make test", status: toolOK, summary: "3 lines"}
	elbow := elbowOf(m, nilEC)
	if !strings.Contains(elbow, "3 lines") || strings.Contains(elbow, "exit") {
		t.Errorf("nil-exitCode elbow = %q, want the summary alone", elbow)
	}
}

// TestToolProgressAnchorsFromEventTime asserts a replayed heartbeat anchors from
// the event's recorded time (evTime-elapsed), NOT nowFunc()-elapsed. A tool that
// really started 10m ago must show ~10m30s after a re-attach, not 30s.
func TestToolProgressAnchorsFromEventTime(t *testing.T) {
	pinNow(t, progressBase) // nowFunc = attach instant
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "p4"}}, nil)

	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "tool-1"})
	idx := m.flatTools["tool-1"]

	evTime := progressBase.Add(-10 * time.Minute) // heartbeat recorded 10m before attach
	sendProgressAt(m, session.ToolPayload{ToolUseID: "tool-1", ElapsedSeconds: fptr(30)}, evTime)

	want := evTime.Add(-30 * time.Second) // anchor = evTime - elapsed, i.e. ~10m30s ago
	if !m.blocks[idx].tool.startedAt.Equal(want) {
		t.Errorf("replayed startedAt = %v, want %v (evTime-elapsed, not nowFunc-elapsed)",
			m.blocks[idx].tool.startedAt, want)
	}
}

// TestToolProgressUnparseableTimeFallsBack asserts an empty/unparseable Event.Time
// falls back to nowFunc()-elapsed anchoring (the live-display default).
func TestToolProgressUnparseableTimeFallsBack(t *testing.T) {
	pinNow(t, progressBase)
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "p5"}}, nil)

	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "tool-1"})
	idx := m.flatTools["tool-1"]

	// Garbage Time string → parse fails → fall back to nowFunc()-elapsed.
	raw, _ := json.Marshal(session.ToolPayload{ToolUseID: "tool-1", ElapsedSeconds: fptr(30)})
	m.handleEvent(session.Event{Type: session.EventToolProgress, Time: "not-a-timestamp", Payload: json.RawMessage(raw)})

	want := progressBase.Add(-30 * time.Second)
	if !m.blocks[idx].tool.startedAt.Equal(want) {
		t.Errorf("fallback startedAt = %v, want %v (nowFunc-elapsed)", m.blocks[idx].tool.startedAt, want)
	}
}

// TestToolProgressAfterCompletionIsNoOp asserts a late heartbeat targeting an
// already-completed flat card neither re-anchors nor bumps it (FIX 6 guard —
// flatTools entries persist after completion).
func TestToolProgressAfterCompletionIsNoOp(t *testing.T) {
	pinNow(t, progressBase)
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "p6"}}, nil)

	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "tool-1"})
	idx := m.flatTools["tool-1"]
	sendEvent(m, session.EventToolCompleted, session.ToolPayload{Tool: "Bash", ToolUseID: "tool-1", Output: "ok"})

	startedBefore := m.blocks[idx].tool.startedAt
	verBefore := m.blocks[idx].Version()

	sendEvent(m, session.EventToolProgress, session.ToolPayload{ToolUseID: "tool-1", ElapsedSeconds: fptr(99)})

	if !m.blocks[idx].tool.startedAt.Equal(startedBefore) {
		t.Errorf("completed card re-anchored: startedAt = %v, want unchanged %v",
			m.blocks[idx].tool.startedAt, startedBefore)
	}
	if got := m.blocks[idx].Version(); got != verBefore {
		t.Errorf("completed card bumped: version = %d, want unchanged %d", got, verBefore)
	}
}

// TestBumpRunningCards asserts the work-tick bump re-renders a running flat card
// with a non-zero anchor, but leaves a completed card (and a zero-anchor card)
// untouched.
func TestBumpRunningCards(t *testing.T) {
	pinNow(t, progressBase)
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "p7"}}, nil)

	// A running flat card with a real anchor.
	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "run-1"})
	runIdx := m.flatTools["run-1"]
	// A second card that completes.
	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "done-1"})
	doneIdx := m.flatTools["done-1"]
	sendEvent(m, session.EventToolCompleted, session.ToolPayload{Tool: "Bash", ToolUseID: "done-1", Output: "ok"})

	runVer := m.blocks[runIdx].Version()
	doneVer := m.blocks[doneIdx].Version()

	if !m.bumpRunningCards() {
		t.Fatal("bumpRunningCards returned false, want true (a running flat card exists)")
	}
	if got := m.blocks[runIdx].Version(); got == runVer {
		t.Errorf("running flat card not bumped: version still %d", got)
	}
	if got := m.blocks[doneIdx].Version(); got != doneVer {
		t.Errorf("completed card bumped: version = %d, want unchanged %d", got, doneVer)
	}
}

// TestBumpRunningCardsZeroAnchorExcluded asserts a running flat card with a zero
// startedAt (no clock to advance) is NOT bumped on the tick.
func TestBumpRunningCardsZeroAnchorExcluded(t *testing.T) {
	pinNow(t, progressBase)
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "p8"}}, nil)

	// An orphan finished-then-reset card would be contrived; instead force the
	// zero-anchor condition directly on a running flat card.
	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "run-1"})
	idx := m.flatTools["run-1"]
	m.blocks[idx].tool.startedAt = time.Time{}
	ver := m.blocks[idx].Version()

	if m.bumpRunningCards() {
		t.Error("bumpRunningCards returned true, want false (only a zero-anchor card exists)")
	}
	if got := m.blocks[idx].Version(); got != ver {
		t.Errorf("zero-anchor card bumped: version = %d, want unchanged %d", got, ver)
	}
}
