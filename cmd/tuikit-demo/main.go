// Command tuikit-demo is a self-contained example chat TUI built entirely from
// the reusable public packages under github.com/cullenmcdermott/sandbox/tui:
//
//	tui/theme    — semantic color tokens + theme registry/swap, gradient & spinner
//	               helpers, status glyphs, and the brand mark
//	tui/kit      — stateless render helpers (Card, Badge, Button, KV, KbdRow,
//	               SectionHeader, TitledRule, ErrorBlock, Scrollbar, Format*)
//	tui/list     — a virtualized, version-cached scrolling list of row-blocks
//	tui/anim     — a gated motion engine + pre-rendered gradient spinner
//	tui/terminal — Ghostty/Kitty capability detection, OSC progress signals, and
//	               the Kitty graphics protocol (real inline images)
//
// It imports nothing from internal/ — everything here is reusable by any other
// system. The app is a cohesive multi-screen flow:
//
//	Claude model picker (floating overlay) → live chat
//
// The chat streams MOCKED responses word-by-word, renders tool-call cards, shows
// a thinking spinner, and tracks a context-usage block-bar gauge. Saying "show me
// a kitty image" pops a real cat photo rendered with the Kitty graphics protocol
// (RGBA over APC _G, transmitted via tea.Raw since the cell renderer drops APC),
// with colored block-art as the fallback. A capability panel (ctrl+g) shows what
// tui/terminal detected.
//
// Run it:
//
//	go run ./cmd/tuikit-demo
package main

