package chat

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// update regenerates the golden files: `go test ./tui/chat -run TestGolden -update`.
var update = flag.Bool("update", false, "update golden files")

// canonicalTranscript builds a representative multi-turn transcript entirely from
// public items, with deterministic content (fixed elapsed, spinner frame 0), so
// its rendered structure is a stable golden. It exercises: a user prompt, a
// committed reasoning block, a streaming-then-finished assistant reply (via the
// public AssistantItem + Bullet chrome), a successful tool with expandable
// output, a failed tool, an expanded edit diff, a nested subagent with a running
// child + narration, a pinned todo list, and info/error notices.
func canonicalTranscript() []list.Item {
	ec0 := 0
	okTool := NewToolItem(&ToolCall{ID: "b1", Name: "Bash", Arg: "go test ./...", Status: ToolOK, Summary: "42 lines", ExitCode: &ec0, Output: "ok  \tpkg/one\t0.2s\nok  \tpkg/two\t0.4s\nPASS"})
	editTool := NewToolItem(&ToolCall{ID: "e1", Name: "Edit", Arg: "reconnect.go", Status: ToolOK,
		Diff: []string{"  func dial() {", "−\tbackoff := 100", "+\tbackoff := 250", "  }"}})
	editTool.SetExpanded(true)
	failTool := NewToolItem(&ToolCall{ID: "r1", Name: "Read", Arg: "/etc/missing", Status: ToolError, Summary: "no such file"})

	sub := NewSubagentItem(&Subagent{ID: "t1", AgentName: "Explore", Prompt: "find the backoff constant", Status: ToolRunning,
		Children: []*ToolCall{
			{Name: "Grep", Arg: "backoff", Status: ToolOK, Summary: "12 matches"},
			{Name: "Read", Arg: "reconnect.go", Status: ToolRunning},
		}, Narration: "the constant defaults to 250ms after the fix"})

	// A finished assistant reply, chromed with the public Bullet helper (as a host
	// composes AssistantItem output into the transcript).
	assistant := &finishedAssistant{
		Versioned: list.NewVersioned(),
		body:      "I ran the suite and fixed the reconnect backoff. All tests pass now.",
	}

	return []list.Item{
		NewUserItem("run the tests, find why reconnect is flaky, and fix it"),
		NewReasoningItem("The flake smells like a backoff race.\nLet me run the suite first, then grep for the backoff constant.", false),
		okTool,
		sub,
		editTool,
		failTool,
		NewPermissionItem(&Permission{ID: "p1", Tool: "Bash", Arg: "rm -rf ./node_modules", Diff: nil}),
		NewTodosItem([]Todo{
			{Content: "Run the test suite", Status: TodoCompleted},
			{Content: "Locate the backoff constant", ActiveForm: "Locating the backoff constant", Status: TodoInProgress},
			{Content: "Add a regression test", Status: TodoPending},
		}),
		NewNoticeItem(NoticeInfo, "context compacted"),
		assistant,
		NewElbowNotice("turn complete"),
		NewFooterItem(&TurnFooter{Model: "Opus 4.8", Backend: "anthropic", Elapsed: 12 * time.Second, InputTokens: 3100, OutputTokens: 820, CostUSD: 0.04}),
	}
}

// finishedAssistant is a tiny list.Item that renders a finished assistant body
// through the public Bullet chrome — the composition pattern the example and any
// host use (AssistantItem renders the body; Bullet adds the ⏺ grammar).
type finishedAssistant struct {
	*list.Versioned
	body string
}

func (f *finishedAssistant) Render(width int) string {
	ai := NewAssistantItem(&AssistantMessage{Content: f.body, Finished: true})
	ai.SetRenderContentMD(func(text string, w int) string {
		r := MarkdownRenderer(w)
		if r == nil {
			return text
		}
		out, err := r.Render(text)
		if err != nil {
			return text
		}
		return strings.TrimRight(out, "\n")
	})
	return Bullet(ai.RawRender(width - msgIndent))
}

type goldenSize struct {
	name string
	w, h int
}

