package kit

// style.go — the kit's styling primitives: ANSI-16 remapping onto the active
// palette, perceptual per-grapheme gradients, titled/section rules, a stateless
// scrollbar, and token/cost formatting.

import (
	"image/color"
	"math"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
)

// SetANSITable overrides the palette RemapANSI maps basic SGR colors onto. It
// swaps the shared palette atomically (copy-modify-store), so it is safe to call
// while another goroutine is rendering. Defaults live in defaultPalette (the
// on-brand xterm-ish values); the dashboard theme swaps them so raw output
// adopts the active theme.
func SetANSITable(t [16]color.RGBA) {
	cur := *pal()
	cur.ansi = t
	activePalette.Store(&cur)
}

// OnColor returns a legible foreground to place on background bg — a near-white
// for dark backgrounds, a near-black for light ones, chosen by relative
// luminance. This is the mechanism behind the OnBrand/OnGold theme roles.
func OnColor(bg color.Color) color.Color {
	if relLuminance(bg) < 0.5 {
		return color.RGBA{R: 0xF5, G: 0xF5, B: 0xF5, A: 0xFF} // near-white on dark
	}
	return color.RGBA{R: 0x10, G: 0x10, B: 0x10, A: 0xFF} // near-black on light
}

// relLuminance is the Rec.709 relative luminance of c in [0,1], from its 16-bit
// channels (color.Color.RGBA returns alpha-premultiplied 0–65535 values).
func relLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 65535.0
}

// RemapANSI rewrites basic SGR color codes (30-37/40-47/90-97/100-107) in s to
// the active ANSI-16 table as truecolor, leaving extended 38/48 (256 and
// truecolor) sequences and all non-color codes byte-for-byte untouched.
func RemapANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		// Only CSI sequences terminated by 'm' (SGR) are candidates.
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' && !isCSIFinal(s[j]) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				b.WriteString(remapSGR(s[i+2 : j]))
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// isCSIFinal reports whether c terminates a CSI sequence (final byte 0x40–0x7e),
// so a non-SGR CSI is left intact rather than scanned past its end.
func isCSIFinal(c byte) bool { return c >= 0x40 && c <= 0x7e }

// remapSGR rewrites the parameter list of one SGR sequence, replacing basic
// color params with truecolor from the active ANSI table and copying everything
// else — including 38/48 extended runs — verbatim.
func remapSGR(params string) string {
	ansi := pal().ansi
	toks := strings.Split(params, ";")
	out := make([]string, 0, len(toks))
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t == "38" || t == "48" {
			// Extended color: copy "38;5;n" (1 sub-param) or "38;2;r;g;b" (3)
			// untouched so truecolor/256 is preserved exactly.
			out = append(out, t)
			if i+1 < len(toks) {
				switch toks[i+1] {
				case "5":
					out = appendUpTo(out, toks, &i, 2)
				case "2":
					out = appendUpTo(out, toks, &i, 4)
				default:
					out = append(out, toks[i+1])
					i++
				}
			}
			continue
		}
		if idx, isBg, ok := basicColorIndex(t); ok {
			out = append(out, trueColorParams(ansi[idx], isBg))
			continue
		}
		out = append(out, t)
	}
	return "\x1b[" + strings.Join(out, ";") + "m"
}

// appendUpTo copies the next n tokens after toks[*i] into out, advancing *i.
func appendUpTo(out, toks []string, i *int, n int) []string {
	for k := 0; k < n && *i+1 < len(toks); k++ {
		*i++
		out = append(out, toks[*i])
	}
	return out
}

// basicColorIndex maps a basic SGR color param to its ansiTable index and
// whether it sets the background. Non-color params return ok=false.
func basicColorIndex(tok string) (idx int, isBg, ok bool) {
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, false, false
	}
	switch {
	case n >= 30 && n <= 37:
		return n - 30, false, true
	case n >= 90 && n <= 97:
		return n - 90 + 8, false, true
	case n >= 40 && n <= 47:
		return n - 40, true, true
	case n >= 100 && n <= 107:
		return n - 100 + 8, true, true
	}
	return 0, false, false
}

// trueColorParams renders an SGR truecolor param run "38;2;r;g;b" (or 48 for bg).
func trueColorParams(c color.RGBA, isBg bool) string {
	lead := "38;2;"
	if isBg {
		lead = "48;2;"
	}
	return lead + strconv.Itoa(int(c.R)) + ";" + strconv.Itoa(int(c.G)) + ";" + strconv.Itoa(int(c.B))
}

// GradientClusters renders s under a perceptual gradient across stops, blended
// per grapheme cluster so emoji and wide/combined glyphs keep their full display
// width. Each cluster is rendered with base plus its ramp color, so a
// caller can carry bold/italic through the gradient. With fewer than two stops
// (nil stops don't count), or for empty input, it returns base.Render(s) (no
// gradient).
func GradientClusters(base lipgloss.Style, s string, stops ...color.Color) string {
	if s == "" || len(stops) < 2 {
		return base.Render(s)
	}
	clusters := graphemeClusters(s)
	if len(clusters) == 0 {
		return base.Render(s)
	}
	ramp := lipgloss.Blend1D(len(clusters), stops...)
	if len(ramp) == 0 {
		// Blend1D yields an empty ramp when every stop is nil.
		return base.Render(s)
	}
	var b strings.Builder
	for i, cl := range clusters {
		c := ramp[0]
		if i < len(ramp) {
			c = ramp[i]
		}
		if c == nil {
			// A short ramp (steps <= stops) is the stops slice verbatim and may
			// carry a caller's nil stop through; render those clusters plain.
			b.WriteString(base.Render(cl))
			continue
		}
		b.WriteString(base.Foreground(c).Render(cl))
	}
	return b.String()
}

