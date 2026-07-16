package dashboard

import (
	"strings"
	"testing"
)

// thumbGlyph is the right-half-block the kit scrollbar draws for its thumb.
const thumbGlyph = "▐"

// TestTransientScrollbarHiddenAtBottom pins §2c (design D5): the scrollbar thumb
// is transient — while the body follows the bottom (the default), no thumb is
// drawn even though the content overflows. The blank gutter branch runs instead,
// so the peripheral glyph doesn't sit permanently bright in follow mode.
func TestTransientScrollbarHiddenAtBottom(t *testing.T) {
	m := buildBigTranscript(30) // seedSize(120,40) + GotoBottom → overflow, at bottom
	total, offset := m.body.Metrics()
	h := m.body.Height()
	if total <= h {
		t.Fatalf("fixture must overflow: total=%d height=%d", total, h)
	}
	if !m.body.AtBottom() {
		t.Fatalf("fixture must start at the bottom (offset=%d total=%d h=%d)", offset, total, h)
	}
	out := m.bodyView()
	if strings.Contains(out, thumbGlyph) {
		t.Errorf("at-bottom overflow drew a scrollbar thumb %q; want blank gutter", thumbGlyph)
	}
}

// TestTransientScrollbarShownWhenScrolledUp is the counterpart: scrolling up off
// the bottom reveals the thumb, so the reader can see their position in the
// scrollback. Same content as the at-bottom case, only the offset differs.
func TestTransientScrollbarShownWhenScrolledUp(t *testing.T) {
	m := buildBigTranscript(30)
	m.body.ScrollBy(-20) // lift off the bottom
	total, offset := m.body.Metrics()
	h := m.body.Height()
	if offset >= total-h {
		t.Fatalf("scroll did not lift off the bottom (offset=%d total=%d h=%d)", offset, total, h)
	}
	out := m.bodyView()
	if !strings.Contains(out, thumbGlyph) {
		t.Errorf("scrolled-up overflow drew no scrollbar thumb %q; want a visible thumb", thumbGlyph)
	}
}
