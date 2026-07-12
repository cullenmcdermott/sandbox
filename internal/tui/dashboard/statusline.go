package dashboard

// statusline.go — the chat status line (slice 1 / Mockup A). A faithful
// Claude-Code statusline clone rendered below the prompt: a 4-row block of
// model ─ cwd ─ branch ─ ctx%/limit + dot-bar · $cost, two rate-limit rows,
// and the permission-mode line. No background band — it blends into the chat
// like the real CC statusline (just colored text). Adapted from the original
// UX-lab statusline prototype (statusCC + dotBar + modeTag), themed via the
// Phase-0a tokens.

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// Status-line collapse (§2c). The permanent two-row gauge block became ONE quiet
// row by default: model · cwd · branch · (ctx only ≥ctxGaugeThreshold) · (cost
// only ≥statusCostThreshold) · mode. The rate-limit window row is TRANSIENT — it
// appears for rlTransientWindow after a rate_limit.updated event and fades back,
// so the status line is a single line at rest. Height is therefore variable
// (statusLineHeight): 1 at rest, 2 while the transient row shows. The layout is
// declarative (liveLayout reserves statusLineHeight()), so the one-line grow/
// shrink is a clean body reflow, not hand-counted stack arithmetic.
const (
	// ctxGaugeThreshold is the context-window fill fraction (as a percent) below
	// which the ctx gauge is hidden entirely — a fresh, roomy context is quiet.
	ctxGaugeThreshold = 60
	// statusCostThreshold hides trivial spend: the $cost segment appears only once
	// a turn has cost at least this much, so a cheap session stays uncluttered.
	statusCostThreshold = 0.10
	// rlTransientWindow is how long the rate-limit row stays up after an update
	// before it has fully faded out. The last fadeTail of it is the fade.
	rlTransientWindow = 8 * time.Second
	rlTransientFade   = 3 * time.Second
)

// podWorkspacePrefix is the legacy pod path the project was mounted under. The
// SDK cwd is now the real host project path (Option B, resumable transcripts),
// so this strip is a no-op for new sessions and remains only to keep the display
// tidy for pre-migration sessions still reporting a /session/workspace cwd.
const podWorkspacePrefix = "/session/workspace"

// permMode is the SDK permission mode the attached chat runs its turns in.
type permMode int

const (
	modeDefault     permMode = iota // SDK "default": ask before each tool
	modeAcceptEdits                 // SDK "acceptEdits": auto-accept edits
	modePlan                        // SDK "plan": read-only planning
	modeBypass                      // SDK "bypassPermissions": yolo (session default)
)

// apiValue is the TurnInput.Mode string sent to the runner for this mode.
func (p permMode) apiValue() string {
	switch p {
	case modeDefault:
		return "default"
	case modePlan:
		return "plan"
	case modeBypass:
		return "bypassPermissions"
	default:
		return "acceptEdits"
	}
}

// next cycles default → acceptEdits → plan → bypassPermissions → wrap, the
// shift+tab order from the design doc.
func (p permMode) next() permMode { return permMode((int(p) + 1) % 4) }

// modeTag is the compact permission-mode tag for the collapsed status row (A2.5):
// a glyph + short label in the mode's hue. The safer modes sit as a quiet
// foreground-only trailing tag on row 1. bypassPermissions (yolo) is the §2d
// default, so it must never be invisible: it renders as a filled coral warning
// CHIP (inverted — dark text on a coral band, bold) that stands out from the
// quiet tags, making an active yolo session unmistakable at a glance.
func (p permMode) modeTag() string {
	if p == modeBypass {
		return lipgloss.NewStyle().
			Foreground(theme.Page).
			Background(theme.Coral).
			Bold(true).
			Render(" ⚠ bypass ")
	}
	var glyph, label string
	var col color.Color
	switch p {
	case modeAcceptEdits:
		glyph, label, col = "⏵⏵", "auto", theme.Guac
	case modeDefault:
		glyph, label, col = "⏵", "ask", theme.Malibu
	case modePlan:
		glyph, label, col = "⏸", "plan", theme.Gold
	}
	return lipgloss.NewStyle().Foreground(col).Render(glyph + " " + label)
}

