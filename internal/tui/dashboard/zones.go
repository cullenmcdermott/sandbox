package dashboard

// zones.go — the Triage Console layout (docs/dashboard-redesign.md). The
// dashboard is composed into bands: a persistent header band, a one-line cluster
// strip, a mid region split into a SESSIONS list (left) + a DETAIL pane (right),
// and a persistent bottom status bar. Every band paints an opaque background so a
// transparent terminal can't bleed through (P1).

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// spread lays left/right against width with filler between (left-aligned left,
// right-aligned right). The right segment (status glyph / affordance) always
// survives: if left+right won't fit, the left is truncated into the remaining
// space so the row never overflows width and clips the right off the edge.
func spread(left, right string, width int) string {
	if width < 1 {
		return ""
	}
	rw := lipgloss.Width(right)
	if rw >= width {
		// The right segment alone fills (or overflows) the row: it wins, clipped
		// to fit; no room for the left or filler.
		return truncate(right, width)
	}
	// Reserve the right segment + at least one filler column, then truncate the
	// left into whatever remains.
	if avail := width - rw - 1; lipgloss.Width(left) > avail {
		left = truncate(left, avail)
	}
	gap := width - lipgloss.Width(left) - rw
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// clampLines forces s to exactly h lines, each padded to width w (no overflow).
// Padding cells are painted theme.Page so the root view is fully opaque (P1).
func clampLines(s string, w, h int) string {
	pagePad := withBackground(strings.Repeat(" ", w), theme.Page)
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, pagePad)
	}
	for i, l := range lines {
		// Enforce the "exactly w" contract in both directions: clip a line that
		// overflows w (else topBar/clusterStrip escape at narrow widths), then pad
		// any shortfall (including the sub-w remainder a wide-rune clip can leave).
		if lipgloss.Width(l) > w {
			l = ansi.Truncate(l, w, "")
		}
		if d := w - lipgloss.Width(l); d > 0 {
			l = l + withBackground(strings.Repeat(" ", d), theme.Page)
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

// bgSeq returns the profile-correct SGR sequence that turns the background on for
// color c (e.g. "\x1b[48;2;19;16;25m"), derived by letting lipgloss render a
// sentinel so the sequence matches the active color profile. Empty when c styles
// to nothing (NO_COLOR / ascii).
func bgSeq(c color.Color) string {
	rendered := lipgloss.NewStyle().Background(c).Render("\x00")
	if i := strings.IndexByte(rendered, '\x00'); i > 0 {
		return rendered[:i]
	}
	return ""
}

// withBackground forces an opaque background color onto every cell of a
// pre-styled string. lipgloss leaves interior cells transparent after a reset
// (SGR `\x1b[m` clears the background), so a plain Background().Render() over
// already-styled fragments bleeds through between fragments (P1). This re-asserts
// the background after every reset and at the start, so each cell paints. Inner
// fragments that set their own background (e.g. a selected row) still win because
// their SGR overrides the ambient one for those cells.
func withBackground(s string, c color.Color) string {
	seq := bgSeq(c)
	if seq == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+seq)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+seq)
	// Re-assert at the start of every physical line too: when this box is later
	// stitched beside another pane (JoinHorizontal), the neighbor's trailing
	// reset would otherwise leave this pane's line starts transparent.
	s = strings.ReplaceAll(s, "\n", "\n"+seq)
	return seq + s + "\x1b[0m"
}

// pageWhitespace fills lipgloss.Place padding with opaque page-colored cells so
// overlay/splash margins don't show the terminal's (possibly transparent)
// background (T9). lipgloss v2 removed WithWhitespaceBackground, so the bg is set
// via a Style.
func pageWhitespace() lipgloss.WhitespaceOption {
	return lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(theme.Page))
}

