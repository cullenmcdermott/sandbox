// Command chatdemo is a self-contained example that builds the polished Sandbox
// chat transcript ENTIRELY from public packages — no internal/ import, and in
// particular none of internal/tui/dashboard. It drives the higher-level
// interactive components from a SCRIPTED stream of public client.Event values,
// exactly as a real host feeds the SSE event stream into the transcript:
//
//	tui/transcript — the event-sourced reducer: Apply(client.Event) mutates it,
//	                 and it renders the transcript through tui/chat + tui/list
//	tui/composer   — the multi-line input with queue-while-busy steering and the
//	                 escape cascade; its submissions feed back into the transcript
//	tui/chrome     — the status/working indicator framing the conversation
//	tui/theme      — semantic color tokens + live theme swap
//	tui/terminal   — OSC tab-progress signal while a turn streams
//
// It replays a full turn as events — session start, a user prompt echo, a
// reasoning block, a running tool that completes, a todo checklist, a permission
// request that resolves, and a streaming-markdown assistant reply — proving the
// reducer, streaming, caching, wrapping, theme-swap, scrolling, and responsive
// layout are all real public component code. Nothing is hand-assembled.
//
// Run it:
//
//	go run ./cmd/chatdemo
//
// Keys: type + enter to send (queues while the scripted turn runs) · r replay ·
// ctrl+o expand/collapse the latest tool · ctrl+t swap theme · ↑/↓/pgup/pgdn
// scroll · esc interrupt/steer · q quit.
package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/chrome"
	"github.com/cullenmcdermott/sandbox/tui/composer"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
	"github.com/cullenmcdermott/sandbox/tui/transcript"
)

const (
	animFPS  = 33 * time.Millisecond
	eventGap = 3 // ticks between scripted events
	tickStep = 120 * time.Millisecond
)

type tickMsg struct{}

// demoClock advances a fixed step per tick so the working indicator's elapsed
// clock and the per-turn footer read as real without wall-clock nondeterminism.
type demoClock struct{ t time.Time }

func (c *demoClock) now() time.Time { return c.t }

type model struct {
	width, height int

	tr   *transcript.Model
	comp *composer.Model
	clk  *demoClock

	script    []client.Event
	evIdx     int
	frame     int
	ticking   bool
	engine    *anim.Engine
	busy      bool
	turnStart time.Time
	verb      string
	outTok    int
	turnDone  bool

	footer lipgloss.Style
}

func newModel() *model {
	m := &model{engine: anim.NewEngine()}
	theme.OnChange(func() { m.footer = lipgloss.NewStyle().Foreground(theme.TextMuted) })
	m.reset()
	return m
}

// reset (re)builds the transcript, composer, and the scripted event stream.
func (m *model) reset() {
	m.clk = &demoClock{t: time.Unix(1_700_000_000, 0)}
	m.tr = transcript.New(
		transcript.WithNow(m.clk.now),
		transcript.WithBackend("anthropic"),
		transcript.WithApprove(func(id, scope string) {}),
		transcript.WithInterrupt(func() { m.busy = false }),
		transcript.WithDetach(func() {}),
	)
	m.comp = composer.New(
		composer.WithNow(m.clk.now),
		composer.WithSubmit(func(s string) { m.tr.Submit(s) }),
		composer.WithSteer(func(s string) { m.tr.Submit(s) }),
		composer.WithInterrupt(func() { m.tr.Interrupt() }),
	)
	m.comp.Focus()
	m.evIdx, m.frame, m.busy, m.turnDone, m.outTok = 0, 0, false, false, 0
	m.verb = "Working"
	m.script = demoScript()
	m.layout()
}

