package kit

// components.go — the Tier-4 design-system component kit (design-system-and-
// states.md §1): stateless, theme-aware render helpers — data in, styled string
// out. No Bubble Tea dependency and no internal state, so each is trivially
// unit-testable. Components name a semantic Role, never a raw color.

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Role is the kit's semantic accent selector — it maps to the theme roles in
// chat-styling-and-motion.md §A.1. Components name a Role, never a raw color.
type Role int

const (
	RoleBrand Role = iota
	RoleBusy
	RoleWaiting
	RoleSuccess
	RoleDenied
	RoleError
	RoleInfo
	RoleMuted
)

// Component palette. Defaults are on-brand; the dashboard theme can swap them
// via SetComponentColors so the kit reskins with the active theme.
var (
	kbdKeyColor    color.Color = color.RGBA{R: 0x7b, G: 0xb6, B: 0xff, A: 0xff}
	kbdLabelColor  color.Color = color.RGBA{R: 0xb6, G: 0xaf, B: 0xd2, A: 0xff}
	kbdSepColor    color.Color = color.RGBA{R: 0x6b, G: 0x6b, B: 0x6b, A: 0xff}
	kvKeyColor     color.Color = color.RGBA{R: 0x92, G: 0x8a, B: 0xae, A: 0xff}
	kvValColor     color.Color = color.RGBA{R: 0xb6, G: 0xaf, B: 0xd2, A: 0xff}
	errDetailColor color.Color = color.RGBA{R: 0xb6, G: 0xaf, B: 0xd2, A: 0xff}
	btnBlurColor   color.Color = color.RGBA{R: 0xb6, G: 0xaf, B: 0xd2, A: 0xff}
)

// ComponentColors carries the theme-driven component palette. A zero field keeps
// the current value, so callers set only what they have.
type ComponentColors struct {
	KbdKey, KbdLabel, KbdSep, KVKey, KVVal, ErrDetail, ButtonBlur color.Color
	Roles                                                         map[Role]color.Color
}

// SetComponentColors swaps the component palette to follow the active theme.
func SetComponentColors(c ComponentColors) {
	if c.KbdKey != nil {
		kbdKeyColor = c.KbdKey
	}
	if c.KbdLabel != nil {
		kbdLabelColor = c.KbdLabel
	}
	if c.KbdSep != nil {
		kbdSepColor = c.KbdSep
	}
	if c.KVKey != nil {
		kvKeyColor = c.KVKey
	}
	if c.KVVal != nil {
		kvValColor = c.KVVal
	}
	if c.ErrDetail != nil {
		errDetailColor = c.ErrDetail
	}
	if c.ButtonBlur != nil {
		btnBlurColor = c.ButtonBlur
	}
	for r, col := range c.Roles {
		if col != nil {
			roleColorTable[r] = col
		}
	}
}

// roleColorTable maps a Role to its accent color (overridable per theme).
var roleColorTable = map[Role]color.Color{
	RoleBrand:   color.RGBA{R: 0x6b, G: 0x50, B: 0xff, A: 0xff},
	RoleBusy:    color.RGBA{R: 0xd9, G: 0xe6, B: 0x4e, A: 0xff},
	RoleWaiting: color.RGBA{R: 0xff, G: 0xc2, B: 0x47, A: 0xff},
	RoleSuccess: color.RGBA{R: 0x2f, G: 0xd9, B: 0x8b, A: 0xff},
	RoleDenied:  color.RGBA{R: 0xe0, G: 0x8a, B: 0x4a, A: 0xff},
	RoleError:   color.RGBA{R: 0xff, G: 0x52, B: 0x77, A: 0xff},
	RoleInfo:    color.RGBA{R: 0x54, G: 0xcb, B: 0xe0, A: 0xff},
	RoleMuted:   color.RGBA{R: 0x80, G: 0x79, B: 0xa0, A: 0xff},
}

// roleAccent returns the accent color for a Role.
func roleAccent(r Role) color.Color {
	if c, ok := roleColorTable[r]; ok {
		return c
	}
	return roleColorTable[RoleMuted]
}

// Kbd renders a single key hint, e.g. Kbd("a","approve") → "[a] approve".
func Kbd(key, label string) string {
	k := lipgloss.NewStyle().Foreground(kbdKeyColor).Render("[" + key + "]")
	return k + " " + lipgloss.NewStyle().Foreground(kbdLabelColor).Render(label)
}

// kbdSeparator joins key hints; one shared separator so every footer/box agrees.
const kbdSeparatorText = " · "

// KbdRow joins several key hints with the one shared separator.
func KbdRow(pairs ...[2]string) string {
	hints := make([]string, len(pairs))
	for i, p := range pairs {
		hints[i] = Kbd(p[0], p[1])
	}
	sep := lipgloss.NewStyle().Foreground(kbdSepColor).Render(kbdSeparatorText)
	return strings.Join(hints, sep)
}

