package dashboard

// permqueue.go — the pending-permission queue view (slice 5e / Mockup C). A
// dedicated queue of every session waiting for approval. Pressing `q` from the
// dashboard opens it; approve/deny advances to the next item; the queue closes
// when empty.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// permQueueModel holds the state of the pending-permission queue view.
type permQueueModel struct {
	open bool
	sel  int // cursor index into permQueueItems
}

// openPermQueue opens the permission queue.
func (m *Model) openPermQueue() {
	m.permQueue = permQueueModel{open: true}
}

// closePermQueue closes the permission queue.
func (m *Model) closePermQueue() {
	m.permQueue = permQueueModel{}
}

// permQueueItems returns the sessions with pending permissions in display order.
func (m *Model) permQueueItems() []Session {
	var out []Session
	for _, s := range m.sessions {
		if s.DashStatus == StatusWaiting && s.PendingPermissionID != "" {
			out = append(out, s)
		}
	}
	return out
}

// renderPermQueue builds the queue overlay string for the given width.
func (m *Model) renderPermQueue(w int) string {
	items := m.permQueueItems()
	boxW := w - 16
	if boxW < 40 {
		boxW = 40
	}
	if boxW > 70 {
		boxW = 70
	}

	// Section header: title on the left, a flat rule, and the waiting count
	// right-aligned to the box edge (§B.2).
	title := lipgloss.NewStyle().Foreground(theme.Gold).Bold(true).Render("Pending permissions")
	header := kit.SectionHeader(title, boxW, fmt.Sprintf("%d waiting", len(items)))
	sub := kit.KbdRow([2]string{"a", "approve"}, [2]string{"d", "deny"}, [2]string{"j", "next"}, [2]string{"k", "prev"}, [2]string{"q", "close"})
	lines := []string{header, sub}
	if len(items) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.Guac).Render("✦ all clear"))
	} else {
		for i, s := range items {
			sel := i == m.permQueue.sel
			glyph := lipgloss.NewStyle().Foreground(theme.Gold).Render(theme.GlyphWaiting)
			header := glyph + " " + lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(s.Title)
			note := ""
			if s.PendingPermissionTool != "" {
				note = "wants: " + s.PendingPermissionTool
			} else {
				note = s.State.Backend + " · " + filepathBaseLocal(s.State.ProjectPath)
			}
			detail := lipgloss.NewStyle().Foreground(theme.TextMuted).Render("  " + note)
			if sel {
				header = lipgloss.NewStyle().Foreground(theme.Guac).Render(glyphChevron+" ") + header
				lines = append(lines,
					lipgloss.NewStyle().Background(theme.Raised2).Width(boxW).Render(header),
					lipgloss.NewStyle().Background(theme.Raised2).Width(boxW).Render(detail),
				)
			} else {
				lines = append(lines, "  "+header, detail)
			}
		}
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Gold).
		Background(theme.Surface).
		Width(boxW).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// permQueueKey handles a key event while the permission queue is open.
func (m *Model) permQueueKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	k := msg.String()
	switch k {
	case "q", "esc":
		m.closePermQueue()
		return nil, true
	case "a":
		return m.resolveQueueHead(true), true
	case "d":
		return m.resolveQueueHead(false), true
	case "j", "down":
		items := m.permQueueItems()
		if m.permQueue.sel < len(items)-1 {
			m.permQueue.sel++
		}
		return nil, true
	case "k", "up":
		if m.permQueue.sel > 0 {
			m.permQueue.sel--
		}
		return nil, true
	}
	return nil, false
}

// resolveQueueHead resolves the selected pending permission in the queue.
func (m *Model) resolveQueueHead(allow bool) tea.Cmd {
	items := m.permQueueItems()
	if len(items) == 0 {
		m.closePermQueue()
		return nil
	}
	sel := m.permQueue.sel
	if sel < 0 || sel >= len(items) {
		sel = 0
	}
	return m.approveCmd(items[sel], allow)
}