// TestGoldenTranscript renders the canonical transcript at the handoff's required
// terminal sizes and compares the ANSI-stripped structure to a golden file.
// Stripping ANSI keeps the golden stable across palette tweaks while still pinning
// layout, wrapping, glyphs, tree structure, and caps; width-safety on the RAW
// ANSI frame is asserted separately (TestGoldenWidthSafe).
func TestGoldenTranscript(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	sizes := []goldenSize{
		{"80x24", 80, 24},
		{"100x30", 100, 30},
		{"140x40", 140, 40},
		{"narrow-40x30", 40, 30},
	}
	for _, s := range sizes {
		t.Run(s.name, func(t *testing.T) {
			l := list.New(canonicalTranscript()...)
			l.SetSize(s.w, s.h)
			l.GotoTop() // deterministic top-anchored frame
			frame := l.Render()
			got := ansi.Strip(frame)
			checkGolden(t, "transcript_"+s.name+".txt", got)
			widthSafe(t, "golden-"+s.name, frame, s.w)
			// A frame must not exceed its declared height.
			if n := strings.Count(frame, "\n") + 1; n > s.h {
				t.Errorf("frame is %d lines, exceeds height %d", n, s.h)
			}
		})
	}
}

// TestGoldenLightTheme: swapping to the light palette must preserve the
// ANSI-stripped structure (a palette swap is color-only) while changing the ANSI.
func TestGoldenLightTheme(t *testing.T) {
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	theme.ApplyForBackground(true)
	l := list.New(canonicalTranscript()...)
	l.SetSize(100, 30)
	l.GotoTop()
	dark := l.Render()

	theme.ApplyForBackground(false)
	l2 := list.New(canonicalTranscript()...)
	l2.SetSize(100, 30)
	l2.GotoTop()
	light := l2.Render()

	if ansi.Strip(dark) != ansi.Strip(light) {
		t.Error("light theme changed transcript structure (should be color-only)")
	}
	if dark == light {
		t.Error("light theme produced identical ANSI (palette did not apply)")
	}
	widthSafe(t, "golden-light", light, 100)
}

// TestGoldenStreaming snapshots a mid-turn transcript: a live reasoning block
// ("∴ Thinking"), a running tool with a fixed elapsed clock ("running… (5s)"), a
// running subagent, and a streaming (mid-sentence) assistant reply — the
// in-progress states a host renders before a turn completes and a footer lands.
func TestGoldenStreaming(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	sub := NewSubagentItem(&Subagent{ID: "t1", AgentName: "Explore", Prompt: "find the backoff constant", Status: ToolRunning,
		Children: []*ToolCall{{Name: "Grep", Arg: "backoff", Status: ToolRunning}}})
	items := []list.Item{
		NewUserItem("stream the fix and keep me posted"),
		NewReasoningItem("Reading the reconnect path.\nThe backoff race looks likely.", true),
		NewToolItem(&ToolCall{ID: "b1", Name: "Bash", Arg: "go test ./... -run TestReconnect", Status: ToolRunning, Elapsed: 5 * time.Second}),
		sub,
		&finishedAssistant{Versioned: list.NewVersioned(), body: "Working on it — I found the backoff constant and I'm"},
	}
	l := list.New(items...)
	l.SetSize(80, 24)
	l.GotoTop()
	frame := l.Render()
	checkGolden(t, "streaming_80x24.txt", ansi.Strip(frame))
	widthSafe(t, "streaming", frame, 80)
}

// TestGoldenFatalError snapshots a transcript whose turn died: a failed tool, a
// loud fatal error notice, and the aborted-turn elbow — what a host shows when
// the runner exits mid-turn.
func TestGoldenFatalError(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	items := []list.Item{
		NewUserItem("run the migration"),
		&finishedAssistant{Versioned: list.NewVersioned(), body: "Starting the migration now."},
		NewToolItem(&ToolCall{ID: "r1", Name: "Bash", Arg: "./migrate.sh", Status: ToolError, Summary: "exit 137 (OOMKilled)"}),
		NewNoticeItem(NoticeError, "fatal: the runner exited (code 137) — the session was terminated and the turn did not complete"),
		NewElbowNotice("turn aborted"),
	}
	l := list.New(items...)
	l.SetSize(80, 24)
	l.GotoTop()
	frame := l.Render()
	checkGolden(t, "fatal_80x24.txt", ansi.Strip(frame))
	widthSafe(t, "fatal", frame, 80)
}

// TestGoldenEmpty pins the empty-transcript frame: a freshly-connected session
// with no events renders nothing (no placeholder chrome, no stray line).
func TestGoldenEmpty(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	l := list.New()
	l.SetSize(80, 24)
	frame := l.Render()
	if frame != "" {
		t.Errorf("empty transcript rendered a non-empty frame: %q", frame)
	}
	checkGolden(t, "empty_80x24.txt", ansi.Strip(frame))
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
