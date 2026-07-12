package kit

// components.go — the design-system component kit: stateless, theme-aware
// render helpers — data in, styled string out. No Bubble Tea dependency and no
// internal state, so each is trivially unit-testable. Components name a
// semantic Role, never a raw color.

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Role is the kit's semantic accent selector — it maps to the theme's semantic
// accent roles. Components name a Role, never a raw color.
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

// numRoles is the count of defined Roles — the size of the palette's role table.
const numRoles = int(RoleMuted) + 1

// ComponentColors carries the theme-driven component palette. A zero field keeps
// the current value, so callers set only what they have.
type ComponentColors struct {
	KbdKey, KbdLabel, KbdSep, KVKey, KVVal, ErrDetail, ButtonBlur color.Color
	// Rule colors SectionHeader/Section's flat `─` rule; ScrollThumb colors the
	// Scrollbar thumb glyph.
	Rule, ScrollThumb color.Color
	Roles             map[Role]color.Color
}

// SetComponentColors swaps the component palette to follow the active theme. It
// copy-modify-stores the shared palette atomically, so it is safe to call while
// another goroutine renders (two tea.Programs never race on the palette). A zero
// field keeps the current value.
func SetComponentColors(c ComponentColors) {
	cur := *pal()
	if c.KbdKey != nil {
		cur.kbdKey = c.KbdKey
	}
	if c.KbdLabel != nil {
		cur.kbdLabel = c.KbdLabel
	}
	if c.KbdSep != nil {
		cur.kbdSep = c.KbdSep
	}
	if c.KVKey != nil {
		cur.kvKey = c.KVKey
	}
	if c.KVVal != nil {
		cur.kvVal = c.KVVal
	}
	if c.ErrDetail != nil {
		cur.errDetail = c.ErrDetail
	}
	if c.ButtonBlur != nil {
		cur.btnBlur = c.ButtonBlur
	}
	if c.Rule != nil {
		cur.rule = c.Rule
	}
	if c.ScrollThumb != nil {
		cur.thumb = c.ScrollThumb
	}
	for r, col := range c.Roles {
		if col != nil && int(r) >= 0 && int(r) < len(cur.roles) {
			cur.roles[r] = col
		}
	}
	activePalette.Store(&cur)
}

// roleAccent returns the accent color for a Role, falling back to the muted
// accent for an out-of-range or unset role.
func roleAccent(r Role) color.Color {
	p := pal()
	if int(r) >= 0 && int(r) < len(p.roles) && p.roles[r] != nil {
		return p.roles[r]
	}
	return p.roles[RoleMuted]
}

// Kbd renders a single key hint, e.g. Kbd("a","approve") → "[a] approve".
func Kbd(key, label string) string {
	p := pal()
	k := lipgloss.NewStyle().Foreground(p.kbdKey).Render("[" + key + "]")
	return k + " " + lipgloss.NewStyle().Foreground(p.kbdLabel).Render(label)
}

// kbdSeparator joins key hints; one shared separator so every footer/box agrees.
const kbdSeparatorText = " · "

// KbdRow joins several key hints with the one shared separator.
func KbdRow(pairs ...[2]string) string {
	hints := make([]string, len(pairs))
	for i, p := range pairs {
		hints[i] = Kbd(p[0], p[1])
	}
	sep := lipgloss.NewStyle().Foreground(pal().kbdSep).Render(kbdSeparatorText)
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
	return lipgloss.NewStyle().Foreground(pal().btnBlur).Padding(0, 1).Render(label)
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
	// without losing their content styling. When zero/nil they don't change
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
	p := pal()
	return lipgloss.NewStyle().Foreground(p.kvKey).Render(k) + " " +
		lipgloss.NewStyle().Foreground(p.kvVal).Render(val)
}

// ErrorBlock renders a structured, actionable error: title, optional detail, and
// optional suggested action, each on its own line. Missing detail/action emit no
// empty lines.
func ErrorBlock(title, detail, action string) string {
	lines := []string{lipgloss.NewStyle().Foreground(roleAccent(RoleError)).Bold(true).Render("✗ " + title)}
	if detail != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(pal().errDetail).Render("  "+detail))
	}
	if action != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(roleAccent(RoleInfo)).Render("  "+action))
	}
	return strings.Join(lines, "\n")
}

// truncateCell trims s to at most max display columns, appending … when cut.
// ANSI-aware via ansi.Truncate: escape sequences cost zero columns and are never
// cut mid-sequence, and width math stays grapheme/wide-char correct (never
// len-based).
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
	return ansi.Truncate(s, max, "…")
}
