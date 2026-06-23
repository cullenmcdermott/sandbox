package main

import (
	"fmt"
	"image/color"
	"strings"

	glamour "charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// --------------------------------------------------------------------------
// Design variants — each is a set of layout/styling decisions applied to the
// same conversation. The point is to make the trade-offs visible side by side.
// --------------------------------------------------------------------------

type footerMode int

const (
	footerEvery footerMode = iota // a model/cost line under every assistant turn (today)
	footerLast                    // only under the most recent turn
	footerOff                     // never
)

type chrome struct {
	name, short, blurb string
	themed             bool // theme-derived glamour vs stock "dark"
	gutter             bool // a slim role-colored bar down the left of each message
	thinBar            bool // use a 1px bar instead of the half-block
	roleLabel          bool // a small "You / Claude" label above each message
	leftPad            int
	turnGap            int // blank lines between turns
	footer             footerMode
	mutedTools         bool // dim tool cards instead of the loud Malibu name
	slimHeader         bool // 1-line header + 1-line status instead of today's stack
}

func (c chrome) barGlyph() string {
	if c.thinBar {
		return "▏ "
	}
	return "▌ "
}

// inlinePrefix is the Today/Themed presentation: a "❯ " user prefix, no gutter,
// no role label — assistant text sits at the left edge.
func (c chrome) inlinePrefix() bool { return !c.gutter && !c.roleLabel && c.leftPad == 0 }

const (
	idxToday = iota
	idxThemed
	idxCalm
	idxReading
	idxCompact
)

var variants = []chrome{
	{
		name: "Today (current)", short: "Today",
		blurb:  "What ships now: glamour's stock 'dark' style (ignores your theme), a green ❯ prefix, a model/cost footer under EVERY turn, a full-width divider, and the 4-row status block.",
		themed: false, footer: footerEvery, slimHeader: false,
	},
	{
		name: "Themed", short: "Themed",
		blurb:  "One change vs Today: glamour restyled from your Midnight tokens — Charple headings, calm inline code, Malibu links, theme-matched syntax. Same layout, so you can isolate how much just fixing the COLORS buys.",
		themed: true, footer: footerEvery, slimHeader: false,
	},
	{
		name: "Calm  ★recommended", short: "Calm★",
		blurb:  "Themed markdown + a slim role gutter, footer only on the latest turn, one-line status, a blank line between turns, and muted tool cards. The de-busied default.",
		themed: true, gutter: true, leftPad: 1, turnGap: 1,
		footer: footerLast, mutedTools: true, slimHeader: true,
	},
	{
		name: "Reading", short: "Reading",
		blurb:  "Themed markdown, roomy left margin, a small 'You / Claude' label over each message, almost no chrome — tuned for reading long answers.",
		themed: true, roleLabel: true, leftPad: 3, turnGap: 1,
		footer: footerLast, mutedTools: true, slimHeader: true,
	},
	{
		name: "Compact", short: "Compact",
		blurb:  "The other extreme: thin single-bar role markers, no footers, zero gaps — maximum content per screen for power users.",
		themed: true, gutter: true, thinBar: true, turnGap: 0,
		footer: footerOff, mutedTools: true, slimHeader: true,
	},
}

// --------------------------------------------------------------------------
// Sample conversation (markdown exercises headings, lists, inline code, a Go
// fence, emphasis, a blockquote, and a link — the stuff that looks worst today).
// --------------------------------------------------------------------------

type toolLine struct {
	status         byte // 'o' ok, 'e' err, 'r' running
	name, arg, sum string
}

type turn struct {
	user                string
	tools               []toolLine
	md                  string
	model, elapsed, cst string
	inTok, outTok       int
}

var conv = []turn{
	{
		user: "Why did Kubernetes add the Lease API, and when should I use it over a status field?",
		tools: []toolLine{
			{'o', "Read", "internal/k8s/backend.go", "142 lines"},
			{'o', "Grep", "coordination.k8s.io", "6 matches"},
		},
		md: "Kubernetes added the **Lease API** (`coordination.k8s.io/v1`) because kubelet heartbeats were rewriting the whole Node status and overwhelming etcd. Splitting the liveness signal into tiny `Lease` objects made heartbeats cheap.\n\n" +
			"## When to reach for a Lease\n\n" +
			"- **Low frequency / few objects** — put `lastActiveTime` in `.status` and move on.\n" +
			"- **High frequency / many objects** — use a `Lease` per entity and reflect a coarse `phase` into the CR status periodically.\n\n" +
			"Readers poll the Lease for *\"is it live?\"*; the CR status answers *\"what is it?\"*\n\n" +
			"```go\nfunc (r *Reconciler) touch(ctx context.Context, l *coordv1.Lease) error {\n    now := metav1.NowMicro()\n    l.Spec.RenewTime = &now // cheap: one tiny object\n    return r.Update(ctx, l)\n}\n```\n\n" +
			"> Rule of thumb: if a field changes faster than you'd want to PATCH the CR, it belongs in a Lease.\n\n" +
			"See the [upstream KEP-589](https://example.com) for the full rationale.",
		model: "Opus 4.8", elapsed: "5s", inTok: 2500, outTok: 612, cst: "$0.03",
	},
	{
		user: "now wire lastActiveTime into the reconciler",
		tools: []toolLine{
			{'o', "Read", "api/v1alpha1/types.go", "88 lines"},
			{'r', "Edit", "controller/reconcile.go", ""},
		},
		md: "I'll add a `LastActiveTime metav1.Time` to the status and stamp it on every successful reconcile:\n\n" +
			"1. Add the field plus a printer column.\n" +
			"2. Stamp it in `updateStatus` after the work succeeds.\n" +
			"3. Gate the \"presumed dead\" check on `now - LastActiveTime > ttl`.\n\n" +
			"Want me to add the **TTL flag** to the manager as well?",
		model: "Opus 4.8", elapsed: "3s", inTok: 2500, outTok: 140, cst: "$0.01",
	},
}

// --------------------------------------------------------------------------
// Transcript body
// --------------------------------------------------------------------------

func (m *model) transcriptLines(ch chrome) []string {
	var lines []string
	add := func(ss ...string) { lines = append(lines, ss...) }
	gap := func() {
		for i := 0; i < ch.turnGap; i++ {
			add("")
		}
	}

	for ti, t := range conv {
		if ti > 0 {
			gap()
		}
		add(m.renderUser(t.user, ch)...)
		if len(t.tools) > 0 {
			add(m.renderTools(t.tools, ch)...)
		}
		// Image demo sits near the top of the first turn so it's visible without
		// scrolling — the point is to confirm the Kitty path actually renders.
		if ti == 0 && m.imageOn {
			add("")
			add(m.renderImageBlock(ch)...)
			add("")
		}
		add(m.renderAssistant(t.md, ch)...)

		isLast := ti == len(conv)-1
		if ch.footer == footerEvery || (ch.footer == footerLast && isLast) {
			add(m.renderFooter(t, ch))
		}
		if ti == 0 {
			gap()
			add(place(noticeLine(), ch))
		}
	}
	return lines
}

func (m *model) renderUser(text string, ch chrome) []string {
	cw := contentWidth(m.width, ch)
	wrapped := wrap(text, cw)
	switch {
	case ch.roleLabel:
		out := []string{pad(ch.leftPad) + lipgloss.NewStyle().Foreground(theme.Guac).Bold(true).Render("You")}
		for _, l := range wrapped {
			out = append(out, pad(ch.leftPad)+lipgloss.NewStyle().Foreground(theme.TextBright).Render(l))
		}
		return out
	case ch.gutter:
		styled := make([]string, len(wrapped))
		for i, l := range wrapped {
			styled[i] = lipgloss.NewStyle().Foreground(theme.TextBright).Render(l)
		}
		return gutterLines(styled, ch, theme.Guac)
	default: // inline ❯ prefix
		pfx := lipgloss.NewStyle().Foreground(theme.Guac).Bold(true).Render("❯ ")
		out := make([]string, 0, len(wrapped))
		for i, l := range wrapped {
			body := lipgloss.NewStyle().Foreground(theme.TextBright).Render(l)
			if i == 0 {
				out = append(out, pfx+body)
			} else {
				out = append(out, "  "+body)
			}
		}
		return out
	}
}

func (m *model) renderAssistant(md string, ch chrome) []string {
	cw := contentWidth(m.width, ch)
	style := "stock"
	if ch.themed {
		style = "themed"
	}
	lines := strings.Split(mdRender(style, md, cw), "\n")
	switch {
	case ch.roleLabel:
		out := []string{pad(ch.leftPad) + lipgloss.NewStyle().Foreground(theme.Charple).Bold(true).Render("Claude")}
		for _, l := range lines {
			out = append(out, pad(ch.leftPad)+l)
		}
		return out
	case ch.gutter:
		return gutterLines(lines, ch, theme.Charple)
	default:
		if ch.leftPad == 0 {
			return lines
		}
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = pad(ch.leftPad) + l
		}
		return out
	}
}