// effortTag is the compact reasoning-effort tag for the collapsed status row,
// rendered only when the user has set a per-turn /effort override. It mirrors
// modeTag's shape (a glyph + short label in a hue). The SDK wire value "max"
// renders as "ultracode" (its UI label); the other levels show verbatim. Hue
// climbs by intensity (Malibu→Guac→Gold→Peach→Coral for low→max) so a higher
// tier reads hotter at a glance.
func effortTag(level string) string {
	label := level
	var col color.Color
	switch level {
	case "low":
		col = theme.Malibu
	case "medium":
		col = theme.Guac
	case "high":
		col = theme.Gold
	case "xhigh":
		col = theme.Peach
	case "max":
		label, col = "ultracode", theme.Coral
	default:
		col = theme.TextMuted
	}
	return lipgloss.NewStyle().Foreground(col).Render("⚡ " + label)
}

// label is the short human name of the mode (for /command confirmations).
func (p permMode) label() string {
	switch p {
	case modeDefault:
		return "ask"
	case modePlan:
		return "plan"
	case modeBypass:
		return "bypass"
	default:
		return "accept-edits"
	}
}

// rampColor returns a green→gold→coral color for a 0..1 fraction (context-%).
// Adapted from the original UX-lab prototype.
func rampColor(frac float64) color.Color {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	ramp := lipgloss.Blend1D(24, theme.Guac, theme.Gold, theme.Coral)
	return ramp[int(frac*float64(len(ramp)-1))]
}

// dotBar renders an n-segment ●/○ progress bar for a 0..1 fraction (the
// Claude-Code statusline idiom). Filled dots take fillColor; empty dots use the
// recessed `dim` rung.
// blockBar renders an n-segment █/░ progress bar for a 0..1 fraction.
// Filled blocks take fillColor; empty blocks use the recessed `dim` rung.
func blockBar(frac float64, n int, fillColor color.Color) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	fill := int(frac * float64(n))
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(fillColor).Render(strings.Repeat("█", fill)))
	b.WriteString(lipgloss.NewStyle().Foreground(theme.TextDim).Render(strings.Repeat("░", n-fill)))
	return b.String()
}

// shimmerBlockBar renders an n-segment block bar whose FILLED cells carry a
// gradient that scrolls by `phase` cells each frame, producing a live shimmer
// while a turn runs (Stage 1, docs/archive/ghostty-terminal-effects.md). Empty cells use
// the same recessed dim rung as blockBar, and the total cell count is identical,
// so the bar occupies exactly the same width — no layout shift versus the static
// path. When pulse is true (ctx ≥ 80%) the fill breathes through a coral ramp
// instead of the green→gold→coral ramp, so a near-full context window throbs
// coral rather than only earning the `!` prefix.
func shimmerBlockBar(frac float64, n, phase int, pulse bool) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	if n < 1 {
		n = 1
	}
	fill := int(frac * float64(n))
	ramp := lipgloss.Blend1D(n, theme.Guac, theme.Gold, theme.Coral)
	if pulse {
		ramp = lipgloss.Blend1D(n, theme.Coral, theme.Peach, theme.Coral)
	}
	var b strings.Builder
	for i := 0; i < fill; i++ {
		idx := ((i+phase)%len(ramp) + len(ramp)) % len(ramp)
		b.WriteString(lipgloss.NewStyle().Foreground(ramp[idx]).Render("█"))
	}
	b.WriteString(lipgloss.NewStyle().Foreground(theme.TextDim).Render(strings.Repeat("░", n-fill)))
	return b.String()
}

// kittyGaugeCols/Rows are the placeholder rectangle dimensions; cols matches the
// block bar width exactly so the kitty gauge causes no layout shift. The source
// bitmap is rendered larger and scaled to the cell grid by the terminal.
const (
	kittyGaugeCols = 10
	kittyGaugeRows = 1
	kittyGaugePixW = 80
	kittyGaugePixH = 16
)

