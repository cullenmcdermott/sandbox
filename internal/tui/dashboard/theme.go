package dashboard

// theme.go owns every literal color in the dashboard. All rendering reads the
// active semantic tokens (the colorXxx vars below); switching the active Theme
// reskins every screen with no layout change. This mirrors the UX lab's proven
// palette/applyTheme pattern so future themes plug in by
// appending to `themes` only.
//
// Token semantics (use the right rung — don't reach for `dim` to mean
// "secondary text"):
//
//	bright   — primary text / titles
//	body     — default body text
//	second   — labels, slightly de-emphasized
//	muted    — connective text: separators, hints, timestamps
//	dim      — recessed only: empty progress tracks, disabled, ghost rules

import (
	"image/color"
	"os"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard/kit"
)

// Theme is the full set of semantic color tokens for one theme, including the
// brand/status accents (which used to be constant package vars). A Theme is
// just a table of values; applyTheme swaps them and rebuilds derived styles.
type Theme struct {
	Name string

	// Brand / gradient (wordmark, busy spinner).
	Charple, Hazy, Dolly color.Color
	// Status accents — semantic; meaning stays stable across themes.
	Gold, Guac, Coral, Malibu, Peach color.Color
	// Surfaces (page → raised stack).
	Page, Surface, Raised, Raised2 color.Color
	BorderSubtle, BorderMedium     color.Color
	// Text ramp (bright → dim).
	TextBright, TextBody, TextSecondary, TextMuted, TextDim color.Color
	// Drop-shadow tone for floating surfaces (slice 4 modal).
	Shadow color.Color

	// Extended semantic roles (chat-styling-and-motion §A.1). Busy is the
	// streaming/working accent, distinct from the brand gradient; Denied is a
	// refusal/blocked tone distinct from the error Coral. Info/Success/Warning
	// each pair a foreground accent with a near-background *Subtle fill used
	// behind it (a notice/toast = accent text on its Subtle background).
	Busy, Denied                             color.Color
	Info, Success, Warning                   color.Color
	InfoSubtle, SuccessSubtle, WarningSubtle color.Color
}

