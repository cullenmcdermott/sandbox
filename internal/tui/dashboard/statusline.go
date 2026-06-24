package dashboard

// statusline.go — the chat status line (slice 1 / Mockup A). A faithful
// Claude-Code statusline clone rendered below the prompt: a 4-row block of
// model ─ cwd ─ branch ─ ctx%/limit + dot-bar · $cost, two rate-limit rows,
// and the permission-mode line. No background band — it blends into the chat
// like the real CC statusline (just colored text). Adapted from the original
// UX-lab statusline prototype (statusCC + dotBar + modeLine), themed via the
// Phase-0a tokens.

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// statusLineRows is the fixed height of the status-line block. A2.5 (Calm)
// collapsed the old 4-row CC-clone block to two: a single status row (model ·
// cwd · branch · ctx · cost · mode) plus one conditional rate-limit row, shown
// only when real claude.ai /usage data is present and otherwise blank. Kept
// FIXED at 2 (not variable) so the body height never reflows when usage data
// arrives mid-session. layout() reserves this many rows below the prompt.
const statusLineRows = 2

// podWorkspacePrefix is the legacy pod path the project was mounted under. The
// SDK cwd is now the real host project path (Option B, resumable transcripts),
// so this strip is a no-op for new sessions and remains only to keep the display
// tidy for pre-migration sessions still reporting a /session/workspace cwd.
const podWorkspacePrefix = "/session/workspace"

// permMode is the SDK permission mode the attached chat runs its turns in.
type permMode int

