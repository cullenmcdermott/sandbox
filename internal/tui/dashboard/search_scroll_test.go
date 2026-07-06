package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// REGRESSION (D3): scrollToMatch must account for actual rendered heights
// (including wrapping) rather than counting raw newlines in unwrapped text.
func TestScrollToMatchUsesRenderedHeight(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "s1"}}, nil)
	m.width = 80
	m.height = 24

	// Add three blocks: first is 5 lines, second is 3 lines, third is 1 line.
	m.blocks = infoCards(m, "block0", "block1", "block2")
	m.syncItems()

	// Manually set up matches: target block 2.
	m.search = searchModel{
		open:       true,
		matchIndex: 0,
		matches:    [][2]int{{2, 0}},
	}

	m.scrollToMatch()

	// The offset should be at least the height of blocks 0+1.
	// With the default renderBlock for blockInfo, each block is 1 line
	// (styled text with no newlines), so offset should be 2.
	off := m.body.Offset()
	if off < 2 {
		t.Fatalf("scrollToMatch offset = %d, want >= 2 (should account for preceding blocks)", off)
	}
}

// scrollToMatch uses match[1] (rune offset) to estimate position within block.
func TestScrollToMatchUsesRuneOffset(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "s2"}}, nil)
	m.width = 80
	m.height = 24

	m.blocks = infoCards(m, strings.Repeat("a", 100)) // 1 line at width 80
	m.syncItems()

	// Match at rune offset 50 (halfway through the 100-char block).
	m.search = searchModel{
		open:       true,
		matchIndex: 0,
		matches:    [][2]int{{0, 50}},
	}

	before := m.body.Offset()
	m.scrollToMatch()
	after := m.body.Offset()

	// With a 100-char block at width 80, it's ~2 lines. Offset 50 should be
	// around line 1 (first line is 0). The exact value depends on lipgloss
	// width calculation; we just verify it's not 0 (block-granularity would be 0).
	if after == before {
		t.Fatal("scrollToMatch should have scrolled based on rune offset")
	}
}