// boxWithTitle renders a rounded box of exactly w×h with the title embedded in
// the top border. Body lines are clipped/padded to the inner width so columns
// line up; the box is padded to h-2 body rows. Every cell is painted bg so the
// surface is opaque (P1).
func boxWithTitle(title string, body []string, w, h int, bc, bg color.Color) string {
	if w < 6 {
		w = 6
	}
	if h < 3 {
		h = 3
	}
	bs := lipgloss.NewStyle().Foreground(bc)
	ts := lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true)

	t := " " + title + " "
	if lipgloss.Width(t) > w-3 {
		t = truncate(t, w-3)
	}
	d := w - 3 - lipgloss.Width(t)
	if d < 0 {
		d = 0
	}
	top := bs.Render("╭─") + ts.Render(t) + bs.Render(strings.Repeat("─", d)+"╮")

	cw := w - 4
	rows := make([]string, h-2)
	for i := range rows {
		content := ""
		if i < len(body) {
			content = body[i]
		}
		content = padRight(truncate(content, cw), cw)
		rows[i] = bs.Render("│") + " " + content + " " + bs.Render("│")
	}
	bottom := bs.Render("╰" + strings.Repeat("─", w-2) + "╯")

	box := strings.Join(append(append([]string{top}, rows...), bottom), "\n")
	return withBackground(box, bg)
}

// renderZoned composes the full Triage Console (header band + cluster strip + mid
// list/detail + status bar), clamped to exactly w×h. If a toast notification is
// active, it is overlaid at the top-right via the lipgloss compositor.
func (m *Model) renderZoned(w, h int) string {
	// Compute the session tally once per render and pass it to both bands (the
	// sessionPartition contract: "computed once per render"). topBar and
	// clusterStrip previously each recomputed it, tallying m.sessions twice.
	c := m.partition()
	top := m.topBar(w, c)
	strip := m.clusterStrip(w, c)
	bottom := m.bottomBar(w)

	midH := h - 3 // minus the top + strip + bottom bands
	if midH < 6 {
		midH = 6
	}

	gap := 1
	leftW := w * 58 / 100
	if leftW < 28 {
		leftW = 28
	}
	rightW := w - leftW - gap

	var mid string
	if rightW < 30 {
		// Narrow terminal: drop the detail pane, full-width sessions box.
		mid = boxWithTitle("SESSIONS", m.sessionListBody(w, midH), w, midH, theme.BorderMedium, theme.Surface)
	} else {
		left := boxWithTitle("SESSIONS", m.sessionListBody(leftW, midH), leftW, midH, theme.BorderMedium, theme.Surface)
		right := boxWithTitle(m.detailTitle(rightW), m.renderDetailLines(rightW-4, midH-2), rightW, midH, theme.BorderMedium, theme.Raised)
		gapCol := joinGap(midH, gap)
		mid = lipgloss.JoinHorizontal(lipgloss.Top, left, gapCol, right)
	}

	bg := lipgloss.JoinVertical(lipgloss.Left, top, strip, mid, bottom)
	// The toast is intentionally NOT composited here: App.View overlays it over
	// the final frame of *every* screen (App.withToast, T3), so it floats above
	// the chat modal / connecting splash too — and so it never gets baked into the
	// modal's dimmed backdrop. Keeping it out of the dashboard's own view is what
	// makes that single-overlay path correct.
	return clampLines(bg, w, h)
}

