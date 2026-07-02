// Package dashboard is the Bubble Tea v2 command-center dashboard. It renders
// all active Kubernetes sandbox sessions in a List+Detail layout and supports
// sort, fuzzy filter, and live cluster-watch updates.
package dashboard

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// --- Derived styles (rebuilt by rebuildStyles on theme change) -----------
//
// These are app-specific styles derived from the shared theme palette. They are
// rebuilt whenever the theme swaps via the theme.OnChange hook registered below.

var (
	styleHeaderTally  lipgloss.Style
	styleHeaderSort   lipgloss.Style
	styleHeaderFilter lipgloss.Style
	styleRow          lipgloss.Style
	styleRowSelected  lipgloss.Style
	styleSelectionBar lipgloss.Style
	styleRelTime      lipgloss.Style
	styleDetailTitle  lipgloss.Style
	styleDivider      lipgloss.Style
	styleHelp         lipgloss.Style
	styleEmpty        lipgloss.Style

	// Transcript styles (declared in transcript.go usage; assigned here so
	// they pick up the active theme).
	styleTUser      lipgloss.Style
	styleTAssistant lipgloss.Style
	styleTTool      lipgloss.Style
	styleTError     lipgloss.Style
	styleTInfo      lipgloss.Style

	// Status-line styles. renderStatusLine and workingStatus run on every
	// keystroke plus each 150ms work-tick, so their fixed styles are memoized
	// here rather than rebuilt per frame.
	styleSLMuted  lipgloss.Style
	styleSLLabel  lipgloss.Style
	styleSLBody   lipgloss.Style
	styleSLBright lipgloss.Style
	styleSLBranch lipgloss.Style
	styleSLWarn   lipgloss.Style
	styleSLCost   lipgloss.Style
	styleSLBusy   lipgloss.Style
)

// init wires the app's style rebuild to the shared theme so a theme swap
// re-skins every dashboard surface. OnChange runs rebuildStyles once now, so the
// styles are populated from the active palette at startup.
func init() { theme.OnChange(rebuildStyles) }

func rebuildStyles() {
	styleHeaderTally = lipgloss.NewStyle().Foreground(theme.TextSecondary)
	styleHeaderSort = lipgloss.NewStyle().Foreground(theme.Peach)
	styleHeaderFilter = lipgloss.NewStyle().Foreground(theme.Malibu)
	styleRow = lipgloss.NewStyle().Foreground(theme.TextBody)
	styleRowSelected = lipgloss.NewStyle().Foreground(theme.TextBright).Background(theme.Raised)
	styleSelectionBar = lipgloss.NewStyle().Foreground(theme.OnBrand)
	styleRelTime = lipgloss.NewStyle().Foreground(theme.TextSecondary)
	styleDetailTitle = lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true)
	styleDivider = lipgloss.NewStyle().Foreground(theme.BorderMedium)
	styleHelp = lipgloss.NewStyle().
		Foreground(theme.TextSecondary).
		Background(theme.Raised).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.BorderMedium)
	styleEmpty = lipgloss.NewStyle().Foreground(theme.TextDim)

	styleTUser = lipgloss.NewStyle().Foreground(theme.Guac).Bold(true)
	styleTAssistant = lipgloss.NewStyle().Foreground(theme.TextBody)
	styleTTool = lipgloss.NewStyle().Foreground(theme.Malibu)
	styleTError = lipgloss.NewStyle().Foreground(theme.Coral)
	styleTInfo = lipgloss.NewStyle().Foreground(theme.TextMuted)

	styleSLMuted = lipgloss.NewStyle().Foreground(theme.TextMuted)
	styleSLLabel = lipgloss.NewStyle().Foreground(theme.TextSecondary)
	styleSLBody = lipgloss.NewStyle().Foreground(theme.TextBody)
	styleSLBright = lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true)
	styleSLBranch = lipgloss.NewStyle().Foreground(theme.Peach)
	styleSLWarn = lipgloss.NewStyle().Foreground(theme.Coral).Bold(true)
	styleSLCost = lipgloss.NewStyle().Foreground(theme.Guac)
	styleSLBusy = lipgloss.NewStyle().Foreground(theme.Busy)
}

// --- Per-status glyph styles (from the Status system in the handoff) -----
//
// glyphColor/glyphStyle map the app's SessionStatus onto theme tokens, so they
// stay in the app package; the glyph strings themselves live in the theme
// package (theme.GlyphBusy etc.).

// glyphColor returns the foreground color for a given SessionStatus.
func glyphColor(s SessionStatus) color.Color {
	switch s {
	case StatusBusy:
		return theme.Charple
	case StatusWaiting:
		return theme.Gold
	case StatusNeedsInput:
		return theme.Guac
	case StatusIdle:
		return theme.StatusMuted
	case StatusSuspended:
		return theme.StatusDim
	case StatusFailed:
		return theme.Coral
	default:
		return theme.TextMuted
	}
}

// glyphStyle returns the Lip Gloss foreground style for a given SessionStatus.
func glyphStyle(s SessionStatus) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(glyphColor(s))
}