func (m *model) renderTools(tools []toolLine, ch chrome) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		var icon string
		var ic = theme.Malibu
		switch t.status {
		case 'o':
			icon, ic = "✓", theme.Guac
		case 'e':
			icon, ic = "✗", theme.Coral
		default:
			icon, ic = "⏵", theme.Malibu
		}
		nameCol, argCol := theme.Malibu, theme.TextBody
		if ch.mutedTools {
			nameCol, argCol = theme.TextSecondary, theme.TextMuted
		}
		line := lipgloss.NewStyle().Foreground(ic).Render(icon) + " " +
			lipgloss.NewStyle().Foreground(nameCol).Bold(!ch.mutedTools).Render(t.name)
		if t.arg != "" {
			line += "  " + lipgloss.NewStyle().Foreground(argCol).Render(t.arg)
		}
		if t.sum != "" {
			line += lipgloss.NewStyle().Foreground(theme.TextMuted).Render("  · " + t.sum)
		}
		out = append(out, place(line, ch))
	}
	return out
}

func (m *model) renderFooter(t turn, ch chrome) string {
	slim := ch.slimHeader
	var s string
	if slim {
		s = fmt.Sprintf("%s · %s · ↑%s ↓%s · %s",
			strings.ToLower(t.model), t.elapsed, fmtTok(t.inTok), fmtTok(t.outTok), t.cst)
	} else {
		s = fmt.Sprintf("◇ %s · via claude · %s · ↑%s ↓%s · %s",
			t.model, t.elapsed, fmtTok(t.inTok), fmtTok(t.outTok), t.cst)
	}
	return place(lipgloss.NewStyle().Foreground(theme.TextMuted).Render(s), ch)
}

