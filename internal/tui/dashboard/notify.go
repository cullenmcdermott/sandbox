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

// notifyIfBackgroundAttention emits at most one toastMsg for a background session
// (one other than the attached one) that has just ENTERED an attention state
// (waiting / needs-input). It is edge-triggered, not level-triggered: a session
// is notified once per attention episode and not again until it leaves and
// re-enters attention. Without this, every SSE event re-toasted a still-waiting
// session (and re-fired its OS notification) the moment its 8s toast expired, and
// ≥2 waiting sessions ping-ponged the toast on each event.
func (m *Model) notifyIfBackgroundAttention(attached session.ID) tea.Cmd {
	if m.notifiedAttention == nil {
		m.notifiedAttention = make(map[session.ID]bool)
	}
	var cmd tea.Cmd
	for _, s := range m.sessions {
		id := s.ID()
		if id == attached {
			continue
		}
		if s.DashStatus != StatusWaiting && s.DashStatus != StatusNeedsInput {
			// Left attention — forget it so a later attention episode re-notifies.
			delete(m.notifiedAttention, id)
			continue
		}
		if m.notifiedAttention[id] {
			continue // already notified this episode; don't re-fire
		}
		// Mark every concurrently-waiting session seen (so they don't each toast in
		// turn on the next event — ⌃G cycles through them), but surface only the
		// first as a toast.
		m.notifiedAttention[id] = true
		if cmd != nil {
			continue
		}
		note := ClientLabel(s.State.Backend) + " · " + filepathBaseLocal(s.State.ProjectPath)
		if g := BackendGlyph(s.State.Backend); g != "" {
			note = g + " " + note // uncolored so it fades with the toast
		}
		if s.DashStatus == StatusWaiting && s.PendingPermissionTool != "" {
			note = "wants: " + s.PendingPermissionTool
		}
		toast := toastMsg{id: id, title: s.DisplayTitle(), note: note, status: s.DashStatus}
		cmd = func() tea.Msg { return toast }
	}
	return cmd
}

// renderToast builds the toast box and the column it should sit at for the
// current frame (it slides in from the right edge during the first few frames).
// The caller composites the box as a single layer at that column — see
// App.withToast — so the whole multi-line box moves together. (Prepending spaces
// to the box string would only indent line 0, shearing the box: its top border
// would land at the right while the body and bottom border collapsed to column
// 0.) Returns ("", 0) when there is no toast.
func (m *Model) renderToast(w int) (string, int) {
	if m.toast == nil {
		return "", 0
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
	return box, x
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
		if s.ID() == m.attachedID {
			continue // already viewing it — jumping here would be a no-op detach/attach
		}
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