const (
	modeDefault     permMode = iota // SDK "default": ask before each tool
	modeAcceptEdits                 // SDK "acceptEdits": auto-accept edits (attach default)
	modePlan                        // SDK "plan": read-only planning
	modeBypass                      // SDK "bypassPermissions": yolo
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

// modeLine renders the CC-style permission-mode footer (row 4 of the status
// line): a bold glyph+label in the mode's hue.
func (p permMode) modeLine() string {
	var glyph, label string
	var col color.Color
	switch p {
	case modeAcceptEdits:
		glyph, label, col = "⏵⏵", "auto mode on", theme.Guac
	case modeDefault:
		glyph, label, col = "⏵", "ask each tool", theme.Malibu
	case modePlan:
		glyph, label, col = "⏸", "plan mode on", theme.Gold
	case modeBypass:
		glyph, label, col = "⏵⏵", "bypass permissions on", theme.Coral
	}
	return lipgloss.NewStyle().Foreground(col).Bold(true).Render(glyph + " " + label)
}

// modeTag is the compact permission-mode tag for the collapsed status row (A2.5):
// a glyph + short label in the mode's hue, vs modeLine's verbose "auto mode on"
// footer form. Not bold — it sits as a quiet trailing tag on row 1.
func (p permMode) modeTag() string {
	var glyph, label string
	var col color.Color
	switch p {
	case modeAcceptEdits:
		glyph, label, col = "⏵⏵", "auto", theme.Guac
	case modeDefault:
		glyph, label, col = "⏵", "ask", theme.Malibu
	case modePlan:
		glyph, label, col = "⏸", "plan", theme.Gold
	case modeBypass:
		glyph, label, col = "⏵⏵", "bypass", theme.Coral
	}
	return lipgloss.NewStyle().Foreground(col).Render(glyph + " " + label)
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
// while a turn runs (Stage 1, docs/ghostty-terminal-effects.md). Empty cells use
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

// fmtTokenLimit formats a token count compactly: 1000000→"1.0m", 200000→"200k".
func fmtTokenLimit(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return formatInt(n)
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
	return m.inTok + m.cacheReadTok + m.cacheWriteTok
}

// activeModelWindow returns the per-model weekly usage window (Opus or Sonnet)
// matching the attached model, when the plan exposes one. Max plans carry a
// separate weekly cap per model; the status strip surfaces just the one for the
// model in use rather than every window (the full set rides on the event for a
// future /usage view). Haiku, unknown, or unset models have no per-model cap, so
// ok is false. label is "opus"/"sonnet" for the row.
func (m *TranscriptModel) activeModelWindow() (util float64, reset time.Time, label string, ok bool) {
	id := strings.ToLower(m.model)
	switch {
	case strings.Contains(id, "opus") && m.rlOpusSeen:
		return m.rlOpusUtil, m.rlOpusReset, "opus", true
	case strings.Contains(id, "sonnet") && m.rlSonnetSeen:
		return m.rlSonnetUtil, m.rlSonnetReset, "sonnet", true
	}
	return 0, time.Time{}, "", false
}

// renderStatusLine renders the 4-row CC-clone status line, themed via the
// Phase-0a tokens. Always visible (idle and during a turn).
func (m *TranscriptModel) renderStatusLine() string {
	muted := lipgloss.NewStyle().Foreground(theme.TextMuted) // connective text
	label := lipgloss.NewStyle().Foreground(theme.TextSecondary)
	sep := muted.Render(" ─ ")

	// Row 1: model ─ cwd ─ branch* ─ used/limit pct ●bar · $cost.
	segs := []string{
		lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(shortModelName(m.model)),
		lipgloss.NewStyle().Foreground(theme.TextBody).Render(m.statusCwd()),
	}
	if m.branch != "" {
		b := m.branch
		if m.dirty {
			b += "*"
		}
		branch := lipgloss.NewStyle().Foreground(theme.Peach).Render(b)
		if m.ahead > 0 || m.behind > 0 {
			branch += muted.Render(fmt.Sprintf(" ↑%d↓%d", m.ahead, m.behind))
		}
		segs = append(segs, branch)
	}

	limit := m.ctxLimit
	if limit <= 0 {
		limit = 200000
	}
	used := m.ctxTokens()
	frac := float64(used) / float64(limit)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	pct := int(frac*100 + 0.5)
	warn := ""
	if pct >= 80 {
		warn = lipgloss.NewStyle().Foreground(theme.Coral).Bold(true).Render("! ")
	}
	// Stage 1: while a turn runs on a truecolor terminal (and motion is allowed),
	// the gauge fill becomes a live gradient that shimmers per work-tick frame and
	// pulses coral past 80%. Everywhere else (idle, NO_COLOR, non-truecolor, tests)
	// it falls back to the static single-color bar — byte-identical to today.
	bar := blockBar(frac, 10, rampColor(frac))
	switch {
	case m.caps.KittyGraphics && !m.caps.ReduceMotion:
		// Stage 3: the showpiece — a rasterized 10×1-cell gauge via Kitty Unicode
		// placeholders. Overrides the shimmer on Ghostty (the richer rendering).
		// Suppressed under the global off switch (NO_COLOR / SANDBOX_REDUCE_MOTION)
		// so output degrades to the block bar (D2/D4).
		bar = m.ctxGaugeKitty(frac)
	case m.caps.TrueColor && !m.caps.ReduceMotion && m.status == StatusBusy:
		bar = shimmerBlockBar(frac, 10, m.workFrame, pct >= 80)
	}
	ctx := lipgloss.NewStyle().Foreground(theme.TextBody).Render(fmt.Sprintf("%s/%s ", fmtTokenLimit(used), fmtTokenLimit(limit))) +
		warn +
		lipgloss.NewStyle().Foreground(rampColor(frac)).Render(fmt.Sprintf("%d%% ", pct)) +
		bar
	segs = append(segs, ctx)

	row1 := strings.Join(segs, sep)
	if m.costUSD > 0 {
		row1 += muted.Render(" · ") + lipgloss.NewStyle().Foreground(theme.Guac).Render(fmt.Sprintf("$%.4f", m.costUSD))
	}
	// Mode moves onto row 1 as a compact trailing tag (was the separate row 4).
	row1 += muted.Render(" · ") + m.mode.modeTag()

	// Row 2: a single compact claude.ai plan-usage row (5-hour + weekly util bars,
	// reset countdowns, and the attached model's weekly cap when present), from
	// real SDK /usage data (rate_limit.updated). Shown ONLY when that data is
	// available; otherwise BLANK — we never fabricate values and never show a
	// placeholder (A2.5: rate-limit detail only-when-present). The block stays a
	// fixed two rows regardless, so the body height never reflows.
	body := lipgloss.NewStyle().Foreground(theme.TextBody)
	row2 := ""
	switch {
	case m.rlSeen && m.rlAvailable:
		f5 := m.rl5hUtil / 100
		f7 := m.rl7dUtil / 100
		row2 = label.Render("5h: ") + blockBar(f5, 8, rampColor(f5)) +
			body.Render(fmt.Sprintf(" %d%%", int(m.rl5hUtil+0.5))) +
			muted.Render(" "+fmtReset(m.rl5hReset)) +
			muted.Render(" · ") + label.Render("weekly: ") + blockBar(f7, 8, rampColor(f7)) +
			body.Render(fmt.Sprintf(" %d%%", int(m.rl7dUtil+0.5))) +
			muted.Render(" "+fmtReset(m.rl7dReset))
		// Per-model weekly cap for the attached model (Max plans): percent + reset.
		if mUtil, mReset, lbl, ok := m.activeModelWindow(); ok {
			row2 += muted.Render(" · ") + label.Render(lbl+": ") +
				lipgloss.NewStyle().Foreground(rampColor(mUtil/100)).Render(fmt.Sprintf("%d%%", int(mUtil+0.5))) +
				muted.Render(" · "+lbl+" in "+fmtReset(mReset))
		}
	case m.rlSeen && !m.rlAvailable:
		// Plan limits don't apply to this session. Rather than leave row 2 blank
		// (reads as a glitch — the original "usage unavailable" complaint), name
		// the reason. An empty subscription type means headless setup-token /
		// API-key auth, where the experimental /usage API carries no plan limits.
		reason := "n/a"
		if m.rlSubscription == "" {
			reason = "n/a (headless auth)"
		}
		row2 = label.Render("usage: ") + muted.Render(reason)
	}

	return " " + row1 + "\n " + row2
}
