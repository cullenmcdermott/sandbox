package dashboard

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// animTickMsg drives the single gated motion loop (§C.2): one ~30fps tick that
// re-renders so every time-based interpolation (spinner hue, glyph fade-in)
// advances. It is only scheduled while anyMotionActive and self-stops otherwise.
type animTickMsg struct{}

// --------------------------------------------------------------------------
// Spinner frames
// --------------------------------------------------------------------------

// animFPS is the cadence of the single motion tick (~30fps). spinnerSubRate is
// how many ticks pass between busy-glyph advances, preserving the ~200ms spinner
// cadence off the faster shared loop.
const (
	animFPS        = 33 * time.Millisecond
	spinnerSubRate = 6
)

// newHelp builds a bubbles help.Model styled with the charmtone palette so the
// footer and `?` overlay match the rest of the dashboard.
func newHelp() help.Model {
	h := help.New()
	h.ShortSeparator = "  ·  "
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(theme.Malibu)
	// Desc was theme.TextDim (#46406A) on theme.Surface — effectively dark-on-dark
	// and unreadable (T4). Use body text for the footer hints.
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(theme.TextBody)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(theme.BorderMedium)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(theme.Malibu)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(theme.TextSecondary)
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(theme.BorderMedium)
	h.Styles.Ellipsis = lipgloss.NewStyle().Foreground(theme.TextDim)
	return h
}

// animCmd schedules the next motion tick.
func (m *Model) animCmd() tea.Cmd {
	return tea.Tick(animFPS, func(time.Time) tea.Msg { return animTickMsg{} })
}

// rowMotionActive reports whether any row changed status recently enough that
// its glyph fade-in or status-flash is still in flight. The window is the longer
// of the two (statusFlashDur ≥ theme.FadeDuration) so the loop covers both.
func (m *Model) rowMotionActive() bool {
	for i := range m.sessions {
		if t := m.sessions[i].statusChangedAt; !t.IsZero() && nowFunc().Sub(t) < statusFlashDur {
			return true
		}
	}
	return false
}

// anyMotionActive reports whether any motion is in flight (§C.2): a running
// busy spinner (tracked through the engine) or a row mid-fade/flash. When it is
// false the single tick loop stops scheduling itself.
func (m *Model) anyMotionActive() bool {
	m.engine.SetSpinners(m.countStatus(StatusBusy))
	return m.engine.AnyMotionActive(nowFunc()) || m.rowMotionActive()
}

// maybeStartAnim starts the single gated motion tick loop if motion is active
// and the loop is not already running. Returns nil otherwise, so the dashboard
// schedules no timer while nothing is moving.
func (m *Model) maybeStartAnim() tea.Cmd {
	if !m.animating && m.anyMotionActive() {
		m.animating = true
		return m.animCmd()
	}
	return nil
}

func (m *Model) render() string {
	if m.width == 0 {
		return "loading…\n"
	}

	if m.showHelp {
		overlay := m.renderHelp()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			pageWhitespace(),
		)
	}

	if m.convert != nil {
		overlay := m.renderConvertModal()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			pageWhitespace(),
		)
	}

	if m.confirm != nil {
		overlay := m.renderConfirm()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			pageWhitespace(),
		)
	}

	zoned := m.renderZoned(m.width, m.height)
	if !m.switcher.open && !m.renaming {
		return zoned
	}

	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(zoned).X(0).Y(0).Z(0),
	))

	if m.switcher.open {
		sw := m.renderSwitcher(m.width)
		swW := lipgloss.Width(firstLineOf(sw))
		swH := strings.Count(sw, "\n") + 1
		if swW > m.width {
			swW = m.width
		}
		if swH > m.height {
			swH = m.height
		}
		sx := (m.width - swW) / 2
		sy := (m.height - swH) / 2
		shadow := solidBlock(swW, swH, theme.Shadow)
		canvas.Compose(lipgloss.NewCompositor(
			lipgloss.NewLayer(shadow).X(sx+2).Y(sy+1).Z(8),
			lipgloss.NewLayer(sw).X(sx).Y(sy).Z(9),
		))
	}

	if m.renaming {
		overlay := m.renderRenameOverlay(m.width)
		canvas.Compose(lipgloss.NewCompositor(
			lipgloss.NewLayer(solidBlock(m.width, 3, theme.Shadow)).X(4).Y(m.height/3+1).Z(9),
			lipgloss.NewLayer(overlay).X(2).Y(m.height/3).Z(10),
		))
	}

	return canvas.Render()
}

