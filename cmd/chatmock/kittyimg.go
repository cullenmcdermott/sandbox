package main

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// Image demo for requirement #4 (real inline images via the Kitty protocol).
// This reuses the SAME production encoder the ctx-gauge uses
// (internal/terminal/kitty.go: KittyTransmitRGBA + KittyPlaceholders, the
// AltScreen-safe Unicode-placeholder variant) — proving real images are reuse,
// not greenfield. The only new bit a production version needs is decoding a
// real PNG to RGBA; here we synthesize a theme-colored banner so the demo is
// self-contained.

const (
	imgCols = 46
	imgRows = 11
	imgPixW = 368
	imgPixH = 176
)

func (m *model) renderImageBlock(ch chrome) []string {
	caption := lipgloss.NewStyle().Foreground(theme.TextMuted).Italic(true).
		Render("◷ architecture.png — real pixels via Kitty graphics")

	if !m.caps.KittyGraphics {
		note := lipgloss.NewStyle().Foreground(theme.TextDim).
			Render("[ inline image needs a Kitty-graphics terminal like Ghostty ]")
		return placeMulti([]string{caption, note}, ch)
	}

	// (Re)transmit only when the palette changed — same one-shot discipline as
	// the production gauge: the heavy APC rides one frame, placeholders ride all.
	if m.imgKey != theme.Active() || m.imgID == 0 {
		m.imgKey = theme.Active()
		m.imgID++
		if m.imgID == 0 || m.imgID >= (1<<24) {
			m.imgID = 1
		}
		m.pendingImg = terminal.KittyTransmitRGBA(m.imgID, imgCols, imgRows, imgPixW, imgPixH, synthImage(imgPixW, imgPixH))
	}

	rows := strings.Split(placeholdersWide(m.imgID, imgCols, imgRows), "\n")
	return placeMulti(append([]string{caption}, rows...), ch)
}

// placeholdersWide is a width-safe replacement for terminal.KittyPlaceholders.
// The production helper writes a COLUMN diacritic on every cell and clamps the
// index into a 32-entry table — fine for the 10-col ctx gauge, but it garbles
// any placement wider than 32 cells (columns 32+ collapse onto index 31). Here
// we put the column diacritic only on the first cell of each row and let the
// terminal AUTO-INCREMENT the column for the rest, so only the ROW count is
// bounded by the table (32 is ample). This is the encoding a real image needs;
// the production fix is to do the same (or extend the diacritic table).
func placeholdersWide(id uint32, cols, rows int) string {
	if id == 0 || cols < 1 || rows < 1 {
		return ""
	}
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", byte(id>>16), byte(id>>8), byte(id))
	const (
		reset       = "\x1b[39m"
		placeholder = "\U0010EEEE"
	)
	var b strings.Builder
	for row := 0; row < rows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(fg)
		// First cell pins (row, col=0); later cells carry only the row diacritic,
		// so the terminal advances the column itself — no per-column table lookup.
		b.WriteString(placeholder)
		b.WriteRune(rowDiacritic(row))
		b.WriteRune(rowDiacritic(0))
		for col := 1; col < cols; col++ {
			b.WriteString(placeholder)
			b.WriteRune(rowDiacritic(row))
		}
		b.WriteString(reset)
	}
	return b.String()
}

// rowDiacritic returns the combining mark Kitty uses to encode index n (the
// prefix of the official rowcolumn-diacritics table; 32 entries cover any
// sensible row count). Mirrors internal/terminal/kitty.go.
func rowDiacritic(n int) rune {
	table := []rune{
		0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F,
		0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357,
		0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
		0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484,
	}
	if n < 0 {
		n = 0
	}
	if n >= len(table) {
		n = len(table) - 1
	}
	return table[n]
}

// synthImage rasterizes a theme-colored banner: a vertical Surface→Page wash, a
// Charple→Dolly brand bar, three accent swatches, and a TextBright sine wave —
// enough to read as a genuine image rather than cell art.
func synthImage(w, h int) []byte {
	out := make([]byte, w*h*4)
	set := func(x, y int, c terminal.RGB) {
		if x < 0 || y < 0 || x >= w || y >= h {
			return
		}
		i := (y*w + x) * 4
		out[i], out[i+1], out[i+2], out[i+3] = c.R, c.G, c.B, 0xff
	}

	top, bot := rgbOf(theme.Surface), rgbOf(theme.Page)
	for y := 0; y < h; y++ {
		row := lerpRGB(top, bot, float64(y)/float64(h-1))
		for x := 0; x < w; x++ {
			set(x, y, row)
		}
	}

	charp, dolly := rgbOf(theme.Charple), rgbOf(theme.Dolly)
	for y := int(0.12 * float64(h)); y < int(0.30*float64(h)); y++ {
		for x := 0; x < w; x++ {
			set(x, y, lerpRGB(charp, dolly, float64(x)/float64(w-1)))
		}
	}

	swatches := []terminal.RGB{rgbOf(theme.Guac), rgbOf(theme.Gold), rgbOf(theme.Malibu)}
	sw := w / 9
	gap := sw / 2
	sy := int(0.44 * float64(h))
	for i, c := range swatches {
		x0 := int(0.10*float64(w)) + i*(sw+gap)
		for y := sy; y < sy+sw; y++ {
			for x := x0; x < x0+sw; x++ {
				set(x, y, c)
			}
		}
	}

	bright := rgbOf(theme.TextBright)
	for x := 0; x < w; x++ {
		yy := int(0.82*float64(h) + 0.10*float64(h)*math.Sin(float64(x)/float64(w)*4*math.Pi))
		for d := -1; d <= 1; d++ {
			set(x, yy+d, bright)
		}
	}
	return out
}

func rgbOf(c color.Color) terminal.RGB {
	r, g, b, _ := c.RGBA()
	return terminal.RGB{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8)}
}

func lerpRGB(a, b terminal.RGB, t float64) terminal.RGB {
	lerp := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return terminal.RGB{R: lerp(a.R, b.R), G: lerp(a.G, b.G), B: lerp(a.B, b.B)}
}