// themes is the registry. Index 0 is the default dark theme; index 1 is the
// default light theme. Values originated in the UX-lab prototype.
var themes = []Theme{
	{
		Name:    "Midnight",
		Charple: lipgloss.Color("#6B50FF"), Hazy: lipgloss.Color("#9B87FF"), Dolly: lipgloss.Color("#FF5FD1"),
		Gold: lipgloss.Color("#FFC247"), Guac: lipgloss.Color("#2FD98B"), Coral: lipgloss.Color("#FF5277"), Malibu: lipgloss.Color("#54CBE0"), Peach: lipgloss.Color("#FF9D5C"),
		Page: lipgloss.Color("#131019"), Surface: lipgloss.Color("#1B1726"), Raised: lipgloss.Color("#221C33"), Raised2: lipgloss.Color("#2A2440"),
		BorderSubtle: lipgloss.Color("#2A2440"), BorderMedium: lipgloss.Color("#3A3258"),
		TextBright: lipgloss.Color("#ECE8F7"), TextBody: lipgloss.Color("#B6AFD2"), TextSecondary: lipgloss.Color("#928AAE"), TextMuted: lipgloss.Color("#8079A0"), TextDim: lipgloss.Color("#46406A"),
		Shadow: lipgloss.Color("#0B0910"),
		Busy:   lipgloss.Color("#D9E64E"), Denied: lipgloss.Color("#E08A4A"),
		Info: lipgloss.Color("#5AB0FF"), Success: lipgloss.Color("#2FD98B"), Warning: lipgloss.Color("#FFB02E"),
		InfoSubtle: lipgloss.Color("#122533"), SuccessSubtle: lipgloss.Color("#102A1E"), WarningSubtle: lipgloss.Color("#2A2210"),
	},
	{
		Name:    "Daylight",
		Charple: lipgloss.Color("#5A3FE0"), Hazy: lipgloss.Color("#7A66E0"), Dolly: lipgloss.Color("#D83FB0"),
		Gold: lipgloss.Color("#C8860A"), Guac: lipgloss.Color("#1FA968"), Coral: lipgloss.Color("#E03058"), Malibu: lipgloss.Color("#2596B0"), Peach: lipgloss.Color("#E0763A"),
		Page: lipgloss.Color("#FBFAFF"), Surface: lipgloss.Color("#F1EEFA"), Raised: lipgloss.Color("#E6E0F4"), Raised2: lipgloss.Color("#D9D2EC"),
		BorderSubtle: lipgloss.Color("#D9D2EC"), BorderMedium: lipgloss.Color("#BEB4DC"),
		TextBright: lipgloss.Color("#1A1426"), TextBody: lipgloss.Color("#332B47"), TextSecondary: lipgloss.Color("#564E70"), TextMuted: lipgloss.Color("#6E6690"), TextDim: lipgloss.Color("#ABA2C6"),
		Shadow: lipgloss.Color("#C8C0DC"),
		Busy:   lipgloss.Color("#8A9A18"), Denied: lipgloss.Color("#C56A1E"),
		Info: lipgloss.Color("#2178C8"), Success: lipgloss.Color("#1FA968"), Warning: lipgloss.Color("#C8860A"),
		InfoSubtle: lipgloss.Color("#DCE9F6"), SuccessSubtle: lipgloss.Color("#DDF1E6"), WarningSubtle: lipgloss.Color("#F6EDD8"),
	},
	{
		Name:    "Ember",
		Charple: lipgloss.Color("#FF9D3C"), Hazy: lipgloss.Color("#FFB86B"), Dolly: lipgloss.Color("#FF6B4A"),
		Gold: lipgloss.Color("#FFC247"), Guac: lipgloss.Color("#5FD98B"), Coral: lipgloss.Color("#FF5277"), Malibu: lipgloss.Color("#54CBE0"), Peach: lipgloss.Color("#FF9D5C"),
		Page: lipgloss.Color("#16100C"), Surface: lipgloss.Color("#201711"), Raised: lipgloss.Color("#2A1F16"), Raised2: lipgloss.Color("#36281C"),
		BorderSubtle: lipgloss.Color("#2A1F16"), BorderMedium: lipgloss.Color("#4A3826"),
		TextBright: lipgloss.Color("#FBEFE2"), TextBody: lipgloss.Color("#D8C4AE"), TextSecondary: lipgloss.Color("#B09478"), TextMuted: lipgloss.Color("#8A6E54"), TextDim: lipgloss.Color("#5A442E"),
		Shadow: lipgloss.Color("#0A0704"),
		Busy:   lipgloss.Color("#D9E64E"), Denied: lipgloss.Color("#FF7B4A"),
		Info: lipgloss.Color("#54CBE0"), Success: lipgloss.Color("#5FD98B"), Warning: lipgloss.Color("#FFC247"),
		InfoSubtle: lipgloss.Color("#112226"), SuccessSubtle: lipgloss.Color("#122A1E"), WarningSubtle: lipgloss.Color("#2A2210"),
	},
}

// --- Active semantic tokens (set by applyTheme, read everywhere) ----------
var (
	colorCharple color.Color
	colorHazy    color.Color
	colorDolly   color.Color

	colorGold   color.Color
	colorGuac   color.Color
	colorCoral  color.Color
	colorMalibu color.Color
	colorPeach  color.Color

	colorPage    color.Color
	colorSurface color.Color
	colorRaised  color.Color
	colorRaised2 color.Color

	colorBorderSubtle color.Color
	colorBorderMedium color.Color

	colorTextBright    color.Color
	colorTextBody      color.Color
	colorTextSecondary color.Color
	colorTextMuted     color.Color
	colorTextDim       color.Color

	colorShadow color.Color

	// Status-glyph neutrals (idle/suspended) follow the text ramp.
	colorStatusMuted color.Color
	colorStatusDim   color.Color

	// Extended semantic roles (§A.1). OnBrand/OnGold are derived per theme from
	// kit.OnColor so they flip near-white (dark) ↔ near-black (light).
	colorOnBrand color.Color
	colorOnGold  color.Color

	colorBusy color.Color
)

// activeTheme is the name of the currently applied theme.
var activeTheme = themes[0].Name

func init() { applyTheme(themes[0]) }