// ctxGaugeKitty returns the Stage 3 Kitty Unicode-placeholder ctx gauge: a run
// of kittyGaugeCols placeholder cells the terminal swaps for a rasterized fill
// gauge. The gauge image is (re)transmitted only when the fill bucket (whole
// percent) changes — the transmission is queued one-shot into pendingKitty for
// App.View to prepend to the frame, never emitted every frame. A new image id
// per change forces the terminal to re-fetch. The returned placeholder run is
// exactly kittyGaugeCols cells wide, matching the block bar it replaces.
func (m *TranscriptModel) ctxGaugeKitty(frac float64) string {
	bucket := int(frac * 100)
	if bucket != m.kittyGaugeBucket || m.kittyGaugeID == 0 {
		m.kittyGaugeBucket = bucket
		// Ids stay within 24 bits because KittyPlaceholders encodes the id in the
		// cell's 24-bit foreground color while the transmission carries the full
		// id — they must agree. Wrap back to 1 (0 is reserved) past the 24-bit
		// ceiling so a marathon session can't desync them.
		m.kittyGaugeID++
		if m.kittyGaugeID >= (1 << 24) {
			m.kittyGaugeID = 1
		}
		rgba := terminal.GaugeRGBA(frac, kittyGaugePixW, kittyGaugePixH,
			rgbOf(rampColor(frac)), rgbOf(theme.TextDim))
		m.pendingKitty = terminal.KittyTransmitRGBA(m.kittyGaugeID,
			kittyGaugeCols, kittyGaugeRows, kittyGaugePixW, kittyGaugePixH, rgba)
	}
	return terminal.KittyPlaceholders(m.kittyGaugeID, kittyGaugeCols, kittyGaugeRows)
}

// takePendingKitty returns and clears any queued one-shot Kitty image
// transmission so it rides exactly one frame.
func (m *TranscriptModel) takePendingKitty() string {
	s := m.pendingKitty
	m.pendingKitty = ""
	return s
}

// rgbOf converts a lipgloss/theme color.Color to the terminal package's 8-bit
// RGB (dropping any alpha) for gauge bitmap generation.
func rgbOf(c color.Color) terminal.RGB {
	r, g, b, _ := c.RGBA()
	return terminal.RGB{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8)}
}

// shortModelName turns a model id (e.g. "claude-opus-4-8" or
// "claude-sonnet-4-6-20250929") into a short display name ("Opus 4.8",
// "Sonnet 4.6"). Unknown shapes fall back to the raw id.
func shortModelName(id string) string {
	if id == "" {
		return "—"
	}
	parts := strings.Split(strings.TrimPrefix(id, "claude-"), "-")
	if len(parts) == 0 || parts[0] == "" {
		return id
	}
	var ver []string
	for _, p := range parts[1:] {
		if isShortNum(p) {
			ver = append(ver, p)
		} else {
			break
		}
	}
	name := capitalize(parts[0])
	if len(ver) > 0 {
		name += " " + strings.Join(ver, ".")
	}
	return name
}

