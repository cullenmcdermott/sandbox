package main

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// chatChrome is the number of non-transcript rows: header(2) + status(1) +
// input(3) + footer(1).
const chatChrome = 7

func (m *model) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	bodyH := m.height - chatChrome
	if bodyH < 1 {
		bodyH = 1
	}
	m.list.SetSize(m.width-2, bodyH) // -2 leaves a column for the scrollbar
}

func (m *model) chatView() string {
	base := strings.Join([]string{
		m.chatHeader(),
		m.transcript(),
		m.statusLine(),
		m.inputBox(),
		m.footer(),
	}, "\n")

	switch {
	case m.showKitty:
		// IMPORTANT: the cat popup must NOT go through lipgloss.Canvas. The
		// compositor reparses content into a cell grid, which destroys the Kitty
		// placeholder cells (U+10EEEE + zero-width placement diacritics) so the
		// image never renders. Center it with plain line math instead, leaving the
		// placeholder escapes byte-for-byte intact.
		return m.centered(m.kittyPopupBox())
	case m.showCaps:
		return m.overlay(base, m.capsBox())
	default:
		return base
	}
}

// centered places box in the middle of the screen using line-based padding only
// (no cell-grid compositing), so Kitty graphics placeholders survive untouched.
func (m *model) centered(box string) string {
	lines := strings.Split(box, "\n")
	left := max((m.width-lipgloss.Width(box))/2, 0)
	top := max((m.height-len(lines))/2, 0)
	pad := strings.Repeat(" ", left)
	out := make([]string, 0, m.height)
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	for _, ln := range lines {
		out = append(out, pad+ln)
	}
	return strings.Join(out, "\n")
}

// overlay floats box over base, centered, with a drop shadow — the z-ordered
// layer/compositor pattern, shared by the caps panel and the cat popup.
func (m *model) overlay(base, box string) string {
	bg := m.st.page.Width(m.width).Height(m.height).Render(base)
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	bx, by := max((m.width-bw)/2, 0), max((m.height-bh)/2, 0)
	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(bg).X(0).Y(0).Z(0),
		lipgloss.NewLayer(solidBlock(bw, bh, theme.Shadow)).X(bx+2).Y(by+1).Z(1),
		lipgloss.NewLayer(box).X(bx).Y(by).Z(2),
	))
	return canvas.Render()
}

// chatHeader is the wordmark + chosen model on the left and the context gauge on
// the right, over a gradient rule.
func (m *model) chatHeader() string {
	word := theme.GradientText("tuikit", true, theme.Charple, theme.Hazy, theme.Dolly)
	left := word + "  " + theme.MarkClaudeStyled() + " " +
		lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(m.modelName)
	right := m.ctxReadout()

	top := placeLR(left, right, m.width)
	rule := kit.SectionHeader("", m.width)
	return top + "\n" + rule
}

func (m *model) transcript() string {
	sb := kit.Scrollbar(m.list.Height(), m.list.TotalHeight(), m.list.Height(), m.list.Offset())
	return lipgloss.JoinHorizontal(lipgloss.Top, m.list.Render(), " ", sb)
}

// statusLine shows the live turn state with a spinner, or an idle hint.
func (m *model) statusLine() string {
	muted := lipgloss.NewStyle().Foreground(theme.TextMuted)
	busy := lipgloss.NewStyle().Foreground(theme.Busy)
	spin := theme.SpinnerFrame(m.frame)
	switch m.phase {
	case phaseThinking:
		return spin + " " + busy.Render("thinking"+anim.Ellipsis(m.frame/4))
	case phaseTool:
		name := ""
		if m.reply != nil && m.reply.tool != nil {
			name = " " + m.reply.tool.name
		}
		return spin + " " + busy.Render("running"+name+anim.Ellipsis(m.frame/4))
	case phaseStreaming:
		return spin + " " + busy.Render("streaming"+anim.Ellipsis(m.frame/4))
	default:
		return muted.Render("ready · type a message, ↵ to send")
	}
}

// inputBox is a rounded-border prompt framing the editable input line.
func (m *model) inputBox() string {
	prompt := lipgloss.NewStyle().Foreground(theme.Charple).Bold(true).Render("❯ ")
	cursor := lipgloss.NewStyle().Foreground(theme.Busy).Render("▏")
	line := prompt + lipgloss.NewStyle().Foreground(theme.TextBright).Render(m.input) + cursor
	return kit.Card(kit.CardOpts{Content: line, BorderColor: theme.BorderMedium, Width: m.width})
}

func (m *model) footer() string {
	// Keep the footer to a single line: a wrapped footer would overflow the
	// height budget (chatChrome assumes 1 row). MaxHeight(1) clips defensively
	// on very narrow terminals; the hint set is chosen to fit ~72 columns.
	return m.st.footer.MaxHeight(1).Render(kit.KbdRow(
		[2]string{"↵", "send"},
		[2]string{"ctrl+t", "theme"},
		[2]string{"ctrl+g", "caps"},
		[2]string{"ctrl+n", "model"},
		[2]string{"ctrl+c", "quit"},
	))
}

// placeLR lays out left and right on a single line of the given width, with the
// right segment flush to the right edge.
func placeLR(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}