// applyTheme swaps the active tokens to t and rebuilds the derived styles, so
// the next render of every screen adapts. Accents swap too (e.g. Daylight tones
// them down for contrast on a light terminal).
func applyTheme(t Theme) {
	activeTheme = t.Name

	colorCharple, colorHazy, colorDolly = t.Charple, t.Hazy, t.Dolly
	colorGold, colorGuac, colorCoral, colorMalibu, colorPeach = t.Gold, t.Guac, t.Coral, t.Malibu, t.Peach
	colorPage, colorSurface, colorRaised, colorRaised2 = t.Page, t.Surface, t.Raised, t.Raised2
	colorBorderSubtle, colorBorderMedium = t.BorderSubtle, t.BorderMedium
	colorTextBright, colorTextBody, colorTextSecondary, colorTextMuted, colorTextDim = t.TextBright, t.TextBody, t.TextSecondary, t.TextMuted, t.TextDim
	colorShadow = t.Shadow
	colorBusy = t.Busy

	colorStatusMuted = colorTextMuted
	colorStatusDim = colorTextDim

	colorOnBrand = kit.OnColor(t.Charple)
	colorOnGold = kit.OnColor(t.Gold)

	// Route raw shell/tool ANSI through the active theme palette (§A.2) so
	// program output (bright red, etc.) adopts our on-brand colors.
	kit.SetANSITable(ansiTableFor(t))

	// Wire the kit component palette to the active theme so KV, ErrorBlock,
	// Badge, Button, etc. follow the theme (D6).
	kit.SetComponentColors(kit.ComponentColors{
		KbdKey:     t.Charple,
		KbdLabel:   t.TextSecondary,
		KbdSep:     t.TextMuted,
		KVKey:      t.TextSecondary,
		KVVal:      t.TextBody,
		ErrDetail:  t.TextSecondary,
		ButtonBlur: t.TextSecondary,
		Roles: map[kit.Role]color.Color{
			kit.RoleBrand:   t.Charple,
			kit.RoleBusy:    t.Busy,
			kit.RoleWaiting: t.Gold,
			kit.RoleSuccess: t.Success,
			kit.RoleDenied:  t.Denied,
			kit.RoleError:   t.Coral,
			kit.RoleInfo:    t.Info,
			kit.RoleMuted:   t.TextMuted,
		},
	})

	rebuildStyles()
}

// toRGBA narrows a color.Color's 16-bit, alpha-premultiplied channels to the
// 8-bit color.RGBA the kit ANSI table expects.
func toRGBA(c color.Color) color.RGBA {
	r, g, b, a := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

// ansiTableFor derives the 16-entry ANSI remap palette (normal 0–7, bright 8–15)
// from a theme's semantic roles, so raw program SGR maps onto on-brand colors
// (§A.2). Blue/cyan reuse Info/Malibu; the brights track the same accents.
func ansiTableFor(t Theme) [16]color.RGBA {
	return [16]color.RGBA{
		toRGBA(t.Page),       // 0 black
		toRGBA(t.Coral),      // 1 red
		toRGBA(t.Guac),       // 2 green
		toRGBA(t.Gold),       // 3 yellow
		toRGBA(t.Info),       // 4 blue
		toRGBA(t.Dolly),      // 5 magenta
		toRGBA(t.Malibu),     // 6 cyan
		toRGBA(t.TextBody),   // 7 white
		toRGBA(t.TextMuted),  // 8 bright black
		toRGBA(t.Coral),      // 9 bright red
		toRGBA(t.Guac),       // 10 bright green
		toRGBA(t.Gold),       // 11 bright yellow
		toRGBA(t.Info),       // 12 bright blue
		toRGBA(t.Dolly),      // 13 bright magenta
		toRGBA(t.Malibu),     // 14 bright cyan
		toRGBA(t.TextBright), // 15 bright white
	}
}

// themeByName returns the registered theme with the given name
// (case-insensitive) and whether it was found.
func themeByName(name string) (Theme, bool) {
	for _, t := range themes {
		if strings.EqualFold(t.Name, name) {
			return t, true
		}
	}
	return Theme{}, false
}

// defaultThemeForBackground picks the default theme for a light/dark terminal,
// honoring a SANDBOX_THEME override (env now; a /theme command later).
func defaultThemeForBackground(isDark bool) Theme {
	if name := os.Getenv("SANDBOX_THEME"); name != "" {
		if t, ok := themeByName(name); ok {
			return t
		}
	}
	if isDark {
		return themes[0] // Midnight
	}
	return themes[1] // Daylight
}

// applyThemeForBackground sets the palette from terminal-background detection
// (the tea.BackgroundColorMsg path), respecting the SANDBOX_THEME override.
func applyThemeForBackground(isDark bool) { applyTheme(defaultThemeForBackground(isDark)) }

// cycleTheme applies the next theme in the registry (wrapping), for the
// /theme command.
func cycleTheme() {
	idx := 0
	for i, t := range themes {
		if t.Name == activeTheme {
			idx = i
			break
		}
	}
	applyTheme(themes[(idx+1)%len(themes)])
}