// Badge renders a small status/label pill; role picks fg+bg from the theme. Its
// geometry (1 column of side padding) is role-independent.
func Badge(text string, role Role) string {
	bg := roleAccent(role)
	return lipgloss.NewStyle().Foreground(OnColor(bg)).Background(bg).Padding(0, 1).Render(text)
}

// Button renders an inline action with a visibly distinct focused variant.
func Button(label string, focused bool) string {
	if focused {
		bg := roleAccent(RoleBrand)
		return lipgloss.NewStyle().Foreground(OnColor(bg)).Background(bg).Bold(true).Padding(0, 1).Render(label)
	}
	return lipgloss.NewStyle().Foreground(btnBlurColor).Padding(0, 1).Render(label)
}

// CardOpts configures a Card.
type CardOpts struct {
	Title     string
	Body      string
	Accent    Role
	Width     int
	Collapsed bool

	// The following are optional extensions that let bespoke bordered surfaces
	// (permission box, plan card, confirm dialog) share the kit-owned frame
	// without losing their content styling (D2). When zero/nil they don't change
	// the default Title/Body behaviour, so the canonical Card tests are unaffected.

	// Content, when set, is used verbatim as the card's inner content (Title and
	// Body are ignored). The caller owns its styling — Card only frames it. Use
	// this to migrate a surface that pre-renders its own title/body/hints.
	Content string
	// BorderColor overrides the Accent-derived border color (e.g. a per-frame
	// animated color, or a non-role color like gold/coral).
	BorderColor color.Color
	// Background optionally fills the card.
	Background color.Color
	// PadV/PadH are the padding inside the border (default 0,0 — flush).
	PadV, PadH int
}

// Card renders a rounded-border block. In the default (Title/Body) mode every
// line is exactly Width wide, with the title on the first inner line and the
// body below (omitted when Collapsed); content longer than the inner width
// truncates with … rather than overflowing. With Content set, the given string
// is framed verbatim (its styling preserved) — and a non-positive Width fits the
// border to the content instead of padding to a fixed width.
func Card(opts CardOpts) string {
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	if opts.BorderColor != nil {
		st = st.BorderForeground(opts.BorderColor)
	} else {
		st = st.BorderForeground(roleAccent(opts.Accent))
	}
	if opts.Background != nil {
		st = st.Background(opts.Background)
	}
	if opts.PadV != 0 || opts.PadH != 0 {
		st = st.Padding(opts.PadV, opts.PadH)
	}

	if opts.Content != "" {
		if opts.Width > 0 {
			st = st.Width(opts.Width)
		}
		return st.Render(opts.Content)
	}

	w := opts.Width
	if w < 4 {
		w = 4
	}
	inner := w - 2 - 2*opts.PadH // content columns inside the rounded border (+ padding)
	if inner < 1 {
		inner = 1
	}
	title := truncateCell(opts.Title, inner)
	content := lipgloss.NewStyle().Bold(true).Foreground(roleAccent(opts.Accent)).Render(title)
	if opts.Body != "" && !opts.Collapsed {
		var bodyLines []string
		for _, l := range strings.Split(opts.Body, "\n") {
			bodyLines = append(bodyLines, truncateCell(l, inner))
		}
		content += "\n" + strings.Join(bodyLines, "\n")
	}
	return st.Width(w). // lipgloss Width is the total frame width (border included)
				Render(content)
}

// Section renders a section header — the kit-owned alias of SectionHeader.
func Section(title string, width int, info ...string) string {
	return SectionHeader(title, width, info...)
}

// KV renders an aligned key/value row: the key column is fixed at keyWidth
// (truncated if longer) so values line up.
func KV(key, val string, keyWidth int) string {
	k := truncateCell(key, keyWidth)
	if pad := keyWidth - lipgloss.Width(k); pad > 0 {
		k += strings.Repeat(" ", pad)
	}
	return lipgloss.NewStyle().Foreground(kvKeyColor).Render(k) + " " +
		lipgloss.NewStyle().Foreground(kvValColor).Render(val)
}

// ErrorBlock renders a structured, actionable error: title, optional detail, and
// optional suggested action, each on its own line. Missing detail/action emit no
// empty lines.
func ErrorBlock(title, detail, action string) string {
	lines := []string{lipgloss.NewStyle().Foreground(roleAccent(RoleError)).Bold(true).Render("✗ " + title)}
	if detail != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(errDetailColor).Render("  "+detail))
	}
	if action != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(roleAccent(RoleInfo)).Render("  "+action))
	}
	return strings.Join(lines, "\n")
}

// truncateCell trims s to at most max display columns, appending … when cut, so
// width math stays grapheme/wide-char correct (never len-based).
func truncateCell(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	var b strings.Builder
	w := 0
	for _, c := range graphemeClusters(s) {
		cw := lipgloss.Width(c)
		if w+cw > max-1 {
			break
		}
		b.WriteString(c)
		w += cw
	}
	b.WriteString("…")
	return b.String()
}
