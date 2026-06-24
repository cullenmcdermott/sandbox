package main

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// gaugeCols is the width, in cells, of the context-usage bar in the header.
const gaugeCols = 14

// updateGauge recomputes the context-usage fraction the header bar reads.
func (m *model) updateGauge() {
	frac := float64(m.tokens) / float64(ctxLimit)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	m.gaugeFrac = frac
}

// gaugeView renders the context bar as a always-visible block bar: a colored
// fill over a dim track. (The Kitty graphics protocol is showcased by the cat
// popup instead — a 1-row anti-aliased image bar reads as invisible at low fill.)
func (m *model) gaugeView() string {
	filled := int(m.gaugeFrac*float64(gaugeCols) + 0.5)
	if filled > gaugeCols {
		filled = gaugeCols
	}
	fill := lipgloss.NewStyle().Foreground(gaugeFill(m.gaugeFrac)).Render(strings.Repeat("█", filled))
	track := lipgloss.NewStyle().Foreground(theme.BorderMedium).Render(strings.Repeat("░", gaugeCols-filled))
	return fill + track
}

// gaugeFill picks the gauge color by how full it is: calm green → gold → coral.
func gaugeFill(frac float64) color.Color {
	switch {
	case frac >= 0.85:
		return theme.Coral
	case frac >= 0.6:
		return theme.Gold
	default:
		return theme.Guac
	}
}

// ctxReadout renders the "ctx <bar> NN%" header segment.
func (m *model) ctxReadout() string {
	pct := int(m.gaugeFrac*100 + 0.5)
	label := lipgloss.NewStyle().Foreground(theme.TextMuted).Render("ctx ")
	num := lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(
		fmt.Sprintf(" %d%% · %s", pct, kit.FormatTokens(m.tokens)))
	return label + m.gaugeView() + num
}

// --- the on-demand cat image (Kitty graphics showcase) --------------------

// catGrid is 16×16 pixel-art of a cat face. Each rune maps to a color (or
// transparent), via catCellColor. It is rendered two ways: as a real Kitty
// graphics image (RGBA → APC transmission → placeholder cells) on a capable
// terminal, or as colored block characters everywhere else.
var catGrid = []string{
	".FFF........FFF.",
	".FFFF......FFFF.",
	".FFFFFFFFFFFFFF.",
	"FFFFFFFFFFFFFFFF",
	"FFFFFFFFFFFFFFFF",
	"FFEEFFFFFFFFEEFF",
	"FFEEFFFFFFFFEEFF",
	"FFFFFFFFFFFFFFFF",
	"FFFFFFFPPFFFFFFF",
	"FWWFFFFPPFFFFWWF",
	"FFFFFDDDDDDFFFFF",
	"FFFFFFFFFFFFFFFF",
	".FFFFFFFFFFFFFF.",
	".FFFFFFFFFFFFFF.",
	"..FFFFFFFFFFFF..",
	"...FFFFFFFFFF...",
}

func catCellColor(r rune) (color.Color, bool) {
	switch r {
	case 'F':
		return theme.Peach, true // fur
	case 'D':
		return theme.Coral, true // mouth
	case 'E':
		return theme.Guac, true // eyes
	case 'P':
		return theme.Dolly, true // nose
	case 'W':
		return theme.TextBright, true // whiskers
	default:
		return nil, false // transparent
	}
}

const (
	catID   uint32 = 0x00CA75
	catCols        = 32 // placement width in cells (cells are ~2:1, so this …
	catRows        = 16 // … with this height shows the square photo ~square
)

// showKittyImage opens the cat popup and returns the command that transmits the
// image data to the terminal, or nil on a terminal without Kitty graphics (the
// popup falls back to block art).
//
// The transmission MUST go through tea.Raw: Bubble Tea v2 renders via the
// ultraviolet cell buffer, whose parser only understands SGR + OSC-8 and silently
// drops APC (\x1b_G…) graphics sequences embedded in View content. tea.Raw writes
// the bytes straight to the terminal, bypassing the cell renderer. The placement
// — the U+10EEEE placeholder cells — stays in the rendered View (those are real
// text cells and survive), referencing the transmitted image by id.
func (m *model) showKittyImage() tea.Cmd {
	m.showKitty = true
	if !m.caps.KittyGraphics {
		return nil
	}
	if m.catXmit == "" {
		m.catXmit = catPhotoTransmission() // real cat photo → RGBA → APC _G
	}
	if m.catXmit == "" {
		return nil // decode failed; popup shows block art
	}
	return tea.Raw(m.catXmit)
}

// catBlockArt renders catGrid as colored block characters (two cells per pixel
// so it isn't squished) — the fallback when the terminal has no Kitty graphics.
func catBlockArt() string {
	var b strings.Builder
	for i, row := range catGrid {
		if i > 0 {
			b.WriteByte('\n')
		}
		for _, r := range row {
			if c, ok := catCellColor(r); ok {
				b.WriteString(lipgloss.NewStyle().Foreground(c).Render("██"))
			} else {
				b.WriteString("  ")
			}
		}
	}
	return b.String()
}

// kittyPopupBox renders the centered cat-image card.
func (m *model) kittyPopupBox() string {
	var art, note string
	if m.caps.KittyGraphics {
		art = terminal.KittyPlaceholders(catID, catCols, catRows)
		note = "a real cat photo (RGBA over the Kitty APC _G protocol)"
	} else {
		art = catBlockArt()
		note = "block-art fallback — terminal has no Kitty graphics"
	}
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("meow")
	caption := lipgloss.NewStyle().Foreground(theme.TextMuted).Render(note)
	body := art + "\n\n" + caption + "\n" + kit.KbdRow([2]string{"any key", "close"})

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Charple).
		Background(theme.Surface).
		Padding(1, 2).
		Render(title + "\n" + body)
}

// capsBox renders the terminal-capability panel (toggled with ctrl+g): exactly
// what tui/terminal.Detect() found, plus which effects are consequently live.
func (m *model) capsBox() string {
	const w = 52
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("terminal capabilities")

	yes := lipgloss.NewStyle().Foreground(theme.Guac).Render("yes")
	no := lipgloss.NewStyle().Foreground(theme.TextDim).Render("no")
	yn := func(b bool) string {
		if b {
			return yes
		}
		return no
	}
	ver := m.caps.GhosttyVersion
	if ver == "" {
		ver = "—"
	}

	lines := []string{
		kit.TitledRule(title, w-2, theme.Charple, theme.Dolly),
		kit.KV("ghostty", yn(m.caps.IsGhostty), 16),
		kit.KV("  version", lipgloss.NewStyle().Foreground(theme.TextBody).Render(ver), 16),
		kit.KV("kitty graphics", yn(m.caps.KittyGraphics), 16),
		kit.KV("truecolor", yn(m.caps.TrueColor), 16),
		kit.KV("reduce-motion", yn(m.caps.ReduceMotion), 16),
		"",
		lipgloss.NewStyle().Foreground(theme.TextMuted).Render(m.effectsLine()),
		"",
		kit.KbdRow([2]string{"any key", "close"}),
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Charple).
		Background(theme.Surface).
		Width(w).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m *model) effectsLine() string {
	if m.caps.KittyGraphics {
		return "→ ask \"show me a kitty image\" for a real inline Kitty image."
	}
	return "→ no Kitty graphics: the cat popup falls back to block art."
}
