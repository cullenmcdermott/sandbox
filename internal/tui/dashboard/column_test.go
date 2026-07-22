package dashboard

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// TestLayoutColumnsFixedGrow: fixed columns keep their width, the grow column
// fills the leftover, and the assembled line is exactly `total` wide.
func TestLayoutColumnsFixedGrow(t *testing.T) {
	got := layoutColumns(20, []column{
		{content: "abc", width: 3, preStyled: true},
		{content: "title", grow: true, minWidth: 1, align: alignLeft},
		{content: "XX", width: 2, preStyled: true},
	})
	// grow width = 20 - 3 - 2 = 15; "title" padded to 15.
	want := "abc" + "title" + "          " + "XX"
	if got != want {
		t.Fatalf("layout = %q, want %q", got, want)
	}
	if w := lipgloss.Width(got); w != 20 {
		t.Fatalf("width = %d, want 20", w)
	}
}

// TestLayoutColumnsGrowMinFloor: a grow column never renders narrower than its
// minWidth even when the leftover is smaller (mirrors the old titleW≥4 clamp).
func TestLayoutColumnsGrowMinFloor(t *testing.T) {
	got := layoutColumns(6, []column{
		{content: "AAAAA", width: 5, preStyled: true},
		{content: "hello", grow: true, minWidth: 4, align: alignLeft},
	})
	// leftover = 6-5 = 1, floored to minWidth 4 → "hello" truncated to 4 cols
	// ("hel…", ellipsis tail from the shared ANSI-aware truncate helper).
	want := "AAAAA" + "hel…"
	if got != want {
		t.Fatalf("layout = %q, want %q", got, want)
	}
}

// TestLayoutColumnsHidden: a hidden column contributes nothing and frees its
// width to the grow column.
func TestLayoutColumnsHidden(t *testing.T) {
	got := layoutColumns(10, []column{
		{content: "SKIP", width: 4, hidden: true},
		{content: "x", grow: true, minWidth: 1, align: alignLeft},
	})
	// hidden column drops out entirely → grow gets the full 10.
	want := "x         "
	if got != want {
		t.Fatalf("layout = %q, want %q", got, want)
	}
	if w := lipgloss.Width(got); w != 10 {
		t.Fatalf("width = %d, want 10", w)
	}
}

// TestLayoutColumnsAlignRight: a right-aligned plain column pads on the left.
func TestLayoutColumnsAlignRight(t *testing.T) {
	got := layoutColumns(10, []column{
		{content: "9s", width: 10, align: alignRight},
	})
	want := "        9s"
	if got != want {
		t.Fatalf("layout = %q, want %q", got, want)
	}
}

// TestLayoutColumnsPreStyledPassThrough: a pre-styled (ANSI) cell is emitted
// verbatim, and its declared width still counts toward the fixed budget so the
// grow column is sized correctly around it.
func TestLayoutColumnsPreStyledPassThrough(t *testing.T) {
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Render("ab") // 2 display cells
	got := layoutColumns(8, []column{
		{content: styled, width: 2, preStyled: true},
		{content: "g", grow: true, minWidth: 1, align: alignLeft},
	})
	// The ANSI is preserved untouched, and the grow column fills 8-2 = 6.
	want := styled + "g     "
	if got != want {
		t.Fatalf("layout = %q, want %q", got, want)
	}
	if w := lipgloss.Width(got); w != 8 {
		t.Fatalf("visible width = %d, want 8", w)
	}
}

// TestLayoutColumnsTwoGrowRemainder: two grow columns split the leftover; the
// leftmost absorbs the rounding remainder.
func TestLayoutColumnsTwoGrowRemainder(t *testing.T) {
	got := layoutColumns(11, []column{
		{content: "a", grow: true, minWidth: 1, align: alignLeft},
		{content: "b", grow: true, minWidth: 1, align: alignLeft},
	})
	// leftover 11 / 2 = 5 each, remainder 1 → first grow gets 6, second gets 5.
	want := "a     " + "b    "
	if got != want {
		t.Fatalf("layout = %q, want %q", got, want)
	}
	if w := lipgloss.Width(got); w != 11 {
		t.Fatalf("width = %d, want 11", w)
	}
}