// renderRowLines produces one string per row for the session list, including
// group headers when group view is enabled.
// renderRowLines lays the display rows into at most `height` physical lines.
// Session rows are two physical lines each; group headers are one. It returns the
// rendered lines plus the count of display rows fully shown (so the caller can
// summarize the overflow). Group headers and skeletons count as one line.
// seedErrorLines renders the actionable failure state shown when the initial
// cluster seed failed (e.g. the cluster is unreachable), so the list shows a
// real error + retry affordance instead of skeleton bars forever.
func (m *Model) seedErrorLines(width int) []string {
	msg := "can't reach the cluster"
	if m.seedErr != nil {
		msg += ": " + m.seedErr.Error()
	}
	lines := []string{""}
	lines = append(lines, strings.Split(kit.ErrorBlock(msg, "", ""), "\n")...)
	lines = append(lines, "")
	lines = append(lines, "  "+kit.KbdRow([2]string{"r", "retry"}, [2]string{"q", "quit"}))
	return lines
}

func (m *Model) renderRowLines(rows []listRow, width, height int) ([]string, int) {
	if len(rows) == 0 {
		// U2: three-way state branch so "loading", "empty cluster", and
		// "filter matched nothing" are visually distinct (spec 04-ux-responsiveness §U2).
		activeFilter := m.filter
		if m.filtering {
			activeFilter = m.filterBuf
		}
		switch {
		case m.seedErr != nil:
			// Initial seed failed (e.g. unreachable cluster): show the error and a
			// retry hint instead of skeleton bars forever.
			return m.seedErrorLines(width), 0
		case !m.seeded:
			// Still loading the seed: show skeleton bars at list rhythm.
			n := height
			if n > 8 {
				n = 8
			}
			if n < 1 {
				n = 1
			}
			return strings.Split(skeletonRows(n, width), "\n"), 0
		case activeFilter != "":
			// Filter active but matched nothing.
			return []string{noMatchCopy(activeFilter)}, 0
		case len(m.sessions) == 0:
			// Seeded, no filter, genuinely empty cluster: first-run CTA.
			return strings.Split(m.firstRunView(width, height), "\n"), 0
		default:
			// Seeded, has sessions, but all filtered out — safety net.
			return []string{styleEmpty.Render("  No matches.")}, 0
		}
	}

	top := m.rowScrollTop(rows, height)
	lines := make([]string, 0, height)
	shown := 0
	for i := top; i < len(rows) && len(lines) < height; i++ {
		var rl []string
		if rows[i].kind == rowSession {
			rl = strings.Split(m.renderSessionRow(*rows[i].session, i == m.cursor, width), "\n")
		} else {
			rl = []string{m.renderGroupHeader(rows[i].repo, width)}
		}
		if len(lines)+len(rl) > height {
			break // a partially-visible row counts as hidden (summarized below)
		}
		lines = append(lines, rl...)
		shown = i + 1
	}
	return lines, shown
}

// rowHeight is the physical line count for a display row: two lines per session
// (primary + dim sub-line), one for a group header.
func rowHeight(r listRow) int {
	if r.kind == rowSession {
		return 2
	}
	return 1
}

// rowScrollTop returns the first visible display-row index so the cursor row is
// fully visible within `height` physical lines, anchoring the cursor toward the
// bottom when scrolling down (matching the old single-line viewport behavior).
func (m *Model) rowScrollTop(rows []listRow, height int) int {
	cur := m.cursor
	if cur < 0 {
		cur = 0
	}
	if cur >= len(rows) {
		cur = len(rows) - 1
	}
	top := cur
	used := rowHeight(rows[cur])
	for top > 0 {
		ph := rowHeight(rows[top-1])
		if used+ph > height {
			break
		}
		used += ph
		top--
	}
	return top
}

