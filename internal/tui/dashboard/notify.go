package dashboard

// notify.go — cross-session "needs you" notifications (slice 5c / Mockup C).
// When a background session transitions to waiting or needs-input while the
// user is in a chat modal, a toast slides in over the dashboard. The user can
// press ctrl+g to jump to the next session that needs attention.

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// toastSlide is the ease-out window for the toast sliding in and back out (§C.3).
const toastSlide = 200 * time.Millisecond

// toastDismissAfter is how long a notification toast stays visible.
const toastDismissAfter = 8 * time.Second

// toastFade is the slow opacity fade-out window at the end of a toast's life
// (T16). It's deliberately longer than toastSlide so the toast dissolves rather
// than snapping away.
const toastFade = 1500 * time.Millisecond

// toastBaseAlpha is the steady-state opacity of a toast: slightly translucent so
// it reads as an overlay floating above the content rather than an opaque box.
const toastBaseAlpha = 0.88

// toastSlideInterval is the refresh cadence of the slide-in animation.
const toastSlideInterval = 50 * time.Millisecond

// notification represents a transient cross-session attention toast.
type notification struct {
	sessionID session.ID
	title     string
	note      string
	status    SessionStatus
	createdAt time.Time
}

// toastMsg is delivered when a background session requires attention.
type toastMsg struct {
	id     session.ID
	title  string
	note   string
	status SessionStatus
}

// toastTickMsg drives the toast slide-in animation and auto-dismiss.
type toastTickMsg struct{}

// notifyIfBackgroundAttention checks whether any session other than the
// attached one just became waiting/needs-input and emits a toastMsg.
func (m *Model) notifyIfBackgroundAttention(attached session.ID) tea.Cmd {
	for _, s := range m.sessions {
		if s.ID() == attached {
			continue
		}
		if s.DashStatus != StatusWaiting && s.DashStatus != StatusNeedsInput {
			continue
		}
		// Only toast once per session per attention state: if we already have
		// a toast for it, skip.
		if m.toast != nil && m.toast.sessionID == s.ID() {
			continue
		}
		note := ClientLabel(s.State.Backend) + " · " + filepathBaseLocal(s.State.ProjectPath)
		if g := BackendGlyph(s.State.Backend); g != "" {
			note = g + " " + note // uncolored so it fades with the toast
		}
		if s.DashStatus == StatusWaiting && s.PendingPermissionTool != "" {
			note = "wants: " + s.PendingPermissionTool
		}
		return func() tea.Msg {
			return toastMsg{id: s.ID(), title: s.Title, note: note, status: s.DashStatus}
		}
	}
	return nil
}

// renderToast builds the toast string for the current frame. It slides in from
// the right edge during the first few frames.
func (m *Model) renderToast(w int) string {
	if m.toast == nil {
		return ""
	}
	t := m.toast
	pulse := []string{"·", "•", "●", "•"}[m.spinnerFrame%4]
	glyph := theme.GlyphWaiting
	if t.status == StatusNeedsInput {
		glyph = theme.GlyphNeedsInput
	}

	// Opacity: steady at toastBaseAlpha, then fades slowly to 0 over the last
	// toastFade window so the toast dissolves into the background (T16). Simulated
	// in the terminal by blending every color toward the page background — under
	// reduce-motion we keep it fully opaque (no fade).
	alpha := toastBaseAlpha
	if !anim.ReduceMotion() {
		if remaining := toastDismissAfter - time.Since(t.createdAt); remaining < toastFade {
			frac := float64(remaining) / float64(toastFade)
			if frac < 0 {
				frac = 0
			}
			alpha *= frac
		}
	} else {
		alpha = 1
	}
	faded := func(c color.Color) color.Color { return anim.LerpColor(theme.Page, c, alpha) }

	line := lipgloss.NewStyle().Foreground(faded(theme.Gold)).Bold(true).
		Render(fmt.Sprintf("%s %s %s", pulse, glyph, t.title)) +
		lipgloss.NewStyle().Foreground(faded(theme.TextBody)).Render(" needs you") +
		lipgloss.NewStyle().Foreground(faded(theme.TextDim)).Render("  ·  ") +
		lipgloss.NewStyle().Foreground(faded(theme.Malibu)).Render("⌃G jump")
	sub := lipgloss.NewStyle().Foreground(faded(theme.TextMuted)).Render("  " + t.note)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(faded(theme.Gold)).
		Background(faded(theme.Raised)).
		Padding(0, 1).
		Render(line + "\n" + sub)

	// Eased slide in from 8 columns off-screen, and back out as the toast nears
	// dismissal (§C.3 toast in/out), both driven by the motion engine.
	tw := lipgloss.Width(firstLineOf(box))
	targetX := w - tw - 2
	if targetX < 0 {
		targetX = 0
	}
	const slideCols = 8
	age := time.Since(t.createdAt)
	off := 0.0
	if !anim.ReduceMotion() {
		// Eased slide-in, then slide-out as the toast nears dismissal (§C.3).
		// Under reduce-motion both collapse to the settled end state (off=0).
		off = (1 - anim.Progress(age, toastSlide)) * slideCols
		if remaining := toastDismissAfter - age; remaining < toastSlide {
			off += anim.Progress(toastSlide-remaining, toastSlide) * slideCols
		}
	}
	x := targetX + int(off+0.5)
	if x < 0 {
		x = 0
	}
	if x > w-1 {
		x = w - 1
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Repeat(" ", x) + box)
}

// firstLineOf returns the first line of a multi-line string.
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// jumpToNextNeedingAttention moves the cursor to the next session that needs
// attention (waiting / needs-input), wrapping around the list. Returns the
// selected session or nil if none need attention.
func (m *Model) jumpToNextNeedingAttention() *Session {
	visible := m.visibleSessions()
	if len(visible) == 0 {
		return nil
	}
	start := m.cursor
	if start < 0 || start >= len(visible) {
		start = 0
	}
	for offset := 1; offset <= len(visible); offset++ {
		idx := (start + offset) % len(visible)
		s := visible[idx]
		if s.DashStatus == StatusWaiting || s.DashStatus == StatusNeedsInput {
			m.cursor = idx
			return &s
		}
	}
	return nil
}

// toastTickCmd schedules the next toast animation frame.
func toastTickCmd() tea.Cmd {
	return tea.Tick(toastSlideInterval, func(time.Time) tea.Msg {
		return toastTickMsg{}
	})
}
