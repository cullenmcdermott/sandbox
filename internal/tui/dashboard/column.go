package dashboard

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// column.go — a small fixed+grow column layout engine for single-line table
// rows (the session-list primary line, §C2 in docs/tui-premium-plan.md). It is
// gh-dash's runtime column model (MIT) rebuilt from scratch as original code:
// fixed-width columns are laid out first, then the leftover width is divided
// evenly among the Grow columns. Every plain cell is truncated then padded to
// its resolved width with the ANSI-aware helpers (truncate/padRight/padLeft) so
// pre-styled content keeps its escape sequences intact and a row never over- or
// under-runs its target width.
//
// The engine deliberately mirrors gh-dash's Column{Width, Grow, Hidden, Align}
// shape; the YAML column config is intentionally skipped (§3 non-goals — our
// columns are curated in code).

// colAlign selects how a plain cell's content sits within its resolved width.
type colAlign int

const (
	alignLeft  colAlign = iota // pad on the right (default)
	alignRight                 // pad on the left
)

// column is one cell in a laid-out row.
type column struct {
	// content is the cell text. Plain columns are truncated/padded by the
	// engine; preStyled columns are emitted verbatim and MUST already measure
	// exactly width display cells (they carry ANSI the engine won't re-slice).
	content string
	// width is the fixed column width in display cells. Ignored when grow is set
	// (the width is then computed from the leftover space). preStyled columns
	// still declare their width here so it counts toward the fixed budget that
	// determines how much space the grow columns share.
	width int
	// grow, when set, makes the column absorb leftover width after every fixed
	// column is placed. The leftmost grow column takes any rounding remainder.
	grow bool
	// minWidth floors a grow column's computed width so it never renders
	// narrower than this even in a cramped row (matching the old titleW≥4 clamp).
	minWidth int
	// hidden columns contribute nothing to the row and consume no width.
	hidden bool
	// align controls padding for plain (non-preStyled) columns.
	align colAlign
	// preStyled marks content the engine emits verbatim.
	preStyled bool
}

// layoutColumns resolves grow widths against total and concatenates every
// visible column's cell into one line. Fixed (incl. preStyled) columns keep
// their declared width; the remaining space is split evenly across the grow
// columns (the leftmost grow column absorbs the division remainder), each grow
// column floored at its minWidth. Plain cells are truncated then padded per
// align; preStyled cells pass through untouched.
func layoutColumns(total int, cols []column) string {
	fixed := 0
	grows := 0
	for _, c := range cols {
		if c.hidden {
			continue
		}
		if c.grow {
			grows++
		} else {
			fixed += c.width
		}
	}

	leftover := total - fixed
	if leftover < 0 {
		leftover = 0
	}
	var each, extra int
	if grows > 0 {
		each = leftover / grows
		extra = leftover % grows // handed to the first grow column
	}

	var b strings.Builder
	for _, c := range cols {
		if c.hidden {
			continue
		}
		w := c.width
		if c.grow {
			w = each
			if extra > 0 {
				w += extra
				extra = 0
			}
			if w < c.minWidth {
				w = c.minWidth
			}
		}
		if c.preStyled {
			b.WriteString(c.content)
			continue
		}
		cell := truncate(c.content, w)
		switch c.align {
		case alignRight:
			cell = padLeft(cell, w)
		default:
			cell = padRight(cell, w)
		}
		b.WriteString(cell)
	}
	return b.String()
}

// padLeft pads s with spaces on the left to exactly width display columns (the
// right-align counterpart to padRight). ANSI-aware via lipgloss.Width.
func padLeft(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}