// renderSessionRow renders one session as two physical lines joined by "\n" (the
// row spec in docs/archive/dashboard-redesign.md):
//
//	line 1: selection-bar(2) attention-dot(2) status-glyph(2) title(flex) right-aligned relTime
//	line 2 (dim, indented): "<model>·<client>  <short-id>  <ctx%>  [⚠ if failed]"
//
// Two lines disambiguate identical titles and fit model/ctx without crowding (P2/P4).
func (m *Model) renderSessionRow(s Session, selected bool, width int) string {
	// Selection bar.
	var bar string
	if selected {
		bar = styleSelectionBar.Render(theme.GlyphSelBar) + " "
	} else {
		bar = "  "
	}

	// Glyph: busy rows use the pre-rendered gradient spinner (P4); pending-action
	// rows also show a spinner so the user sees in-progress feedback (U3).
	var glyphRendered string
	if s.DashStatus == StatusBusy || s.PendingAction != "" {
		glyphRendered = theme.SpinnerFrame(m.spinnerFrame) + " "
	} else {
		gcol := theme.FadeColor(glyphColor(s.DashStatus), s.statusChangedAt)
		glyphRendered = lipgloss.NewStyle().Foreground(gcol).Render(s.DashStatus.Glyph()) + " "
	}

	// relTime: last-active, falling back to created-age so it's never a bare "—" (P3).
	relTime := styleRelTime.Render(rowRelTime(s))

	// Attention dot (D4): a colored ● for waiting/needs-input/failed rows; two
	// spaces otherwise, so the fixed-width layout never shifts.
	dot := attentionDot(s)
	var dotSlot string
	if dot != "" {
		dotSlot = dot + " "
	} else {
		dotSlot = "  "
	}

	// Line 1 title fills the space between the glyph and the right-aligned relTime.
	fixedW := 2 + 2 + 2 + 1 + lipgloss.Width(relTime)
	titleW := width - fixedW
	if titleW < 4 {
		titleW = 4
	}
	title := padRight(truncate(s.DisplayTitle(), titleW), titleW)

	var rowStyle lipgloss.Style
	if selected {
		rowStyle = styleRowSelected.Width(width)
	} else {
		rowStyle = styleRow.Width(width)
		// Status-flash: briefly pulse the row background toward the new status'
		// accent right after a status change (§C.3), drawing the eye to it.
		if bg, ok := flashBg(theme.Page, glyphColor(s.DashStatus), s.statusChangedAt); ok {
			rowStyle = rowStyle.Background(bg)
		}
		// Row-enter: a freshly-created row fades its title text in (§C.3). A
		// foreground blend (not a slide) so the fixed-width layout never shifts.
		if e := rowEnter(s.State.CreatedAt); e < 1 {
			rowStyle = rowStyle.Foreground(anim.LerpColor(theme.TextDim, theme.TextBody, e))
		}
	}
	line1 := rowStyle.Render(bar + dotSlot + glyphRendered + title + " " + relTime)

	// Line 2 (Layout A): the colored agent glyph in the gutter, then the colored
	// status word, then a dim "·"-joined tail of what-it's-doing / where-it-lives
	// / lifecycle context (see Session.sublineParts). The status word carries its
	// own status accent; the rest is dim so the eye lands on agent + state first.
	var subBg color.Color
	if selected {
		subBg = theme.Raised
	}
	// Brand mark in the gutter so every row is identifiable by agent at a glance.
	// It is composed as its own styled cell (kept out of truncate, which is not
	// ANSI-aware) and carries the row background so a selected row fills flush.
	gutter := lipgloss.NewStyle().Background(subBg).Render("      ") // 6 cols under bar+dot+glyph
	if glyph := BackendGlyph(s.State.Backend); glyph != "" {
		gcol, _ := BackendColor(s.State.Backend)
		markCell := lipgloss.NewStyle().Foreground(gcol).Background(subBg).Render(glyph)
		gutter = lipgloss.NewStyle().Background(subBg).Render("  ") + markCell +
			lipgloss.NewStyle().Background(subBg).Render("   ")
	}

	avail := max(4, width-6)
	statusWord := statusLabel(s.DashStatus)
	statusStyle := lipgloss.NewStyle().Foreground(glyphColor(s.DashStatus))
	dimStyle := lipgloss.NewStyle().Foreground(theme.TextMuted)
	if subBg != nil {
		statusStyle = statusStyle.Background(subBg)
		dimStyle = dimStyle.Background(subBg)
	}
	rest := ""
	if parts := s.sublineParts(); len(parts) > 0 {
		rest = truncate(" · "+strings.Join(parts, " · "), max(0, avail-lipgloss.Width(statusWord)))
	}
	used := lipgloss.Width(statusWord) + lipgloss.Width(rest)
	pad := ""
	if used < avail {
		pad = strings.Repeat(" ", avail-used)
	}
	body := statusStyle.Render(statusWord) + dimStyle.Render(rest+pad)
	line2 := gutter + body

	return line1 + "\n" + line2
}

