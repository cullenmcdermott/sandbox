package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// chatPhase is the state of the (mocked) in-flight turn.
type chatPhase int

const (
	phaseIdle chatPhase = iota
	phaseThinking
	phaseTool
	phaseStreaming
)

// Turn pacing, in ~33ms frames.
const (
	thinkFrames = 16 // ~0.5s "thinking" before the first output
	toolFrames  = 20 // ~0.66s a tool runs before it returns
	wordEvery   = 2  // reveal one word every 2 frames (~15 words/s)
)

// --- transcript items -----------------------------------------------------

type msgRole int

const (
	roleUser msgRole = iota
	roleAssistant
)

// msgItem is one chat bubble. It satisfies list.Item; bumping its version on each
// streamed word lets the list re-render only this block.
type msgItem struct {
	*list.Versioned
	role msgRole
	text string
	done bool
}

func newMsgItem(role msgRole) *msgItem {
	return &msgItem{Versioned: list.NewVersioned(), role: role}
}

func (i *msgItem) Finished() bool { return i.done }

func (i *msgItem) Render(width int) string {
	var label string
	switch i.role {
	case roleUser:
		label = kit.Badge("you", kit.RoleInfo)
	default:
		label = theme.MarkClaudeStyled() + " " +
			lipgloss.NewStyle().Foreground(theme.TextSecondary).Bold(true).Render("claude")
	}

	cursor := ""
	if !i.done {
		cursor = lipgloss.NewStyle().Foreground(theme.Busy).Render("▍")
	}
	body := lipgloss.NewStyle().Foreground(theme.TextBody).Width(width).Render(i.text + cursor)
	return "\n" + label + "\n" + body
}

// toolItem is a tool-call card: a kit.Card framing the call, with a spinner while
// it runs and a result body once it completes.
type toolItem struct {
	*list.Versioned
	name, arg, result string
	done              bool
	frame             int
}

func newToolItem(name, arg string) *toolItem {
	return &toolItem{Versioned: list.NewVersioned(), name: name, arg: arg}
}

func (i *toolItem) complete(result string) {
	i.result, i.done = result, true
	i.Bump()
}

func (i *toolItem) Finished() bool { return i.done }

func (i *toolItem) Render(width int) string {
	title := i.name + lipgloss.NewStyle().Foreground(theme.TextMuted).Render("("+i.arg+")")
	var body string
	if i.done {
		body = lipgloss.NewStyle().Foreground(theme.Guac).Render("✓ ") +
			lipgloss.NewStyle().Foreground(theme.TextBody).Render(i.result)
	} else {
		body = theme.SpinnerFrame(i.frame) + " " +
			lipgloss.NewStyle().Foreground(theme.TextMuted).Render("running…")
	}
	w := min(width, 60)
	return "\n" + kit.Card(kit.CardOpts{Title: title, Body: body, Accent: kit.RoleInfo, Width: w})
}

// --- chat lifecycle -------------------------------------------------------

// startChat enters the chat screen with the chosen Claude model and a greeting.
func (m *model) startChat(modelName string) {
	m.modelName = modelName
	m.screen = screenChat
	m.rows = nil
	m.list = list.New()
	m.phase = phaseIdle
	m.tokens = 0
	m.stick = true

	greet := newMsgItem(roleAssistant)
	greet.text = "Hi! I'm a mock " + modelName + " — every reply here is canned, but the " +
		"streaming, tool cards, spinner, and the context gauge are all real component code. " +
		"Ask me anything; try \"run the tests\" or \"show me a kitty image\"."
	greet.done = true
	m.appendItem(greet)
	m.layout()
	m.updateGauge()
}

// appendItem adds a transcript block and keeps the view pinned to the bottom when
// the user hasn't scrolled away.
func (m *model) appendItem(it list.Item) {
	m.rows = append(m.rows, it)
	m.list.SetItems(m.rows...)
	if m.stick {
		m.list.GotoBottom()
	}
}

func (m *model) setPhase(p chatPhase) { m.phase = p; m.phaseAt = m.frame }

