// CANONICAL TEST — do not weaken.
package kit

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

const esc = "\x1b["

// luminance is an independent reference for "is this color light?" so the test
// does not depend on kit's own internals.
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 65535.0
}

// ORACLE: the on-color contrasts its background — light fg on a dark bg and dark
// fg on a light bg (the OnBrand/OnGold contrast rule). [S0]
func TestOnColorContrast(t *testing.T) {
	dark := color.RGBA{R: 10, G: 10, B: 20, A: 255}
	light := color.RGBA{R: 240, G: 240, B: 230, A: 255}

	if onDark := OnColor(dark); luminance(onDark) <= luminance(dark) {
		t.Fatalf("OnColor(dark) lum %.3f must exceed bg lum %.3f", luminance(onDark), luminance(dark))
	}
	if onLight := OnColor(light); luminance(onLight) >= luminance(light) {
		t.Fatalf("OnColor(light) lum %.3f must be below bg lum %.3f", luminance(onLight), luminance(light))
	}
}

// ORACLE: a basic-color SGR is rewritten; COUNTER: a truecolor sequence is left
// byte-for-byte untouched. [S0]
func TestRemapANSIMapsBase(t *testing.T) {
	in := esc + "31mred" + esc + "0m"
	got := RemapANSI(in)
	if got == in {
		t.Fatalf("RemapANSI left a basic SGR (31m) unchanged: %q", got)
	}
	if !strings.Contains(got, "red") {
		t.Fatalf("RemapANSI dropped the payload text: %q", got)
	}

	tc := esc + "38;2;10;20;30mtc" + esc + "0m"
	if RemapANSI(tc) != tc {
		t.Fatalf("RemapANSI must leave truecolor sequences untouched: %q", RemapANSI(tc))
	}
}

var gradStops = []color.Color{
	color.RGBA{R: 255, G: 0, B: 0, A: 255},
	color.RGBA{R: 0, G: 0, B: 255, A: 255},
}

// ORACLE: gradient styling never changes display width, across ascii, empty,
// single-grapheme and wide/emoji clusters; COUNTER: it actually colorizes
// (emits SGR), so it is not the identity. [S1]
func TestGradientLineWidthStable(t *testing.T) {
	cases := []string{"", "g", "hello world", "👍🏽", "a👍b"}
	for _, s := range cases {
		out := GradientLine(s, gradStops...)
		if got, want := lipgloss.Width(out), lipgloss.Width(s); got != want {
			t.Fatalf("GradientLine(%q) width %d, want %d (no width drift)", s, got, want)
		}
		if s != "" && !strings.Contains(out, esc) {
			t.Fatalf("GradientLine(%q) emitted no SGR; it must actually colorize", s)
		}
	}
}

// ORACLE: a titled rule fills exactly width w and keeps the title visible;
// COUNTER: below the minimum width it no-ops to the bare title rather than
// overflowing. [S1]
func TestTitledRuleFillsWidth(t *testing.T) {
	from := color.RGBA{R: 255, A: 255}
	to := color.RGBA{B: 255, A: 255}
	const w = 40
	out := TitledRule("Permission", w, from, to)
	if got := lipgloss.Width(out); got != w {
		t.Fatalf("TitledRule width %d, want %d", got, w)
	}
	if !strings.Contains(stripSGR(out), "Permission") {
		t.Fatalf("TitledRule dropped its title: %q", stripSGR(out))
	}
	if got := lipgloss.Width(TitledRule("Permission", 2, from, to)); got > len("Permission") {
		t.Fatalf("TitledRule under min width overflowed: width %d", got)
	}
}

// ORACLE: a section header fills width w and right-aligns its info so the info
// ends at the right edge. [S1]
func TestSectionHeaderFillsWidth(t *testing.T) {
	const w = 50
	out := SectionHeader("Details", w, "3 items")
	if got := lipgloss.Width(out); got != w {
		t.Fatalf("SectionHeader width %d, want %d", got, w)
	}
	plain := stripSGR(out)
	if !strings.Contains(plain, "Details") {
		t.Fatalf("SectionHeader dropped its title: %q", plain)
	}
	if !strings.HasSuffix(strings.TrimRight(plain, " "), "3 items") {
		t.Fatalf("SectionHeader info not right-aligned: %q", plain)
	}
}

// ORACLE: the scrollbar thumb sits proportionally and shrinks with the visible
// fraction; COUNTER: when the content fits the viewport there is no thumb. [S4]
func TestScrollbarThumb(t *testing.T) {
	const h = 10
	if bar := Scrollbar(h, 5, 10, 0); strings.TrimSpace(bar) != "" {
		t.Fatalf("content fits but scrollbar drew a thumb: %q", bar)
	}

	top := Scrollbar(h, 100, 10, 0)
	bottom := Scrollbar(h, 100, 10, 90)
	if top == "" || bottom == "" {
		t.Fatalf("overflowing content drew no scrollbar")
	}
	if lipgloss.Height(top) != h || lipgloss.Height(bottom) != h {
		t.Fatalf("scrollbar height: top=%d bottom=%d, want %d", lipgloss.Height(top), lipgloss.Height(bottom), h)
	}
	if top == bottom {
		t.Fatalf("thumb did not move between top and bottom offsets")
	}
	thumbRow := func(bar string) int {
		for i, line := range strings.Split(bar, "\n") {
			if strings.TrimSpace(stripSGR(line)) != "" {
				return i
			}
		}
		return -1
	}
	if thumbRow(top) >= thumbRow(bottom) {
		t.Fatalf("thumb at top (row %d) not above thumb at bottom (row %d)", thumbRow(top), thumbRow(bottom))
	}
}

// ORACLE: number formatting matches known outputs (k/M/B trimming, unit
// promotion at each boundary, comma cost). [S4]
func TestFormatTokensAndCost(t *testing.T) {
	tok := map[int]string{
		0: "0", 999: "999", 1000: "1k", 1200: "1.2k",
		999_949:   "999.9k", // just below the k→M promotion
		999_950:   "1M",     // one-decimal k rounds to 1000 → promotes to M
		1_000_000: "1M", 1_500_000: "1.5M", 12_300_000: "12.3M",
		999_949_999:   "999.9M", // just below the M→B promotion
		999_950_000:   "1B",     // one-decimal M rounds to 1000 → promotes to B
		1_000_000_000: "1B", 2_500_000_000: "2.5B", 15_000_000_000: "15B",
	}
	for in, want := range tok {
		if got := FormatTokens(in); got != want {
			t.Fatalf("FormatTokens(%d) = %q, want %q", in, got, want)
		}
	}
	cost := map[float64]string{0: "$0.00", 0.04: "$0.04", 1234.5: "$1,234.50"}
	for in, want := range cost {
		if got := FormatCost(in); got != want {
			t.Fatalf("FormatCost(%v) = %q, want %q", in, got, want)
		}
	}
}

// stripSGR removes CSI SGR sequences so width/content assertions see plain text.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
