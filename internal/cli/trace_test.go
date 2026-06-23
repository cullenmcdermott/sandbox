package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func traceEvent(seq uint64, typ session.EventType, payload any) session.Event {
	raw, _ := json.Marshal(payload)
	return session.Event{
		Seq:     seq,
		Time:    "2026-06-22T14:30:05Z",
		Type:    typ,
		Payload: raw,
	}
}

func sampleTrace() []session.Event {
	exit0, exit2 := 0, 2
	return []session.Event{
		traceEvent(1, session.EventTurnStarted, map[string]any{"prompt": "build it"}),
		traceEvent(2, session.EventMessageCompleted, session.MessagePayload{Role: "assistant", Content: "Running the build.\nOne moment."}),
		traceEvent(3, session.EventToolStarted, session.ToolPayload{Tool: "Bash", Input: json.RawMessage(`{"command":"go build ./..."}`)}),
		traceEvent(4, session.EventToolCompleted, session.ToolPayload{Tool: "Bash", Output: "ok", ExitCode: &exit0}),
		traceEvent(5, session.EventToolFailed, session.ToolPayload{Tool: "Bash", Error: "exit status 2", ExitCode: &exit2}),
		traceEvent(6, session.EventToolCompleted, session.ToolPayload{Tool: "Edit", Output: "edited"}),
		traceEvent(7, session.EventSessionStatusChanged, session.SessionStatusPayload{Status: "error", Reason: "model overloaded"}),
	}
}

func TestRenderTraceHuman(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTrace(&buf, sampleTrace(), traceOptions{}); err != nil {
		t.Fatalf("renderTrace: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 7 {
		t.Fatalf("want 7 lines, got %d:\n%s", len(lines), out)
	}
	// Spot-check content: seq, formatted time, type, and useful summary.
	if !strings.Contains(lines[2], "tool.started") || !strings.Contains(lines[2], "go build ./...") {
		t.Errorf("tool.started line missing command: %q", lines[2])
	}
	if !strings.Contains(lines[2], "14:30:05") {
		t.Errorf("expected formatted time-of-day in line: %q", lines[2])
	}
	if !strings.Contains(lines[4], "error: exit status 2") {
		t.Errorf("tool.failed line missing error: %q", lines[4])
	}
	if !strings.Contains(lines[1], "assistant: Running the build. One moment.") {
		t.Errorf("message line not collapsed to one line: %q", lines[1])
	}
	if !strings.Contains(lines[6], "error (model overloaded)") {
		t.Errorf("status line missing reason: %q", lines[6])
	}
}

func TestRenderTraceToolFilter(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTrace(&buf, sampleTrace(), traceOptions{tool: "Bash"}); err != nil {
		t.Fatalf("renderTrace: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Only the three Bash tool events (seq 3,4,5); the Edit tool + non-tool
	// events are excluded.
	if len(lines) != 3 {
		t.Fatalf("want 3 Bash lines, got %d:\n%s", len(lines), out)
	}
	for _, l := range lines {
		if !strings.Contains(l, "Bash") {
			t.Errorf("non-Bash line leaked through --tool filter: %q", l)
		}
		if strings.Contains(l, "Edit") {
			t.Errorf("Edit tool leaked through --tool=Bash: %q", l)
		}
	}
}

func TestRenderTraceSince(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTrace(&buf, sampleTrace(), traceOptions{since: 5}); err != nil {
		t.Fatalf("renderTrace: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 { // seq 6,7
		t.Fatalf("want 2 lines after seq 5, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "6 ") {
		t.Errorf("first line should be seq 6: %q", lines[0])
	}
}

func TestRenderTraceJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTrace(&buf, sampleTrace(), traceOptions{json: true}); err != nil {
		t.Fatalf("renderTrace: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 7 {
		t.Fatalf("want 7 json lines, got %d", len(lines))
	}
	// Each line must be a valid, round-trippable normalized event.
	var first session.Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 0 not valid event json: %v", err)
	}
	if first.Seq != 1 || first.Type != session.EventTurnStarted {
		t.Errorf("decoded event mismatch: %+v", first)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"short", "short"},
		{strings.Repeat("a", 10), strings.Repeat("a", 10)},
		{strings.Repeat("a", 11), strings.Repeat("a", 9) + "…"},
	}
	for _, c := range cases {
		if got := truncate(c.in, 10); got != c.want {
			t.Errorf("truncate(%q,10) = %q, want %q", c.in, got, c.want)
		}
	}
}