// noticeLine demonstrates requirement #2's fix: a model switch is a visible,
// context-preserving event, not a silent reset.
func noticeLine() string {
	return lipgloss.NewStyle().Foreground(theme.Malibu).
		Render("⟳ model → Opus 4.8 · 47 messages kept in context")
}

func (m *model) renderHeader(ch chrome) string {
	title := lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render("sandbox")
	tag := lipgloss.NewStyle().Foreground(theme.TextDim).
		Render(fmt.Sprintf("[%d/%d] %s", m.variant+1, len(variants), ch.name))

	var status string
	switch m.phase {
	case phaseWorking:
		status = lipgloss.NewStyle().Foreground(theme.Busy).Render(theme.GlyphBusy + " working…")
	case phaseLoading:
		status = lipgloss.NewStyle().Foreground(theme.Malibu).Render("⟳ loading transcript… 142/210")
	default:
		status = lipgloss.NewStyle().Foreground(theme.Guac).Render(theme.GlyphNeedsInput + " ready")
	}
	meta := lipgloss.NewStyle().Foreground(theme.TextMuted).Render("claude · sandbox")

	line := rightAlign(title+"  "+tag, meta+"  "+status, m.width)
	if ch.slimHeader {
		return line
	}
	div := lipgloss.NewStyle().Foreground(theme.BorderMedium).Render(strings.Repeat("─", m.width))
	return line + "\n" + div
}

func (m *model) renderBottom(ch chrome) string {
	var b []string
	b = append(b, m.renderInput(ch))
	b = append(b, m.renderStatus(ch)...)
	b = append(b, m.renderControls(ch)...)
	return strings.Join(b, "\n")
}

func (m *model) renderInput(ch chrome) string {
	ph := lipgloss.NewStyle().Foreground(theme.TextDim).Render("type a message…")
	prompt := lipgloss.NewStyle().Foreground(theme.Charple).Render("❯ ")
	if ch.slimHeader {
		left := prompt + ph
		right := lipgloss.NewStyle().Foreground(theme.TextMuted).Render("↵ send · esc detach")
		return rightAlign(left, right, m.width)
	}
	badge := lipgloss.NewStyle().Background(theme.Guac).Foreground(theme.Page).Bold(true).Padding(0, 1).Render("INSERT")
	left := badge + " " + prompt + ph
	right := lipgloss.NewStyle().Foreground(theme.TextMuted).Render("[↵] send · [esc] detach")
	return rightAlign(left, right, m.width)
}

