package kit

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// REGRESSION: all-nil gradient stops must not panic (lipgloss.Blend1D returns
// an empty ramp when every stop is nil, or the nil stops verbatim when there
// are at least as many stops as clusters) and must fall back to a plain render.
func TestGradientNilStopsNoPanic(t *testing.T) {
	base := lipgloss.NewStyle()
	// Two clusters, two nil stops: Blend1D returns the stops verbatim (nils).
	if got := GradientClusters(base, "hi", nil, nil); stripSGR(got) != "hi" {
		t.Fatalf("GradientClusters(nil stops) = %q, want plain %q", got, "hi")
	}
	// More clusters than stops: Blend1D drops the nils and yields an empty ramp.
	if got := GradientLine("hello world", nil, nil); got != "hello world" {
		t.Fatalf("GradientLine(nil stops) = %q, want unstyled input", got)
	}
}

// ORACLE: a single stop is not a gradient — both helpers fall back to the
// plain render, matching the documented "fewer than two stops" contract.
func TestGradientSingleStopFallsBack(t *testing.T) {
	red := color.RGBA{R: 0xff, A: 0xff}
	if got := GradientLine("abc", red); got != "abc" {
		t.Fatalf("GradientLine(single stop) = %q, want %q", got, "abc")
	}
	base := lipgloss.NewStyle().Bold(true)
	if got, want := GradientClusters(base, "abc", red), base.Render("abc"); got != want {
		t.Fatalf("GradientClusters(single stop) = %q, want %q", got, want)
	}
}

// REGRESSION: truncateCell must not count ANSI escape bytes as display columns
// or cut mid-escape when trimming styled input.
func TestTruncateCellANSIAware(t *testing.T) {
	styled := "\x1b[31mabcdef\x1b[0m"
	got := truncateCell(styled, 4)
	if w := lipgloss.Width(got); w != 4 {
		t.Fatalf("truncateCell(styled, 4) width = %d, want 4 (%q)", w, got)
	}
	if plain := stripSGR(got); plain != "abc…" {
		t.Fatalf("truncateCell(styled, 4) visible = %q, want %q", plain, "abc…")
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("truncateCell dropped or corrupted the leading escape: %q", got)
	}
}

// ORACLE: truncation is wide-rune/grapheme aware — a CJK or emoji cluster never
// splits, and the ellipsis stays within the column budget.
func TestTruncateCellWideRunes(t *testing.T) {
	if got := truncateCell("你好世界", 5); got != "你好…" {
		t.Fatalf("truncateCell(CJK, 5) = %q, want %q", got, "你好…")
	}
	if got := truncateCell("👍👍👍", 3); got != "👍…" {
		t.Fatalf("truncateCell(emoji, 3) = %q, want %q", got, "👍…")
	}
	if got := truncateCell("abc", 5); got != "abc" {
		t.Fatalf("truncateCell(no-op) = %q, want %q", got, "abc")
	}
}

// ORACLE: FormatTokens promotes to the next unit when one-decimal rounding
// would otherwise render "1000k".
func TestFormatTokensUnitBoundaries(t *testing.T) {
	cases := map[int]string{
		999:       "999",
		1000:      "1k",
		999_949:   "999.9k",
		999_950:   "1M",
		999_999:   "1M",
		1_000_000: "1M",
		1_049_999: "1M",
		1_050_000: "1.1M",
	}
	for in, want := range cases {
		if got := FormatTokens(in); got != want {
			t.Fatalf("FormatTokens(%d) = %q, want %q", in, got, want)
		}
	}
}