func isShortNum(s string) bool {
	if len(s) == 0 || len(s) > 2 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// parseResetTime parses an RFC3339 reset instant from a rate_limit.updated
// payload, returning the zero time for an empty or unparseable value (rendered
// as "—").
func parseResetTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// fmtReset renders the countdown to a usage-window reset (e.g. "2h13m", "3d4h").
// Uses the injectable clock nowFunc() so golden snapshots stay stable. The zero
// time (unknown reset) renders as "—"; a past instant renders as "now".
func fmtReset(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := t.Sub(nowFunc())
	if d <= 0 {
		return "now"
	}
	if d < time.Minute {
		return "<1m"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// statusCwd is the project path shown in the status line: the SDK cwd (now the
// real host project path) with any legacy /session/workspace/ prefix stripped,
// falling back to the project base name.
func (m *TranscriptModel) statusCwd() string {
	if m.cwd != "" {
		if rel := strings.TrimPrefix(strings.TrimPrefix(m.cwd, podWorkspacePrefix), "/"); rel != "" && rel != m.cwd {
			return rel
		}
		return filepath.Base(m.cwd)
	}
	if m.projectPath != "" {
		return filepath.Base(m.projectPath)
	}
	return ""
}

// ctxTokens is the current request's context size: input + cache-read +
// cache-write tokens from the latest usage event. This is the ctx% numerator.
func (m *TranscriptModel) ctxTokens() int {
	return m.InputTokens + m.CacheReadTokens + m.CacheWriteTokens
}

// activeModelWindow returns the per-model weekly usage window (Opus or Sonnet)
// matching the attached model, when the plan exposes one. Max plans carry a
// separate weekly cap per model; the status strip surfaces just the one for the
// model in use rather than every window (the full set rides on the event for a
// future /usage view). Haiku, unknown, or unset models have no per-model cap, so
// ok is false. label is "opus"/"sonnet" for the row.
func (m *TranscriptModel) activeModelWindow() (util float64, reset time.Time, label string, ok bool) {
	id := strings.ToLower(m.Model)
	switch {
	case strings.Contains(id, "opus") && m.rlOpusSeen:
		return m.rlOpusUtil, m.rlOpusReset, "opus", true
	case strings.Contains(id, "sonnet") && m.rlSonnetSeen:
		return m.rlSonnetUtil, m.rlSonnetReset, "sonnet", true
	}
	return 0, time.Time{}, "", false
}

// statusLineHeight is the current row count of the status line: one quiet row at
// rest, plus the transient rate-limit row while it is up (§2c). liveLayout
// reserves exactly this, so renderStatusLine must emit the same number of rows.
func (m *TranscriptModel) statusLineHeight() int {
	if m.rateRowVisible() {
		return 2
	}
	return 1
}

// rateRowVisible reports whether the transient rate-limit row is up: within
// rlTransientWindow of the last rate_limit.updated. A zero updatedAt (no event
// yet) is never visible, so a fresh session shows a single-row status line.
func (m *TranscriptModel) rateRowVisible() bool {
	if !m.rlSeen || m.rlUpdatedAt.IsZero() {
		return false
	}
	return nowFunc().Sub(m.rlUpdatedAt) < rlTransientWindow
}

// rateRowFade is the transient row's 0..1 fade fraction: 0 during the hold, then
// ramping to 1 across the final rlTransientFade so the row blends toward the page
// background before it is dropped. 0 outside the window (never called then).
func (m *TranscriptModel) rateRowFade() float64 {
	el := nowFunc().Sub(m.rlUpdatedAt)
	start := rlTransientWindow - rlTransientFade
	if el <= start {
		return 0
	}
	f := float64(el-start) / float64(rlTransientFade)
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f
}

// slFade blends a status-line color toward the page background by frac (0=full,
// 1=gone), used to fade the transient rate-limit row out over its window.
func slFade(c color.Color, frac float64) color.Color {
	if frac <= 0 {
		return c
	}
	return anim.LerpColor(c, theme.Page, frac)
}

// slSeg is one budgeted status-row segment. Required segments (the model id and
// the permission-mode chip — the ⚠ bypass warning must never be invisible, §2d)
// are always kept; optional segments are dropped from the tail inward when the
// row would otherwise overflow, so the row is width-safe by construction (§1c).
// Each optional segment bakes in its own leading separator, so dropping a middle
// one never leaves a dangling separator.
type slSeg struct {
	s        string
	required bool
}

// budgetRow joins segments left-to-right, keeping every required segment and
// including optional ones only while they (plus the still-unplaced required
// segments) fit the budget. A final ANSI-aware truncate is the backstop when even
// the required segments overflow a very narrow width.
func budgetRow(budget int, segs []slSeg) string {
	reqAfter := make([]int, len(segs)+1)
	for i := len(segs) - 1; i >= 0; i-- {
		reqAfter[i] = reqAfter[i+1]
		if segs[i].required {
			reqAfter[i] += lipgloss.Width(segs[i].s)
		}
	}
	var b strings.Builder
	cur := 0
	for i, sg := range segs {
		w := lipgloss.Width(sg.s)
		if !sg.required && cur+w+reqAfter[i+1] > budget {
			continue
		}
		b.WriteString(sg.s)
		cur += w
	}
	out := b.String()
	if lipgloss.Width(out) > budget {
		out = truncate(out, budget)
	}
	return out
}

// ctxBar renders the 10-cell context gauge: the Ghostty raster gauge (Kitty
// placeholders), the shimmer bar during a turn, or the static block bar — the
// same degradation ladder as before, just gated behind the ≥60% threshold now.
func (m *TranscriptModel) ctxBar(frac float64, pct int) string {
	switch {
	case m.caps.KittyGraphics && !m.caps.ReduceMotion:
		return m.ctxGaugeKitty(frac)
	case m.caps.TrueColor && !m.caps.ReduceMotion && m.DashStatus == StatusBusy:
		return shimmerBlockBar(frac, 10, m.workFrame, pct >= 80)
	default:
		return blockBar(frac, 10, rampColor(frac))
	}
}

// ctxSegment builds the row-1 context gauge, or "" to hide it. It is HIDDEN when
// the model's context limit is unknown (m.CtxLimit ≤ 0) — matching the dashboard,
// which hides the gauge then rather than assuming a 200k window (§2c fix c) — and
// also below ctxGaugeThreshold, so a roomy context stays quiet. At/above the
// threshold it shows "pct% <bar>", with a coral "! " warning past 80%.
func (m *TranscriptModel) ctxSegment() string {
	if m.CtxLimit <= 0 {
		return ""
	}
	frac := float64(m.ctxTokens()) / float64(m.CtxLimit)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	pct := int(frac*100 + 0.5)
	if pct < ctxGaugeThreshold {
		return ""
	}
	warn := ""
	if pct >= 80 {
		warn = styleSLWarn.Render("! ")
	}
	return warn +
		lipgloss.NewStyle().Foreground(rampColor(frac)).Render(fmt.Sprintf("%d%% ", pct)) +
		m.ctxBar(frac, pct)
}

// renderStatusLine renders the §2c collapsed status line: one quiet row (model ·
// cwd · branch · ctx≥60% · cost≥threshold · mode · effort · sync), plus the
// transient rate-limit row while it is up. Width-safe by construction.
func (m *TranscriptModel) renderStatusLine() string {
	// Budget the row to the frame width (leaving the leading space), so segments
	// shed tail-first instead of overflowing (§1c). A model that hasn't been laid
	// out yet (width 0 — background/warm builds, unit tests) has no meaningful
	// budget, so nothing is shed: the row renders in full.
	budget := 1 << 30
	if m.width > 0 {
		budget = max(10, m.width-1)
	}
	row1 := m.statusRow1(budget)
	if !m.rateRowVisible() {
		return " " + row1
	}
	return " " + row1 + "\n " + truncate(m.rateRow(), budget)
}

// statusRow1 assembles the always-present quiet row as budgeted segments. The
// model id and the mode chip are required (the ⚠ bypass chip must stay visible);
// cwd, branch, ctx, cost, effort, and sync are optional and shed tail-first at
// narrow widths. Every optional segment bakes in its leading separator.
func (m *TranscriptModel) statusRow1(budget int) string {
	muted := styleSLMuted
	sepDash := muted.Render(" ─ ")
	sepDot := muted.Render(" · ")

	segs := []slSeg{{s: styleSLBright.Render(shortModelName(m.Model)), required: true}}
	if cwd := m.statusCwd(); cwd != "" {
		segs = append(segs, slSeg{s: sepDash + styleSLBody.Render(cwd)})
	}
	if m.Branch != "" {
		b := m.Branch
		if m.Dirty {
			b += "*"
		}
		branch := styleSLBranch.Render(b)
		if m.Ahead > 0 || m.Behind > 0 {
			branch += muted.Render(fmt.Sprintf(" ↑%d↓%d", m.Ahead, m.Behind))
		}
		segs = append(segs, slSeg{s: sepDash + branch})
	}
	if ctx := m.ctxSegment(); ctx != "" {
		segs = append(segs, slSeg{s: sepDash + ctx})
	}
	// Cost trails only once spend is material (§2c): a cheap session stays quiet.
	if m.TotalCostUSD >= statusCostThreshold {
		segs = append(segs, slSeg{s: sepDot + styleSLCost.Render(fmt.Sprintf("$%.2f", m.TotalCostUSD))})
	}
	// The permission-mode chip is required so the ⚠ bypass warning is never shed.
	segs = append(segs, slSeg{s: sepDot + m.mode.modeTag(), required: true})
	if m.effortOverride != "" {
		segs = append(segs, slSeg{s: sepDot + effortTag(m.effortOverride)})
	}
	if seg := syncSegment(m.syncStatus); seg != "" {
		segs = append(segs, slSeg{s: sepDot + seg})
	}
	return budgetRow(budget, segs)
}

// rateRow builds the transient claude.ai plan-usage row (5-hour + weekly util
// bars, reset countdowns, and the attached model's weekly cap when present) from
// real rate_limit.updated data. Every color is faded toward the page background
// by rateRowFade so the row visibly dissolves before it is dropped. When plan
// limits don't apply it names the reason (headless auth) instead of a bare blank.
func (m *TranscriptModel) rateRow() string {
	fade := m.rateRowFade()
	lbl := func(s string) string {
		return lipgloss.NewStyle().Foreground(slFade(theme.TextSecondary, fade)).Render(s)
	}
	body := func(s string) string {
		return lipgloss.NewStyle().Foreground(slFade(theme.TextBody, fade)).Render(s)
	}
	mut := func(s string) string {
		return lipgloss.NewStyle().Foreground(slFade(theme.TextMuted, fade)).Render(s)
	}
	if m.rlAvailable {
		f5 := m.rl5hUtil / 100
		f7 := m.rl7dUtil / 100
		row := lbl("5h: ") + blockBar(f5, 8, slFade(rampColor(f5), fade)) +
			body(fmt.Sprintf(" %d%%", int(m.rl5hUtil+0.5))) +
			mut(" "+fmtReset(m.rl5hReset)) +
			mut(" · ") + lbl("weekly: ") + blockBar(f7, 8, slFade(rampColor(f7), fade)) +
			body(fmt.Sprintf(" %d%%", int(m.rl7dUtil+0.5))) +
			mut(" "+fmtReset(m.rl7dReset))
		if mUtil, mReset, lname, ok := m.activeModelWindow(); ok {
			row += mut(" · ") + lbl(lname+": ") +
				lipgloss.NewStyle().Foreground(slFade(rampColor(mUtil/100), fade)).Render(fmt.Sprintf("%d%%", int(mUtil+0.5))) +
				mut(" · "+lname+" in "+fmtReset(mReset))
		}
		return row
	}
	// Plan limits don't apply. An empty subscription type means headless setup-
	// token / API-key auth, where the experimental /usage API carries no limits.
	reason := "n/a"
	if m.rlSubscription == "" {
		reason = "n/a (headless auth)"
	}
	return lbl("usage: ") + mut(reason)
}

// syncSegment renders the attached session's mutagen sync health as a compact
// status-line segment (glyph + label), matching the dashboard detail pane's
// glyphs (✓ synced / ⟳ syncing / ⚠ stalled). A stalled sync is coral to draw the
// eye; the healthy states are muted so they don't compete with the live gauge.
// An empty/unknown status returns "" so the status line stays unchanged.
func syncSegment(status string) string {
	glyph, ok := map[string]string{
		"synced":     "✓",
		"syncing":    "⟳",
		"stalled":    "⚠",
		"conflicted": "⇄",
	}[status]
	if !ok {
		return ""
	}
	// Transport stall = Coral (an error that may self-heal); conflict = Gold (it
	// needs YOU to resolve, mirroring the waiting-on-user status color).
	color := theme.TextMuted
	switch status {
	case "stalled":
		color = theme.Coral
	case "conflicted":
		color = theme.Gold
	}
	return lipgloss.NewStyle().Foreground(color).Render(glyph + " " + status)
}