// GradientLine renders s under a perceptual per-grapheme gradient across stops
// with no extra styling. With fewer than two stops, or for empty input,
// it returns the text unstyled.
func GradientLine(s string, stops ...color.Color) string {
	if s == "" || len(stops) < 2 {
		return s
	}
	return GradientClusters(lipgloss.NewStyle(), s, stops...)
}

// graphemeClusters splits s into user-perceived characters so styling never
// lands in the middle of a multi-rune cluster (skin-tone emoji, ZWJ sequences).
func graphemeClusters(s string) []string {
	var out []string
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		out = append(out, g.Str())
	}
	return out
}

// TitledRule renders title, a space, then a gradient-filled rule of `╱` out to
// width w. It no-ops to the bare title when w can't fit the title plus at
// least one rule glyph.
func TitledRule(title string, w int, from, to color.Color) string {
	tw := lipgloss.Width(title)
	fill := w - tw - 1 // 1 for the separating space
	if fill < 1 {
		return title
	}
	rule := GradientLine(strings.Repeat("╱", fill), from, to)
	return title + " " + rule
}

// SectionHeader renders title, a flat `─` rule, and optional right-aligned info,
// occupying exactly width w. The info ends flush against the right edge;
// the rule expands to absorb the slack between title and info. When w cannot fit
// the title plus a single rule glyph it no-ops to the bare title.
func SectionHeader(title string, w int, info ...string) string {
	inf := ""
	if len(info) > 0 {
		inf = info[0]
	}
	tw := lipgloss.Width(title)
	ruleColor := pal().rule
	rule := func(n int) string {
		return lipgloss.NewStyle().Foreground(ruleColor).Render(strings.Repeat("─", n))
	}
	if inf == "" {
		fill := w - tw - 1 // 1 for the separating space
		if fill < 1 {
			return title
		}
		return title + " " + rule(fill)
	}
	iw := lipgloss.Width(inf)
	fill := w - tw - iw - 2 // two separating spaces
	if fill < 1 {
		// No room for a rule: pad the gap so info still lands at the right edge.
		gap := w - tw - iw
		if gap < 1 {
			return title + " " + inf
		}
		return title + strings.Repeat(" ", gap) + inf
	}
	return title + " " + rule(fill) + " " + inf
}

// Scrollbar renders a stateless vertical scrollbar of the given height for a
// content of contentSize lines shown in a viewport of viewportSize lines at the
// given top offset. The thumb's size tracks the visible fraction (at least
// one row) and its position tracks the offset; the track is blank. It returns
// the empty string when the content fits the viewport (no thumb).
func Scrollbar(height, contentSize, viewportSize, offset int) string {
	if height <= 0 || viewportSize <= 0 || contentSize <= viewportSize {
		return ""
	}
	thumb := int(math.Round(float64(height) * float64(viewportSize) / float64(contentSize)))
	if thumb < 1 {
		thumb = 1
	}
	if thumb > height {
		thumb = height
	}
	maxOffset := contentSize - viewportSize
	travel := height - thumb
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	pos := 0
	if travel > 0 && maxOffset > 0 {
		pos = int(math.Round(float64(offset) / float64(maxOffset) * float64(travel)))
	}
	if pos > travel {
		pos = travel
	}
	thumbGlyph := lipgloss.NewStyle().Foreground(pal().thumb).Render("▐")
	rows := make([]string, height)
	for i := range rows {
		if i >= pos && i < pos+thumb {
			rows[i] = thumbGlyph
		} else {
			rows[i] = " "
		}
	}
	return strings.Join(rows, "\n")
}

// FormatTokens renders a token count with k/M/B units, trimming a trailing
// ".0": counts under 1000 are plain, thousands use "k", millions use "M", and
// billions use "B". At each boundary a value whose one-decimal rendering would
// round up to 1000 promotes to the next unit instead of showing "1000k" /
// "1000M" (e.g. n >= 999,950 → "M", n >= 999,950,000 → "B").
func FormatTokens(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	if k := roundTenth(float64(n) / 1000); k < 1000 {
		return trimDotZero(k) + "k"
	}
	if m := roundTenth(float64(n) / 1_000_000); m < 1000 {
		return trimDotZero(m) + "M"
	}
	return trimDotZero(roundTenth(float64(n)/1_000_000_000)) + "B"
}

// roundTenth rounds v to one decimal place — the precision trimDotZero prints —
// so unit promotion decisions match what would actually be rendered.
func roundTenth(v float64) float64 { return math.Round(v*10) / 10 }

// trimDotZero formats v with one decimal place, then drops a trailing ".0".
func trimDotZero(v float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0")
}

// FormatCost renders a USD cost with a "$" prefix, comma-grouped dollars, and
// two decimal places of cents.
func FormatCost(usd float64) string {
	neg := usd < 0
	if neg {
		usd = -usd
	}
	cents := int64(math.Round(usd * 100))
	dollars := groupThousands(cents / 100)
	rem := strconv.FormatInt(cents%100, 10)
	if len(rem) < 2 {
		rem = "0" + rem
	}
	out := "$" + dollars + "." + rem
	if neg {
		out = "-" + out
	}
	return out
}

// groupThousands renders n with comma thousands separators.
func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		b.WriteByte(',')
	}
	for i := pre; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}
