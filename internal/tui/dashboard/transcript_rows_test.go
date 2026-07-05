package dashboard

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The header and composer-hint rows must never overflow the width, so a long
// title or a long queued-prompt chip can't push the status glyph / send-esc
// affordance off the right edge (§1c spot 1). Both now route through spread.
func TestHeaderAndHintNeverOverflow(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 40, 24
	m.title = strings.Repeat("verylongtitle", 5)
	m.queuedPrompt = strings.Repeat("queuedtext", 10)
	m.layout()

	for _, l := range strings.Split(m.renderHeader(), "\n") {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("header line width %d > %d: %q", w, m.width, l)
		}
	}
	for i, l := range strings.Split(m.renderInput(), "\n") {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("renderInput line %d width %d > %d: %q", i, w, m.width, l)
		}
	}
}