// rowRelTime is the row's relative-time string: last-active, or created-age when
// the session has never reported activity, so the column is never a bare "—" (P3).
func rowRelTime(s Session) string {
	t := s.State.LastActivity
	if t.IsZero() {
		t = s.State.CreatedAt
	}
	return relativeTime(t)
}

// renderDetailLines produces lines for the right-hand detail pane.
func (m *Model) renderDetailLines(width, height int) []string {
	sel := m.selectedSession()
	if sel == nil {
		empty := styleEmpty.Render("  Select a session")
		return []string{empty}
	}
	s := *sel

	lines := []string{
		styleDetailTitle.Width(width).Render(s.DisplayTitle()),
		"",
	}

	// model line carries ctx% when known: "model   opus-4.8   ctx 62%" (Phase 3).
	modelVal := s.Model
	if modelVal != "" {
		if pct := s.CtxPercent(); pct > 0 {
			modelVal += fmt.Sprintf("   ctx %d%%", pct)
		}
	}
	kvPairs := []struct{ k, v string }{
		{"status", glyphStyle(s.DashStatus).Render(s.DashStatus.Glyph() + " " + s.DashStatus.String())},
		{"agent", MarkedClientLabel(s.State.Backend)},
		{"model", modelVal},
		{"project", s.State.ProjectPath},
		{"session", string(s.ID())},
		{"pod", s.State.PodName},
		{"created", relativeTime(s.State.CreatedAt)},
		{"active", rowRelTime(s)},
	}
	// Cost line once usage has been reported (Phase 3).
	if s.TotalCostUSD > 0 {
		kvPairs = append(kvPairs, struct{ k, v string }{"cost", fmt.Sprintf("$%.2f", s.TotalCostUSD)})
	}
	// When a suspend/resume/destroy is in flight, append a pending line (U3).
	if s.PendingAction != "" {
		kvPairs = append(kvPairs, struct{ k, v string }{"pending", s.PendingAction + "…"})
	}
	// Sync health (warm sessions, polled).
	if s.SyncStatus != "" {
		glyph := map[string]string{
			"synced":     "✓",
			"syncing":    "⟳",
			"stalled":    "⚠",
			"conflicted": "⇄",
		}[s.SyncStatus]
		kvPairs = append(kvPairs, struct{ k, v string }{"sync", strings.TrimSpace(glyph + " " + s.SyncStatus)})
	}

	// detailKVWidth is the fixed key column width for aligned key/value rows
	// (kit §KV, design-system §1.3). "created" and "project" are the longest keys.
	const detailKVWidth = 7
	for _, kv := range kvPairs {
		if kv.v == "" {
			continue // skip unknown fields (e.g. model before session.started)
		}
		lines = append(lines, kit.KV(kv.k, kv.v, detailKVWidth))
	}

	// Conflict detail (§1d): when the sync is conflicted, list the conflicting
	// files (Gold, matching the conflict glyph) and a one-line resolution hint
	// under the KV block so the user knows exactly what to fix and how. Both come
	// pre-formatted from the sync prober; render verbatim, width-clamped.
	if s.SyncStatus == "conflicted" && len(s.SyncConflicts) > 0 {
		for _, cf := range s.SyncConflicts {
			lines = append(lines, lipgloss.NewStyle().Foreground(theme.Gold).
				Render("  ⇄ "+truncate(cf, max(8, width-4))))
		}
		if s.SyncHint != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextMuted).
				Render("  "+truncate(s.SyncHint, max(8, width-2))))
		}
	}

	// Unread badge: events that arrived since this warm session was last viewed.
	if u := s.Unread(); u > 0 {
		badge := lipgloss.NewStyle().Foreground(theme.Gold).Bold(true).
			Render(fmt.Sprintf("● %d new", u))
		lines = append(lines, "", badge)
	}

	// Idle-soon hint: how long until the reaper suspends this idle warm session.
	if !s.IdleSince.IsZero() && m.idleTimeout > 0 {
		idleFor := time.Since(s.IdleSince)
		if rem := idleRemaining(m.idleTimeout, idleFor); rem > 0 {
			hint := fmt.Sprintf("idle %s · suspends in ~%s", roundDur(idleFor), roundDur(rem))
			lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextMuted).Render(hint))
		}
	}

	// ─ recent ─ : the last ≈3 main-thread tool calls, newest first (Phase 4).
	if n := len(s.RecentTools); n > 0 {
		lines = append(lines, detailRule("recent", width))
		shown := 0
		for i := n - 1; i >= 0 && shown < 3; i-- {
			t := s.RecentTools[i]
			tool := lipgloss.NewStyle().Foreground(theme.Malibu).Render(t.Tool)
			arg := lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(truncate(t.Arg, max(4, width-lipgloss.Width(t.Tool)-3)))
			lines = append(lines, " "+tool+"  "+arg)
			shown++
		}
	}

	// When the session is waiting for approval, show the inline permission prompt.
	if s.DashStatus == StatusWaiting && s.PendingPermissionTool != "" {
		lines = append(lines, "")

		// Gold-bordered permission box.
		toolLabel := lipgloss.NewStyle().
			Foreground(theme.Gold).
			Bold(true).
			Render(theme.GlyphWaiting + " " + s.PendingPermissionTool)
		lines = append(lines, toolLabel)
		// What the tool wants to do (command/path/url), so approving from the
		// dashboard isn't blind.
		if s.PendingPermissionArg != "" {
			arg := lipgloss.NewStyle().Foreground(theme.TextSecondary).
				Render(truncate(s.PendingPermissionArg, max(4, width-4)))
			lines = append(lines, "  "+arg)
		}

		// Unified key hint row (kit §Kbd, design-system §1.3 priority 1). On the
		// dashboard ↵ attaches (the diff viewer lives in the transcript).
		lines = append(lines, "  "+kit.KbdRow([2]string{"a", "approve"}, [2]string{"d", "deny"}, [2]string{"↵", "attach"}))
	}

	// ─ needs you ─ : action hints when the session is actionable — waiting,
	// needs-input, or failed (P13: items become actionable from the dashboard).
	switch s.DashStatus {
	case StatusWaiting, StatusNeedsInput, StatusFailed:
		lines = append(lines, detailRule("needs you", width))
		var note string
		switch s.DashStatus {
		case StatusWaiting:
			note = theme.GlyphWaiting + " waiting for your approval"
		case StatusNeedsInput:
			note = theme.GlyphNeedsInput + " ready for your next prompt"
		case StatusFailed:
			note = theme.GlyphFailed + " session failed"
		}
		lines = append(lines, " "+lipgloss.NewStyle().Foreground(glyphColor(s.DashStatus)).Render(note))
		lines = append(lines, " "+kit.KbdRow([2]string{"↵", "attach"}, [2]string{"R", "rename"}, [2]string{"x", "suspend"}, [2]string{"!", "destroy"}))
	}

	// Show last connector error if any (kit §ErrorBlock, design-system §2.3).
	if m.connectErr != nil {
		lines = append(lines, "")
		lines = append(lines, strings.Split(kit.ErrorBlock(m.connectErr.Error(), "", ""), "\n")...)
	}

	// Show last action error if any (kit §ErrorBlock, design-system §2.3).
	if m.actionErr != nil {
		lines = append(lines, "")
		lines = append(lines, strings.Split(kit.ErrorBlock(m.actionErr.Error(), "", ""), "\n")...)
	}

	// Pad/truncate lines to width.
	out := make([]string, 0, height)
	for _, l := range lines {
		if len(out) >= height {
			break
		}
		out = append(out, padRight(truncate(l, width), width))
	}
	return out
}

