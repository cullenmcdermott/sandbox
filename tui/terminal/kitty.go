package terminal

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Kitty graphics protocol, Unicode-placeholder ("virtual placement") variant —
// the only form safe to use inside a Bubble Tea AltScreen, because the rendered
// placeholder cells are normal-width and the diff renderer measures them
// correctly (docs/ghostty-terminal-effects.md §4).
//
// Two pieces:
//
//   - Transmission: an APC _G control string carrying the RGBA image plus a
//     virtual placement (U=1). This is genuinely out-of-band; emit it ONLY when
//     the image changes (it is large), prepended to the frame on the changing
//     frame — never every frame.
//   - Placement: a rectangle of U+10EEEE placeholder cells (row/column encoded
//     via combining diacritics, image id encoded in the foreground color). These
//     ride the frame string like any other text and are width-correct.
//
// Everything here returns plain strings and performs no I/O.

// placeholder is the Kitty Unicode placeholder base code point. The terminal
// replaces a run of these (carrying row/col diacritics + an id-bearing fg color)
// with the corresponding slice of the transmitted image.
const placeholder = "\U0010EEEE"

// apcStart / apcEnd frame a Kitty graphics command (APC … ST).
const (
	apcStart = "\x1b_G"
	apcEnd   = "\x1b\\"
)

// rowColumnDiacritics is the prefix of Kitty's official rowcolumn-diacritics
// table: the combining marks that encode a placeholder cell's row or column
// index (index 0 → first entry). 32 entries cover a 32×32 placement, ample for
// a statusline gauge. Mirrors the table shipped with kitty.
var rowColumnDiacritics = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F,
	0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357,
	0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
	0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484,
}

// diacritic returns the combining mark for index n, clamped into the available
// table (callers keep gauges small, so clamping only guards against misuse).
func diacritic(n int) rune {
	if n < 0 {
		n = 0
	}
	if n >= len(rowColumnDiacritics) {
		n = len(rowColumnDiacritics) - 1
	}
	return rowColumnDiacritics[n]
}

// kittyChunkSize is the max base64 payload per APC chunk (the protocol limit).
const kittyChunkSize = 4096

// RGB is an 8-bit-per-channel color, used to parameterize GaugeRGBA without the
// terminal package depending on the dashboard's theme.
type RGB struct{ R, G, B uint8 }

// GaugeRGBA builds a pixW×pixH RGBA bitmap for a horizontal fill gauge at the
// given fraction (clamped to [0,1]): columns left of the fill boundary take
// fill, the rest take empty, and the single boundary column is alpha-blended by
// its sub-pixel coverage so the edge is anti-aliased (the "real pixels" payoff).
// Output is row-major width*height*4 bytes, fully opaque. Returns nil for a
// degenerate size.
func GaugeRGBA(frac float64, pixW, pixH int, fill, empty RGB) []byte {
	if pixW < 1 || pixH < 1 {
		return nil
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	boundary := frac * float64(pixW)
	out := make([]byte, pixW*pixH*4)
	for x := 0; x < pixW; x++ {
		// coverage is how much of column x is "filled" in [0,1].
		cov := boundary - float64(x)
		if cov < 0 {
			cov = 0
		}
		if cov > 1 {
			cov = 1
		}
		r := uint8(float64(fill.R)*cov + float64(empty.R)*(1-cov) + 0.5)
		g := uint8(float64(fill.G)*cov + float64(empty.G)*(1-cov) + 0.5)
		bl := uint8(float64(fill.B)*cov + float64(empty.B)*(1-cov) + 0.5)
		for y := 0; y < pixH; y++ {
			i := (y*pixW + x) * 4
			out[i] = r
			out[i+1] = g
			out[i+2] = bl
			out[i+3] = 0xff
		}
	}
	return out
}

// KittyTransmitRGBA builds the APC _G transmission for an RGBA image bound to a
// virtual placement spanning cols×rows terminal cells. id identifies the image
// (kept ≤ 24 bits so the placement foreground color alone selects it). pixW/pixH
// are the source pixel dimensions; rgba is width*height*4 bytes (8-bit RGBA).
// The payload is base64-encoded and chunked per the protocol. q=2 suppresses the
// terminal's acknowledgement so no stray response corrupts the TUI input stream.
//
// Returns "" if id is 0 (reserved / "no image") or rgba is empty.
func KittyTransmitRGBA(id uint32, cols, rows, pixW, pixH int, rgba []byte) string {
	if id == 0 || len(rgba) == 0 {
		return ""
	}
	enc := base64.StdEncoding.EncodeToString(rgba)
	var b strings.Builder
	for i := 0; i < len(enc); i += kittyChunkSize {
		end := i + kittyChunkSize
		if end > len(enc) {
			end = len(enc)
		}
		more := 0
		if end < len(enc) {
			more = 1
		}
		b.WriteString(apcStart)
		if i == 0 {
			// First chunk carries the full control key set.
			fmt.Fprintf(&b, "q=2,a=T,U=1,i=%d,f=32,s=%d,v=%d,c=%d,r=%d,m=%d",
				id, pixW, pixH, cols, rows, more)
		} else {
			// Continuation chunks carry only the more-data flag.
			fmt.Fprintf(&b, "m=%d", more)
		}
		b.WriteString(";")
		b.WriteString(enc[i:end])
		b.WriteString(apcEnd)
	}
	return b.String()
}

// KittyPlaceholders renders the cols×rows rectangle of placeholder cells that
// displays image id. Each row is a run of U+10EEEE cells; the image id is
// carried in a 24-bit foreground color (R=id>>16, G=id>>8, B=id). Rows are
// separated by '\n'. Each cell measures width 1 (the diacritics are zero-width),
// so a cols-wide row occupies exactly cols columns — the same width as the block
// bar it replaces.
//
// Column encoding: only the FIRST cell of each row carries an explicit column
// diacritic (col 0); the rest carry just the row diacritic and let the terminal
// AUTO-INCREMENT the column. This keeps only the ROW count bounded by the
// 32-entry diacritic table — a placement wider than 32 cells (e.g. a real inline
// image) renders correctly instead of collapsing columns 32+ onto the clamped
// last table index (the bug the per-cell column diacritic had).
//
// Returns "" for a degenerate rectangle or id 0.
func KittyPlaceholders(id uint32, cols, rows int) string {
	if id == 0 || cols < 1 || rows < 1 {
		return ""
	}
	r := byte(id >> 16)
	g := byte(id >> 8)
	bl := byte(id)
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, bl)
	const reset = "\x1b[39m"

	var b strings.Builder
	for row := 0; row < rows; row++ {
		if row > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fg)
		// First cell pins (row, col=0); subsequent cells carry only the row
		// diacritic, so the terminal advances the column itself — no per-column
		// table lookup, so wide placements never clamp.
		b.WriteString(placeholder)
		b.WriteRune(diacritic(row))
		b.WriteRune(diacritic(0))
		for col := 1; col < cols; col++ {
			b.WriteString(placeholder)
			b.WriteRune(diacritic(row))
		}
		b.WriteString(reset)
	}
	return b.String()
}
