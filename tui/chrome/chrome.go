// Package chrome is a public collection of the reusable transcript "chrome" —
// the frame around the conversation: a width-budgeted status line, a context /
// token gauge, a live working indicator, and calm one-line notices. These are
// promoted from the dashboard's statusline.go, generalized and freed of any app
// transport or lifecycle policy (no session modes, sync state, or rate-limit
// rows). The transient scrollbar (kit.Scrollbar), token/cost formatters
// (kit.FormatTokens / kit.FormatCost), and the transition catalog (tui/anim)
// already live in public packages; this package fills the remaining gaps.
//
// Everything here is width/ANSI/grapheme-safe and reads its colors from
// tui/theme, so a /theme swap re-skins it. Nothing imports internal/.
package chrome

import (
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// --- status line -------------------------------------------------------------

// Segment is one status-line cell. A required segment is always kept; optional
// segments are shed tail-first when the row would overflow. Each segment should
// bake in its own leading separator so dropping a middle one leaves no dangling
// separator.
type Segment struct {
	Text     string
	Required bool
}

// Seg builds an optional segment.
func Seg(text string) Segment { return Segment{Text: text} }

// Req builds a required segment.
func Req(text string) Segment { return Segment{Text: text, Required: true} }

// StatusLine joins segments left-to-right within width columns, keeping every
// required segment and including optional ones only while they (plus the
// still-unplaced required segments) fit. A final ANSI-aware truncate is the
// backstop when even the required segments overflow. A width <= 0 means "no
// budget" — everything is kept. Ported from the dashboard's budgetRow.
func StatusLine(width int, segs ...Segment) string {
	budget := width
	if budget <= 0 {
		budget = 1 << 30
	}
	reqAfter := make([]int, len(segs)+1)
	for i := len(segs) - 1; i >= 0; i-- {
		reqAfter[i] = reqAfter[i+1]
		if segs[i].Required {
			reqAfter[i] += lipgloss.Width(segs[i].Text)
		}
	}
	var b strings.Builder
	cur := 0
	for i, sg := range segs {
		w := lipgloss.Width(sg.Text)
		if !sg.Required && cur+w+reqAfter[i+1] > budget {
			continue
		}
		b.WriteString(sg.Text)
		cur += w
	}
	out := b.String()
	if width > 0 && lipgloss.Width(out) > width {
		out = clamp(out, width)
	}
	return out
}

// --- context / token gauge ---------------------------------------------------

// gaugeThreshold is the ctx% below which ContextGauge stays quiet (a roomy
// context needs no gauge). gaugeWarn is where it turns coral.
const (
	gaugeThreshold = 60
	gaugeWarn      = 80
	gaugeCells     = 10
)

// RampColor maps a 0..1 fraction onto the calm→warn→alarm ramp (Guac → Gold →
// Coral). Clamped to [0,1].
func RampColor(frac float64) color.Color {
	frac = clampFrac(frac)
	ramp := lipgloss.Blend1D(24, theme.Guac, theme.Gold, theme.Coral)
	return ramp[int(frac*float64(len(ramp)-1))]
}

// BlockBar renders an n-cell block gauge for a 0..1 fraction: filled cells in
// the ramp color, empty cells recessed dim.
func BlockBar(frac float64, n int) string {
	frac = clampFrac(frac)
	if n < 1 {
		n = 1
	}
	fill := int(frac * float64(n))
	if fill > n {
		fill = n
	}
	return lipgloss.NewStyle().Foreground(RampColor(frac)).Render(strings.Repeat("█", fill)) +
		lipgloss.NewStyle().Foreground(theme.TextDim).Render(strings.Repeat("░", n-fill))
}

// ContextGauge renders the "pct% <bar>" context indicator from a token count and
// a context-window limit, or "" when the limit is unknown (<= 0) or the context
// is still roomy (below the 60% threshold). Past 80% it prefixes a coral "! "
// warning. The pct color tracks the ramp.
func ContextGauge(tokens, limit int) string {
	if limit <= 0 {
		return ""
	}
	frac := clampFrac(float64(tokens) / float64(limit))
	pct := int(frac*100 + 0.5)
	if pct < gaugeThreshold {
		return ""
	}
	warn := ""
	if pct >= gaugeWarn {
		warn = lipgloss.NewStyle().Foreground(theme.Coral).Bold(true).Render("! ")
	}
	return warn +
		lipgloss.NewStyle().Foreground(RampColor(frac)).Render(itoa(pct)+"% ") +
		BlockBar(frac, gaugeCells)
}

// --- working indicator -------------------------------------------------------

// Working describes the live working indicator's content. All fields are
// optional; a zero Working renders a bare "working…".
type Working struct {
	// Verb is the context-aware action word ("Thinking", "Running Bash",
	// "Writing"); defaults to "Working".
	Verb string
	// Elapsed is the turn's running duration; shown when > 0.
	Elapsed time.Duration
	// OutputTokens is the running output token count; shown when > 0.
	OutputTokens int
	// Hint is the trailing affordance ("esc to interrupt", "esc to steer").
	Hint string
	// Frame advances the animated ellipsis (host-driven, so motion is opt-in and
	// goldens stay deterministic at frame 0). Ignored under reduced motion.
	Frame int
}

// WorkingIndicator renders the single-line live indicator shown above the
// composer while a turn runs, e.g. "✳ Thinking… · 12s · ↓820 tokens · esc to
// interrupt". Honors anim.ReduceMotion (a static "…" instead of the animated
// ellipsis) so reduced-motion terminals and goldens stay stable.
func WorkingIndicator(w Working) string {
	verb := w.Verb
	if verb == "" {
		verb = "Working"
	}
	ell := "…"
	if !anim.ReduceMotion() {
		ell = anim.Ellipsis(w.Frame)
	}
	glyph := lipgloss.NewStyle().Foreground(theme.Busy).Render("✳ ")
	head := lipgloss.NewStyle().Foreground(theme.Busy).Render(verb + ell)

	var parts []string
	if w.Elapsed > 0 {
		parts = append(parts, fmtElapsed(w.Elapsed))
	}
	if w.OutputTokens > 0 {
		parts = append(parts, "↓"+kit.FormatTokens(w.OutputTokens)+" tokens")
	}
	if strings.TrimSpace(w.Hint) != "" {
		parts = append(parts, w.Hint)
	}
	out := glyph + head
	if len(parts) > 0 {
		out += lipgloss.NewStyle().Foreground(theme.TextMuted).Render(" · " + strings.Join(parts, " · "))
	}
	return out
}

// --- calm notices ------------------------------------------------------------

// NoticeKind is a notice's tone.
type NoticeKind int

const (
	// NoticeInfo is the quietest tone (muted).
	NoticeInfo NoticeKind = iota
	// NoticeWarn sits between info and error (warning tone).
	NoticeWarn
	// NoticeError is the loudest (coral).
	NoticeError
)

// Notice renders a calm one-line notice in the given tone, clamped to width —
// the chrome-level transient line.
func Notice(kind NoticeKind, text string, width int) string {
	var c color.Color
	switch kind {
	case NoticeWarn:
		c = theme.Warning
	case NoticeError:
		c = theme.Coral
	default:
		c = theme.TextMuted
	}
	line := lipgloss.NewStyle().Foreground(c).Render(strings.TrimSpace(text))
	if width > 0 {
		line = clamp(line, width)
	}
	return line
}

// --- helpers -----------------------------------------------------------------

func clampFrac(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func clamp(s string, w int) string {
	if w < 1 {
		w = 1
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

func fmtElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return itoa(s) + "s"
	}
	mn, sec := s/60, s%60
	pad := ""
	if sec < 10 {
		pad = "0"
	}
	return itoa(mn) + "m" + pad + itoa(sec) + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