// detailRule renders a "─ label ─────" section divider for the detail pane.
func detailRule(label string, width int) string {
	prefix := "─ " + label + " "
	d := width - lipgloss.Width(prefix)
	if d < 0 {
		d = 0
	}
	return styleDivider.Render(prefix + strings.Repeat("─", d))
}

// renderConfirm renders the centered y/n confirmation dialog for destructive
// actions. Calm by design (a single bordered question), not a red scream.
func (m *Model) renderConfirm() string {
	if m.confirm == nil {
		return ""
	}
	msg := lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(m.confirm.message)
	hint := kit.KbdRow([2]string{"y", "yes"}, [2]string{"n", "no"})
	body := msg + "\n\n" + hint
	// D2: framed by the shared kit panel (content-fit width, coral border, raised
	// fill, 1×3 padding) — same frame as before, now via the design-system kit.
	return kit.Card(kit.CardOpts{
		Content:     body,
		BorderColor: theme.Coral,
		Background:  theme.Raised,
		PadV:        1,
		PadH:        3,
	})
}

// renderConvertModal renders the convert-to-branch prompt: the git facts from
// Status, the two editable fields (branch + commit message), an optional inline
// error, and the key hints — framed by the shared kit card (design §4.6 step 3).
func (m *Model) renderConvertModal() string {
	cm := m.convert
	if cm == nil {
		return ""
	}
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("convert to branch")

	var facts string
	if cm.dirty {
		files := "file"
		if cm.changed != 1 {
			files = "files"
		}
		facts = fmt.Sprintf("%s · %d changed %s (will be committed)", cm.curBranch, cm.changed, files)
	} else {
		facts = cm.curBranch + " · clean (rename only, no commit)"
	}
	factsLine := lipgloss.NewStyle().Foreground(theme.TextDim).Render(facts)

	branchView := lipgloss.NewStyle().Foreground(theme.TextBright).Render(cm.branch.View())
	messageView := lipgloss.NewStyle().Foreground(theme.TextBright).Render(cm.message.View())

	body := title + "\n" + factsLine + "\n\n" + branchView + "\n" + messageView
	if cm.inlineErr != "" {
		body += "\n\n" + kit.ErrorBlock(cm.inlineErr, "", "")
	}
	body += "\n\n" + kit.KbdRow(
		[2]string{"tab", "field"},
		[2]string{"↵", "convert"},
		[2]string{"esc", "cancel"},
	)

	return kit.Card(kit.CardOpts{
		Content:     body,
		BorderColor: theme.Charple,
		Background:  theme.Surface,
		PadV:        1,
		PadH:        3,
	})
}

