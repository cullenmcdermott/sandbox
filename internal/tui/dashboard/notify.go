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
// (waiting / needs-input / failed). It is edge-triggered, not level-triggered: a session
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
		if !needsAttention(s) {
			// Left attention — forget it so a later attention episode re-notifies.
			// This runs even during catch-up (below): a permission RESOLVED in a
			// replay burst must end its episode, so a DIFFERENT permission
			// requested later in the same burst is a fresh episode that toasts
			// once at the flip-to-live — not masked by the stale entry.
			delete(m.notifiedAttention, id)
			continue
		}
		if id == attached {
			continue
		}
		if s.catchingUp {
			// A background stream is replaying history (§1a step 3): the state is
			// applied, but do NOT toast for a replayed attention state, and do NOT
			// mark notifiedAttention — leave that to the single flip-to-live scan
			// (when catchingUp clears at EventStreamLive) so it makes one honest
			// decision on the session's FINAL post-replay state. The leave-episode
			// delete above still runs during the burst.
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

// autopilotToast surfaces an autopilot driver's termination — goal reached, loop
// finished, or a silent lapse when the pod suspended — as a cross-session toast +
// OS notification, reusing the toastMsg plumbing (§1e items 1–3) so a driver that
// ends while the user is on the dashboard is never invisible. It pre-marks the
// session notified so the generic attention pass in this same Update tick doesn't
// also emit a plain "needs you" toast that would clobber this more specific one.
func (m *Model) autopilotToast(id session.ID, note string) tea.Cmd {
	if m.notifiedAttention == nil {
		m.notifiedAttention = make(map[session.ID]bool)
	}
	m.notifiedAttention[id] = true
	title := m.sessionByID(id).DisplayTitle()
	if title == "" {
		title = string(id)
	}
	toast := toastMsg{id: id, title: title, note: note, status: StatusNeedsInput}
	return func() tea.Msg { return toast }
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
	switch t.status {
	case StatusNeedsInput:
		glyph = theme.GlyphNeedsInput
	case StatusFailed:
		glyph = theme.GlyphFailed
	}

	// Opacity: steady at toastBaseAlpha, then fades slowly to 0 over the last
	// toastFade window so the toast dissolves into the background (T16). Simulated
	// in the terminal by blending every color toward the page background — under
	// reduce-motion we keep it fully opaque (no fade).
	alpha := toastBaseAlpha
	if !anim.ReduceMotion() {
		if remaining := toastDismissAfter - nowFunc().Sub(t.createdAt); remaining < toastFade {
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
	age := nowFunc().Sub(t.createdAt)
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
// attention, wrapping around the list. Returns the
// selected session or nil if none need attention.
func (m *Model) jumpToNextNeedingAttention() *Session {
	// Scan the flat, filtered+sorted session list (visibleSessions) forward from
	// the current selection — resolved by session *identity*, never by treating the
	// row cursor as a session index — for the next session that needs attention and
	// isn't already attached. Then translate the target back into the ONE row model
	// (visibleRows): expand its group first in group view so its row exists, and
	// move the row cursor onto that row (§1b — a raw session index must never be
	// stuffed into a display-row cursor).
	visible := m.visibleSessions()
	if len(visible) == 0 {
		return nil
	}
	start := 0
	if sel := m.selectedSession(); sel != nil {
		for i, s := range visible {
			if s.ID() == sel.ID() {
				start = i
				break
			}
		}
	}
	for offset := 1; offset <= len(visible); offset++ {
		idx := (start + offset) % len(visible)
		s := visible[idx]
		if s.ID() == m.attachedID || !needsAttention(s) {
			continue
		}
		if m.groupView.open && m.groupView.repos != nil {
			m.groupView.repos[repoKey(s)] = true
		}
		rows := m.visibleRows()
		for rowIdx, row := range rows {
			if row.kind == rowSession && row.session.ID() == s.ID() {
				m.cursor = rowIdx
				return row.session
			}
		}
		// Fail closed rather than return a session without having moved the row
		// cursor: a future hidden/archived rowKind could make visibleRows() drop a
		// session visibleSessions() still lists, and silently returning it here would
		// reintroduce the exact stale-row-cursor bug §1b fixed.
		return nil
	}
	return nil
}

// toastTickCmd schedules the next toast animation frame.
func toastTickCmd() tea.Cmd {
	return tea.Tick(toastSlideInterval, func(time.Time) tea.Msg {
		return toastTickMsg{}
	})
}