func (m *model) renderStatus(ch chrome) []string {
	if ch.slimHeader {
		s := "opus 4.8 · ~/git/sandbox · 19k/1m (2%) · $0.0329 · auto"
		return []string{lipgloss.NewStyle().Foreground(theme.TextMuted).Render(s)}
	}
	l1 := lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render("Opus 4.8") +
		lipgloss.NewStyle().Foreground(theme.TextMuted).Render(" — ~/git/sandbox — 19k/1.0m ") +
		lipgloss.NewStyle().Foreground(theme.Malibu).Render("2%") +
		lipgloss.NewStyle().Foreground(theme.Guac).Render("    $0.0329")
	l2 := lipgloss.NewStyle().Foreground(theme.TextMuted).Render("5h: —   ·   weekly: —")
	l3 := lipgloss.NewStyle().Foreground(theme.TextDim).Render("usage limits unavailable")
	l4 := lipgloss.NewStyle().Foreground(theme.Guac).Render("▶▶ auto mode on")
	return []string{l1, l2, l3, l4}
}

// renderControls is the demo harness UI (not part of the mock) — visually
// separated by a short rule so it reads as "the lab", not "the product".
func (m *model) renderControls(ch chrome) []string {
	rule := lipgloss.NewStyle().Foreground(theme.TextDim).Render(strings.Repeat("─", min(m.width, 46)))

	picks := make([]string, len(variants))
	for i, v := range variants {
		st := lipgloss.NewStyle().Foreground(theme.TextMuted)
		if i == m.variant {
			st = lipgloss.NewStyle().Foreground(theme.Charple).Bold(true)
		}
		picks[i] = st.Render(fmt.Sprintf("%d %s", i+1, v.short))
	}

	img := "off"
	switch {
	case !m.caps.KittyGraphics:
		img = "n/a (needs kitty term)"
	case m.imageOn:
		img = "on"
	}
	keys := lipgloss.NewStyle().Foreground(theme.TextDim).Render(
		fmt.Sprintf("tab cycle · t theme:%s · i image:%s · l phase:%s · j/k scroll · q quit",
			theme.Active(), img, m.phase))
	blurb := lipgloss.NewStyle().Foreground(theme.TextSecondary).Italic(true).
		Render("» " + variants[m.variant].blurb)

	return []string{rule, strings.Join(picks, "   "), keys, blurb}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func contentWidth(w int, ch chrome) int {
	left := ch.leftPad
	if ch.gutter {
		left += 2 // bar glyph + space
	}
	cw := w - left - 1
	if cw < 24 {
		cw = 24
	}
	return cw
}

func gutterLines(lines []string, ch chrome, col color.Color) []string {
	bar := lipgloss.NewStyle().Foreground(col).Render(ch.barGlyph())
	p := pad(ch.leftPad)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = p + bar + l
	}
	return out
}

// place puts a subordinate line (tool card, footer, notice) in the message
// column without a role bar.
func place(line string, ch chrome) string {
	if ch.inlinePrefix() {
		return line
	}
	return pad(ch.leftPad) + "  " + line
}

func placeMulti(lines []string, ch chrome) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = place(l, ch)
	}
	return out
}

func pad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

func fmtTok(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func wrap(s string, w int) []string {
	if w < 4 {
		w = 4
	}
	var out []string
	var cur string
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
			cur += " " + word
		default:
			out = append(out, cur)
			cur = word
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// --------------------------------------------------------------------------
// Glamour renderers (cached per style+width+theme)
// --------------------------------------------------------------------------

var rcache = map[string]*glamour.TermRenderer{}

func getRenderer(style string, width int) *glamour.TermRenderer {
	key := fmt.Sprintf("%s|%d|%s", style, width, theme.Active())
	if r, ok := rcache[key]; ok {
		return r
	}
	opt := glamour.WithStandardStyle("dark")
	if style == "themed" {
		opt = glamour.WithStyles(themedStyleConfig())
	}
	r, err := glamour.NewTermRenderer(opt, glamour.WithWordWrap(width))
	if err != nil {
		return nil
	}
	rcache[key] = r
	return r
}

func mdRender(style, md string, width int) string {
	if width < 20 {
		width = 20
	}
	r := getRenderer(style, width)
	if r == nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.TrimRight(out, "\n")
}