// renderHelp renders the `?` overlay as the shared grouped, expandable help
// surface (categories sourced from the keymap; see help.go).
func (m *Model) renderHelp() string {
	return m.helpUI.view(m.width)
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func (m *Model) countStatus(s SessionStatus) int {
	n := 0
	for _, sess := range m.sessions {
		if sess.DashStatus == s {
			n++
		}
	}
	return n
}

// progressState maps the per-frame session aggregate to a Ghostty tab/taskbar
// progress state (Stage 2): a pending permission (Waiting) shows the error state
// so it surfaces on an unfocused tab; any busy turn shows an indeterminate
// pulse; otherwise the indicator is cleared. Returns ProgressNone when the
// terminal is not Ghostty so the caller can skip emission entirely.
func (m *Model) progressState() terminal.Progress {
	// Honor the global off switch (NO_COLOR / SANDBOX_REDUCE_MOTION, folded into
	// caps.ReduceMotion) so output matches today exactly under it (D2/D4).
	if !m.caps.IsGhostty || m.caps.ReduceMotion {
		return terminal.ProgressNone
	}
	c := m.partition()
	switch {
	case c.waiting > 0:
		return terminal.ProgressError
	case c.busy > 0:
		return terminal.ProgressBusy
	default:
		return terminal.ProgressNone
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// truncate shortens s to at most maxW display columns.
func truncate(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	if maxW == 1 {
		return "…"
	}
	return ansi.Truncate(s, maxW, "…")
}

// padRight pads s with spaces to exactly width display columns.
func padRight(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}
