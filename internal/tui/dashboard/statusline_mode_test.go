package dashboard

import (
	"strings"
	"testing"
)

// §2d yolo default: bypassPermissions is now the unpinned runner default, so the
// status line must make an active bypass session unmistakable. modeTag renders
// bypass as a distinct warning chip (⚠ + padding on an inverted coral band),
// while the safer modes stay quiet foreground-only tags.

// hasBackgroundSGR reports whether an ANSI-styled string carries a background
// color band. lipgloss renders the repo's hex theme colors as truecolor, so a
// background shows up as an SGR "48;" parameter; a foreground-only tag emits only
// "38;". Also treat the reverse-video attribute (\x1b[7m) as a band.
func hasBackgroundSGR(s string) bool {
	return strings.Contains(s, "48;") || strings.Contains(s, "\x1b[7m")
}

// The bypass mode surfaces as the ⚠ bypass chip in the full status line.
func TestStatusLineSurfacesBypassChip(t *testing.T) {
	m := newStatusModel(1000, 100)
	m.mode = modeBypass
	raw := m.renderStatusLine()
	out := stripANSI(raw)
	if !strings.Contains(out, "⚠ bypass") {
		t.Fatalf("bypass status line missing the ⚠ bypass warning chip: %q", out)
	}
	// The chip must be styled (a coral band), not plain text: the raw render
	// carries ANSI the stripped text drops.
	if raw == out {
		t.Fatalf("bypass chip is unstyled (no ANSI escapes): %q", raw)
	}
}

// The quieter modes render their own labels and never the bypass chip.
func TestStatusLineQuietModesAreNotBypass(t *testing.T) {
	for _, tc := range []struct {
		mode  permMode
		label string
	}{
		{modeDefault, "ask"},
		{modeAcceptEdits, "auto"},
		{modePlan, "plan"},
	} {
		m := newStatusModel(1000, 100)
		m.mode = tc.mode
		out := stripANSI(m.renderStatusLine())
		if !strings.Contains(out, tc.label) {
			t.Errorf("mode %v: status line missing %q label: %q", tc.mode, tc.label, out)
		}
		if strings.Contains(out, "bypass") {
			t.Errorf("mode %v: quiet mode must not render the bypass chip: %q", tc.mode, out)
		}
	}
}

// The bypass chip is inverted (dark text on a coral band): it carries a
// background SGR the quiet foreground-only tags never emit.
func TestBypassModeTagIsDistinctChip(t *testing.T) {
	bypass := modeBypass.modeTag()
	auto := modeAcceptEdits.modeTag()
	if bypass == stripANSI(bypass) {
		t.Skip("no ANSI styling in this environment; the chip's distinctness is color-based") // gate-ok: assertion is ANSI-color-based, meaningless without styling; chip text still covered by TestStatusLineSurfacesBypassChip
	}
	if !hasBackgroundSGR(bypass) {
		t.Errorf("bypass chip should carry a background band SGR: %q", bypass)
	}
	if hasBackgroundSGR(auto) {
		t.Errorf("quiet 'auto' tag should be foreground-only (no background band): %q", auto)
	}
}