import (
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// animFPS is the cadence of the single motion tick (~30fps), mirroring the
// dashboard's gated loop: a tick is scheduled only while motion is active.
const animFPS = 33 * time.Millisecond

// ctxLimit is the mock model's context window, used to drive the usage gauge.
const ctxLimit = 200_000

// screen is the active top-level view.
type screen int

const (
	screenPicker screen = iota
	screenChat
)

// tickMsg drives the gated motion loop.
type tickMsg struct{}

type styles struct {
	page   lipgloss.Style
	footer lipgloss.Style
}

type model struct {
	width, height int
	screen        screen
	caps          terminal.Caps

	// picker
	pickerSel int

	// chat
	modelName string // chosen Claude model (display name)
	rows      []list.Item
	list      *list.List
	input     string
	phase     chatPhase
	phaseAt   int // frame at which the current phase began
	turn      int
	reply     *reply   // scripted reply being played out
	stream    []string // remaining words to reveal
	cur       *msgItem // assistant message currently streaming
	curTool   *toolItem
	tokens    int
	stick     bool // keep transcript pinned to the bottom
	showCaps  bool // capability panel overlay
	showKitty bool // cat-image popup overlay

	// gauge + kitty-image state
	gaugeFrac float64 // context-usage fraction (0..1) for the header bar
	catXmit   string  // the cat image's Kitty transmission (built once, sent via tea.Raw)

	// motion
	frame   int
	engine  *anim.Engine
	ticking bool

	st styles
}

func newModel() *model {
	m := &model{
		screen: screenPicker,
		caps:   terminal.Detect(),
		list:   list.New(),
		engine: anim.NewEngine(),
		stick:  true,
		phase:  phaseIdle,
	}
	// Re-derive app styles on every theme swap (runs once immediately).
	theme.OnChange(m.rebuildStyles)
	return m
}

func (m *model) rebuildStyles() {
	m.st.page = lipgloss.NewStyle().Background(theme.Page).Foreground(theme.TextBody)
	m.st.footer = lipgloss.NewStyle().Foreground(theme.TextMuted)
}

// --- motion gating -------------------------------------------------------

// motionActive reports whether anything on screen is animating. Only the chat's
// in-flight turn animates, so an idle chat (and the static picker) schedules no
// ticks — the gating contract that keeps a quiescent UI at 0% CPU.
func (m *model) motionActive() bool { return m.screen == screenChat && m.phase != phaseIdle }

func (m *model) tick() tea.Cmd {
	return tea.Tick(animFPS, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) ensureTick() tea.Cmd {
	n := 0
	if m.motionActive() {
		n = 1
	}
	m.engine.SetSpinners(n)
	if m.ticking || !m.engine.AnyMotionActive(time.Now()) {
		return nil
	}
	m.ticking = true
	return m.tick()
}

// --- Bubble Tea wiring ----------------------------------------------------

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tickMsg:
		m.ticking = false
		m.frame++
		m.advance() // drive the mocked turn forward
		return m, m.ensureTick()

	case tea.PasteMsg:
		// Bracketed paste arrives as one PasteMsg, not key presses — append it to
		// the chat input (collapsed to a single line).
		if m.screen == screenChat && !m.showCaps && !m.showKitty {
			m.input += sanitizePaste(msg.Content)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// sanitizePaste collapses a pasted blob into a single input line, dropping the
// newlines/tabs/carriage-returns that would otherwise break the one-line prompt.
func sanitizePaste(s string) string {
	r := strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ")
	return r.Replace(s)
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Global quit (the chat owns plain typing, so only ctrl+c quits there).
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.screen {
	case screenPicker:
		return m.pickerKey(msg)
	default:
		return m.chatKey(msg)
	}
}

func (m *model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}
	var content string
	switch m.screen {
	case screenPicker:
		content = m.pickerView()
	default:
		content = m.chatView()
	}

	// Paint a fully opaque page background. lipgloss ends every Render with a
	// full reset, which clears any inherited background — so an outer
	// Background() style leaves most cells transparent and they show the desktop
	// through on a transparent terminal. opaqueFrame re-asserts the page color on
	// every cell instead, so the UI is consistently opaque regardless of terminal
	// transparency.
	frame := opaqueFrame(content, m.width, m.height, theme.Page)

	// Splice the (zero-width / width-correct) terminal-feature strings onto the
	// composed frame — exactly the pattern the dashboard uses. All are no-ops on
	// terminals that don't support them.
	frame = m.terminalSignals() + frame

	v := tea.NewView(frame)
	v.AltScreen = true
	return v
}

// opaqueFrame forces a solid background color across an entire width×height
// frame. It (1) sets the bg at the start of every line, (2) re-asserts it
// immediately after each reset sequence so inner styled segments don't punch a
// transparent hole for the rest of the line, (3) pads every line to width with
// background-colored cells, and (4) pads the block to height with full bg lines.
// Lines already wider than width are left intact (never truncated mid-escape).
func opaqueFrame(content string, width, height int, bg color.Color) string {
	r, g, b, _ := bg.RGBA()
	set := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
	const reset = "\x1b[0m"

	blank := set + strings.Repeat(" ", max(width, 0)) + reset
	lines := strings.Split(content, "\n")
	out := make([]string, 0, max(height, len(lines)))
	for _, ln := range lines {
		// Re-assert the page bg after every reset within the line so a child
		// style that reset to default doesn't leave the rest of the row (and its
		// trailing padding) transparent.
		ln = strings.ReplaceAll(ln, reset, reset+set)
		ln = strings.ReplaceAll(ln, "\x1b[m", "\x1b[m"+set)
		if pad := width - lipgloss.Width(ln); pad > 0 {
			ln += strings.Repeat(" ", pad)
		}
		out = append(out, set+ln+reset)
	}
	for len(out) < height {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}

// terminalSignals returns the control strings to prepend to the frame: the OSC
// 9;4 tab-progress signal while a turn is streaming. (The Kitty image is NOT
// emitted here — APC graphics sequences are dropped by the cell renderer, so the
// transmission goes through tea.Raw instead; see showKittyImage.)
func (m *model) terminalSignals() string {
	var pre string
	if m.screen == screenChat {
		if m.phase != phaseIdle {
			pre += terminal.OSCProgress(terminal.ProgressBusy)
		} else {
			pre += terminal.OSCProgress(terminal.ProgressNone)
		}
	}
	return pre
}

func main() {
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tuikit-demo:", err)
		os.Exit(1)
	}
}