// demoScript is a full turn expressed as public client.Event values.
func demoScript() []client.Event {
	ec := 0
	return []client.Event{
		ev(1, client.EventSessionStarted, client.SessionStartedPayload{Model: "claude-opus-4-8"}),
		ev(2, client.EventModelsAvailable, client.ModelsAvailablePayload{Models: []client.ModelInfo{{Value: "claude-opus-4-8", DisplayName: "Opus 4.8"}}}),
		ev(3, client.EventTurnStarted, client.TurnStartedPayload{Prompt: "run the tests, find why reconnect is flaky, and fix it"}),
		ev(4, client.EventMessageCompleted, client.MessagePayload{Role: "user", Content: "run the tests, find why reconnect is flaky, and fix it"}),
		ev(5, client.EventReasoningCompleted, client.MessagePayload{Content: "The flake smells like a backoff race.\nLet me run the suite, then grep for the backoff constant."}),
		ev(6, client.EventToolStarted, client.ToolPayload{Tool: "Bash", ToolUseID: "b1", Input: json.RawMessage(`{"command":"go test ./..."}`)}),
		ev(7, client.EventToolCompleted, client.ToolPayload{Tool: "Bash", ToolUseID: "b1", Output: "ok  \tpkg/runner\t0.42s\nok  \tpkg/session\t0.31s\nPASS", ExitCode: &ec}),
		ev(8, client.EventTodoUpdated, client.TodoUpdatedPayload{Todos: []client.TodoItem{
			{Content: "Run the test suite", Status: "completed"},
			{Content: "Locate the backoff constant", Status: "completed"},
			{Content: "Fix and re-run", ActiveForm: "Fixing and re-running", Status: "in_progress"},
		}}),
		ev(9, client.EventPermissionRequested, client.PermissionPayload{PermissionID: "p1", Tool: "Edit", Input: json.RawMessage(`{"file_path":"reconnect.go","old_string":"backoff := 100","new_string":"backoff := 250"}`)}),
		ev(10, client.EventPermissionResolved, client.PermissionPayload{PermissionID: "p1", Tool: "Edit", Decision: "allow-once"}),
		ev(11, client.EventMessageStarted, client.MessagePayload{Role: "assistant"}),
		ev(12, client.EventMessageDelta, client.MessagePayload{Role: "assistant", Content: "I ran the suite and reproduced the flake. ", Delta: true}),
		ev(13, client.EventMessageDelta, client.MessagePayload{Role: "assistant", Content: "The reconnect **backoff** was hard-coded to `100ms`, ", Delta: true}),
		ev(14, client.EventMessageDelta, client.MessagePayload{Role: "assistant", Content: "too tight for a cold pod resume. I raised it to `250ms`.", Delta: true}),
		ev(15, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "I ran the suite and reproduced the flake. The reconnect **backoff** was hard-coded to `100ms`, too tight for a cold pod resume. I raised it to `250ms` in `reconnect.go` and the suite is green now."}),
		ev(16, client.EventUsageUpdated, client.UsagePayload{InputTokens: 4200, OutputTokens: 610, TotalCostUSD: 0.03}),
		ev(17, client.EventTurnCompleted, client.TurnCompletedPayload{}),
	}
}

func ev(seq uint64, typ client.EventType, payload any) client.Event {
	b, _ := json.Marshal(payload)
	return client.Event{Seq: seq, Type: typ, Payload: b}
}

func (m *model) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	// Reserve the status row (1), the composer (its own Height), and the hint (1).
	m.comp.SetWidth(m.width)
	reserve := 2 + m.comp.Height()
	m.tr.SetSize(max(1, m.width-1), max(1, m.height-reserve))
}

// --- motion + scripting ------------------------------------------------------

func (m *model) motionActive() bool { return !m.turnDone }