// joinGap is the opaque (page-colored) inter-column gutter between the list and
// detail panes.
func joinGap(h, w int) string {
	line := withBackground(strings.Repeat(" ", w), theme.Page)
	lines := make([]string, h)
	for i := range lines {
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// detailTitle is the right-pane title: "DETAIL ─ <session>" for the selected
// session, or plain "DETAIL" when nothing is selected. The session portion is
// truncated so it always fits the border.
func (m *Model) detailTitle(boxW int) string {
	sel := m.selectedSession()
	if sel == nil {
		return "DETAIL"
	}
	name := truncate(sel.DisplayTitle(), max(4, boxW-12))
	return "DETAIL ─ " + name
}

// topBar is the persistent header band: branded wordmark + session tally, with a
// "needs you" badge on the right when sessions are waiting.
func (m *Model) topBar(w int, c sessionPartition) string {
	wordmark := theme.GradientText("sandbox", true, theme.Charple, theme.Dolly)
	// Use attentionSummary for the waiting/needs-input portion (D4): renders
	// "2 waiting · 1 needs input" or "" when nothing needs attention.
	attnStr := attentionSummary(m.sessions)
	tallyStr := fmt.Sprintf("%d session%s · %d busy", c.total, plural(c.total), c.busy)
	if attnStr != "" {
		tallyStr += " · " + attnStr
	}
	tally := styleHeaderTally.Render(tallyStr)
	left := " " + wordmark + "   " + tally

	var right string
	switch {
	case m.filtering:
		right = styleHeaderFilter.Render("/ " + m.filterBuf + "█")
	case m.filter != "":
		right = styleHeaderFilter.Render("filter: " + m.filter)
	default:
		sortStr := "sort: " + m.sortKey.String() + " " + m.sortDir.Arrow()
		if m.attentionFirst {
			sortStr = "⚑ attn + " + sortStr
		}
		right = styleHeaderSort.Render(sortStr)
	}
	// Persistent "needs you" badge (D4 §3.1) — always visible when sessions need attention.
	if attnStr != "" {
		badge := lipgloss.NewStyle().Foreground(theme.Gold).Bold(true).
			Render("◆ " + attnStr + "  ")
		right = badge + right
	}
	right += " "
	return withBackground(spread(left, right, w), theme.Surface)
}

// clusterStrip is the one-line cluster summary that replaces the old CLUSTER box:
// pod-state counts (running / suspended / failed) on the left and the real
// backend mix (claude N · opencode M) on the right, both derived from the live
// sessions (P9, P13).
func (m *Model) clusterStrip(w int, c sessionPartition) string {
	key := lipgloss.NewStyle().Foreground(theme.TextMuted)
	val := lipgloss.NewStyle().Foreground(theme.TextBody)

	dot := lipgloss.NewStyle().Foreground(theme.Guac).Render("●")
	left := key.Render(" cluster  ") + dot + " " +
		val.Render(fmt.Sprintf("%d running", c.running)) +
		val.Render(fmt.Sprintf(" · %d suspended", c.suspended))
	if c.failed > 0 {
		left += val.Render(" · ") + lipgloss.NewStyle().Foreground(theme.Coral).Render(fmt.Sprintf("✕ %d failed", c.failed))
	}

	right := m.backendMix(c) + " " // self-styled (carries its own brand marks)
	return withBackground(spread(left, right, w), theme.Surface)
}

// backendMix renders the live backend distribution, e.g. "claude 4 · opencode 1"
// (P9 — no hardcoded backend). Known backends render in a stable order; any
// unknown ids follow, sorted, so the strip is deterministic.
func (m *Model) backendMix(c sessionPartition) string {
	val := lipgloss.NewStyle().Foreground(theme.TextBody)
	// part renders "<mark> <client> <n>" with the brand mark in its own color and
	// the rest in TextBody, each part fully styled so colors don't bleed when the
	// parts are joined (the mark's reset would otherwise clear the row tone).
	part := func(b string, n int) string {
		s := val.Render(fmt.Sprintf("%s %d", ClientLabel(b), n))
		if mark := BackendMark(b); mark != "" {
			return mark + " " + s
		}
		return s
	}
	var parts []string
	for _, b := range []string{session.BackendClaudeSDK, session.BackendOpenCode} {
		if n := c.byBackend[b]; n > 0 {
			parts = append(parts, part(b, n))
		}
	}
	var extras []string
	for b := range c.byBackend {
		if b == session.BackendClaudeSDK || b == session.BackendOpenCode {
			continue
		}
		extras = append(extras, b)
	}
	sort.Strings(extras)
	for _, b := range extras {
		parts = append(parts, part(b, c.byBackend[b]))
	}
	return strings.Join(parts, val.Render(" · "))
}

// bottomBar is the persistent status/hint band.
func (m *Model) bottomBar(w int) string {
	m.help.SetWidth(w - 2)
	left := " " + m.help.ShortHelpView(m.keys.ShortHelp())
	// Right-aligned warm-session count: how many running sessions are kept warm
	// (live model + passive stream) and so resume instantly.
	warm := ""
	if n := m.warmCount(); n > 0 {
		warm = lipgloss.NewStyle().Foreground(theme.Gold).Render(fmt.Sprintf("⚡%d warm", n)) + " "
	}
	// theme.TextMuted (not the recessed theme.TextDim) so the footer keeps contrast
	// against the surface fill (P11).
	styled := lipgloss.NewStyle().Foreground(theme.TextMuted).Render(spread(left, warm, w))
	return withBackground(styled, theme.Surface)
}

// sessionPartition is the single shared session tally (P13), consumed by the
// header, the cluster strip, and the legend. It carries both the DashStatus
// dimension (busy/waiting/…) and the cluster-state dimension (running/…), plus
// the backend mix, so counts are computed once per render.
type sessionPartition struct {
	total int
	// DashStatus dimension.
	busy, waiting, needsInput, idle, suspendedView, failedView int
	// Cluster-state dimension (session.State.Status).
	running, suspended, failed int
	// Backend id → count.
	byBackend map[string]int
}

// partition computes the shared session tally in one pass (P13).
func (m *Model) partition() sessionPartition {
	c := sessionPartition{byBackend: make(map[string]int)}
	c.total = len(m.sessions)
	for _, s := range m.sessions {
		switch s.DashStatus {
		case StatusBusy:
			c.busy++
		case StatusWaiting:
			c.waiting++
		case StatusNeedsInput:
			c.needsInput++
		case StatusIdle:
			c.idle++
		case StatusSuspended:
			c.suspendedView++
		case StatusFailed:
			c.failedView++
		}
		switch s.State.Status {
		case session.StatusRunning, session.StatusCreating:
			c.running++
		case session.StatusSuspended:
			c.suspended++
		case session.StatusFailed:
			c.failed++
		}
		c.byBackend[s.State.Backend]++
	}
	return c
}

// sessionListBody renders the SESSIONS box body rows for a box of inner width w.
// Rows are two physical lines each; a pinned legend row is appended as the box
// footer (P5).
func (m *Model) sessionListBody(boxW, boxH int) []string {
	innerW := boxW - 4
	if innerW < 10 {
		innerW = 10
	}
	allRows := m.visibleRows()
	bodyH := boxH - 2
	if bodyH < 1 {
		bodyH = 1
	}

	// Reserve the last body line for the pinned legend (P5) when there's room.
	legend := ""
	listH := bodyH
	if bodyH >= 3 {
		legend = m.renderLegend(innerW)
		listH = bodyH - 1
	}

	rows, shown := m.renderRowLines(allRows, innerW, listH)

	// Overflow band (D4 §3.3): when the list exceeds the viewport, show how many
	// sessions are below the fold and whether any need attention.
	if shown < len(allRows) {
		hidden := make([]Session, 0)
		for _, r := range allRows[shown:] {
			if r.session != nil {
				hidden = append(hidden, *r.session)
			}
		}
		if band := overflowSummary(hidden); band != "" {
			bandLine := lipgloss.NewStyle().Foreground(theme.TextMuted).Render("  " + band)
			bandLine = padRight(truncate(bandLine, innerW), innerW)
			if len(rows) > 0 {
				rows[len(rows)-1] = bandLine
			} else {
				rows = append(rows, bandLine)
			}
		}
	}

	// Pad the list region to its reserved height, then pin the legend.
	for len(rows) < listH {
		rows = append(rows, padRight("", innerW))
	}
	if legend != "" {
		rows = append(rows, legend)
	}
	return rows
}

// renderLegend renders the pinned glyph legend footer (P5).
func (m *Model) renderLegend(innerW int) string {
	legend := "─ legend ─  ○idle ◐busy ◆wait ❯input ⏾susp ✕fail"
	return lipgloss.NewStyle().Foreground(theme.TextMuted).Render(truncate(legend, innerW))
}

// filepathBaseLocal returns the last path segment (project base) without
// importing path/filepath here.
func filepathBaseLocal(p string) string {
	if i := strings.LastIndexByte(strings.TrimRight(p, "/"), '/'); i >= 0 {
		return strings.TrimRight(p, "/")[i+1:]
	}
	return p
}