// advance drives the mocked turn forward one frame. Called from the tick handler.
func (m *model) advance() {
	if m.screen != screenChat || m.phase == phaseIdle {
		return
	}
	elapsed := m.frame - m.phaseAt
	switch m.phase {
	case phaseThinking:
		if elapsed >= thinkFrames {
			if m.reply.tool != nil && m.curTool == nil {
				m.curTool = newToolItem(m.reply.tool.name, m.reply.tool.arg)
				m.appendItem(m.curTool)
				m.setPhase(phaseTool)
			} else {
				m.beginStream()
			}
		}
	case phaseTool:
		m.curTool.frame = m.frame // animate the card's spinner
		m.curTool.Bump()
		if m.stick {
			m.list.GotoBottom()
		}
		if elapsed >= toolFrames {
			m.curTool.complete(m.reply.tool.result)
			m.beginStream()
		}
	case phaseStreaming:
		if elapsed%wordEvery != 0 {
			return
		}
		if len(m.stream) == 0 {
			m.cur.done = true
			m.cur.Bump()
			m.finishTurn()
			return
		}
		w := m.stream[0]
		m.stream = m.stream[1:]
		if m.cur.text != "" {
			m.cur.text += " "
		}
		m.cur.text += w
		m.cur.Bump()
		if m.stick {
			m.list.GotoBottom()
		}
	}
}

func (m *model) beginStream() {
	m.cur = newMsgItem(roleAssistant)
	m.appendItem(m.cur)
	m.stream = strings.Fields(m.reply.text)
	m.setPhase(phaseStreaming)
}

func (m *model) finishTurn() {
	m.phase = phaseIdle
	// Mock token accounting so the context gauge visibly climbs each turn.
	m.tokens += 5_000 + len(strings.Fields(m.reply.text))*40
	if m.reply.tool != nil {
		m.tokens += 1_500
	}
	m.updateGauge()
	m.reply, m.cur, m.curTool = nil, nil, nil
}

// --- input ----------------------------------------------------------------

func (m *model) chatKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.showKitty {
		// Any key dismisses the cat-image popup.
		m.showKitty = false
		return m, nil
	}
	if m.showCaps {
		// Any key dismisses the capability overlay.
		m.showCaps = false
		return m, nil
	}
	switch msg.String() {
	case "ctrl+n":
		m.screen = screenPicker
		return m, nil
	case "ctrl+g":
		m.showCaps = true
		return m, nil
	case "ctrl+t":
		theme.Cycle()
		return m, nil
	case "enter":
		return m.submit()
	case "backspace":
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
		return m, nil
	case "up":
		// Note: no j/k bindings here — this is a text input, so letters must
		// reach the prompt. Scrolling is arrows + pgup/pgdown only.
		m.list.ScrollBy(-1)
		m.stick = m.list.AtBottom()
		return m, nil
	case "down":
		m.list.ScrollBy(1)
		m.stick = m.list.AtBottom()
		return m, nil
	case "pgup":
		m.list.ScrollBy(-m.list.Height())
		m.stick = m.list.AtBottom()
		return m, nil
	case "pgdown":
		m.list.ScrollBy(m.list.Height())
		m.stick = m.list.AtBottom()
		return m, nil
	case "space":
		m.input += " "
		return m, nil
	default:
		if msg.Text != "" { // printable input
			m.input += msg.Text
		}
		return m, nil
	}
}

func (m *model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input)
	if text == "" || m.phase != phaseIdle {
		return m, nil
	}
	m.input = ""
	m.stick = true

	um := newMsgItem(roleUser)
	um.text, um.done = text, true
	m.appendItem(um)
	m.tokens += 200 + len([]rune(text))/3
	m.turn++
	m.reply = scriptedReply(m.turn, text)
	m.setPhase(phaseThinking)

	// "show me a kitty image" → pop the big cat image. The image transmission is
	// returned as a tea.Raw command (the cell renderer drops APC in View content),
	// batched with the motion tick.
	var rawCmd tea.Cmd
	low := strings.ToLower(text)
	if strings.Contains(low, "kitty") || strings.Contains(low, "image") || strings.Contains(low, "cat") {
		rawCmd = m.showKittyImage()
	}
	return m, tea.Batch(m.ensureTick(), rawCmd)
}