func (m *model) tick() tea.Cmd {
	return tea.Tick(animFPS, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) ensureTick() tea.Cmd {
	n := 0
	if m.motionActive() {
		n = 1
	}
	m.engine.SetSpinners(n)
	if m.ticking || (!m.motionActive() && !m.engine.AnyMotionActive(time.Now())) {
		return nil
	}
	m.ticking = true
	return m.tick()
}

func (m *model) advance() {
	m.frame++
	m.clk.t = m.clk.t.Add(tickStep)
	// Fire the next scripted event every eventGap ticks.
	if m.evIdx < len(m.script) && m.frame%eventGap == 0 {
		e := m.script[m.evIdx]
		m.applyEvent(e)
		m.evIdx++
		if m.evIdx >= len(m.script) {
			m.turnDone = true
		}
	}
	m.comp.SetPermissionPending(m.tr.PendingPermission() != nil)
	if m.busy {
		m.comp.SetState(composer.StateBusy)
	} else {
		m.comp.SetState(composer.StateReady)
	}
}

func (m *model) applyEvent(e client.Event) {
	switch e.Type {
	case client.EventTurnStarted:
		m.busy, m.turnStart, m.verb = true, m.clk.now(), "Thinking"
	case client.EventTurnCompleted, client.EventTurnFailed, client.EventTurnInterrupted:
		m.busy = false
	case client.EventReasoningCompleted:
		m.verb = "Thinking"
	case client.EventToolStarted:
		m.verb = "Running"
	case client.EventMessageStarted, client.EventMessageDelta:
		m.verb = "Writing"
	case client.EventUsageUpdated:
		var p client.UsagePayload
		if json.Unmarshal(e.Payload, &p) == nil {
			m.outTok = p.OutputTokens
		}
	}
	m.tr.Apply(e)
}

// --- Bubble Tea wiring -------------------------------------------------------

func (m *model) Init() tea.Cmd { return m.ensureTick() }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, m.ensureTick()
	case tickMsg:
		m.ticking = false
		m.advance()
		m.layout()
		return m, m.ensureTick()
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.comp.Value() == "" {
			return m, tea.Quit
		}
	case "ctrl+t":
		theme.Cycle()
		return m, nil
	case "r":
		if m.comp.Value() == "" {
			m.reset()
			return m, m.ensureTick()
		}
	case "ctrl+o":
		m.tr.ToggleExpand()
		return m, nil
	case "pgup":
		m.tr.ScrollBy(-m.tr.Height())
		return m, nil
	case "pgdown":
		m.tr.ScrollBy(m.tr.Height())
		return m, nil
	}
	// Everything else drives the composer (input, enter/esc cascade, history).
	m.comp, _ = m.comp.Update(msg)
	m.layout()
	return m, nil
}

func (m *model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}
	status := ""
	if m.busy {
		status = chrome.WorkingIndicator(chrome.Working{
			Verb:         m.verb,
			Elapsed:      m.clk.now().Sub(m.turnStart),
			OutputTokens: m.outTok,
			Hint:         "esc to interrupt",
			Frame:        m.frame,
		})
	}
	hint := m.footer.Render(kit.KbdRow(
		[2]string{"enter", "send"},
		[2]string{"r", "replay"},
		[2]string{"ctrl+o", "expand"},
		[2]string{"ctrl+t", "theme"},
		[2]string{"q", "quit"},
	))
	parts := []string{}
	if status != "" {
		parts = append(parts, status)
	}
	parts = append(parts, m.tr.Render(), m.comp.View(), hint)
	content := strings.Join(parts, "\n")
	frame := opaqueFrame(content, m.width, m.height, theme.Page)
	if m.busy {
		frame = terminal.OSCProgress(terminal.ProgressBusy) + frame
	}
	v := tea.NewView(frame)
	v.AltScreen = true
	return v
}

// opaqueFrame forces a solid page background across the whole frame so a
// transparent terminal doesn't show through. Kept local — the example imports
// nothing from internal/.
func opaqueFrame(content string, width, height int, bg color.Color) string {
	r, g, b, _ := bg.RGBA()
	set := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
	const reset = "\x1b[0m"
	blank := set + strings.Repeat(" ", max(width, 0)) + reset
	lines := strings.Split(content, "\n")
	out := make([]string, 0, max(height, len(lines)))
	for _, ln := range lines {
		ln = strings.ReplaceAll(ln, reset, reset+set)
		ln = strings.ReplaceAll(ln, "\x1b[m", "\x1b[m"+set)
		if pad := width - lipgloss.Width(ln); pad > 0 {
			ln += strings.Repeat(" ", pad)
		}
		if len(out) < height {
			out = append(out, set+ln+reset)
		}
	}
	for len(out) < height {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}

func main() {
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "chatdemo:", err)
		os.Exit(1)
	}
}
