package dashboard

import (
	"strings"
	"testing"
)

// reasoning_render_test.go — §2c "Thinking: italic dim body, same shape streaming
// and completed". A committed multi-line think renders an italic muted body
// capped at reasoningCapLines wrapped lines with a "… +N lines (ctrl+o)" trailer
// (expandable via ctrl+o → toggleLatestExpandable); the live "∴ Thinking" tail
// shows the same shape, tail-following the newest text. These tests pin the
// committed render, the toggle walk, and the live cap.

// appendReasoning commits a blockReasoning carrying text and syncs the list, so
// the toggle walk sees it in m.blocks.
func appendReasoning(m *TranscriptModel, text string) *blockCard {
	b := m.newBlockCard(blockReasoning, text)
	m.blocks = append(m.blocks, b)
	m.syncItems()
	return b
}

// eightLineThink is a think whose eight short raw lines each fit within the wrap
// width, so its wrapped-line count is a stable 8 (> reasoningCapLines) regardless
// of the exact terminal width — 2 lines hidden when collapsed.
const eightLineThink = "step 1 here\nstep 2 here\nstep 3 here\nstep 4 here\n" +
	"step 5 here\nstep 6 here\nstep 7 here\nstep 8 here"

// TestReasoningCommittedCollapsedCaps: a >6-wrapped-line think renders exactly
// reasoningCapLines body lines plus a "… +N lines (ctrl+o)" trailer with N equal
// to the hidden count.
func TestReasoningCommittedCollapsedCaps(t *testing.T) {
	m := reasoningModel(t)
	b := appendReasoning(m, eightLineThink)

	got := stripANSICodes(m.renderBlockBody(b))
	if !strings.Contains(got, "… +2 lines (ctrl+o)") {
		t.Errorf("collapsed think missing the '… +2 lines (ctrl+o)' trailer:\n%s", got)
	}
	// The first six steps are shown; the last two are hidden behind the trailer.
	for _, want := range []string{"step 1 here", "step 6 here"} {
		if !strings.Contains(got, want) {
			t.Errorf("collapsed think should show %q:\n%s", want, got)
		}
	}
	for _, hidden := range []string{"step 7 here", "step 8 here"} {
		if strings.Contains(got, hidden) {
			t.Errorf("collapsed think leaked capped line %q:\n%s", hidden, got)
		}
	}
	// The body is exactly reasoningCapLines + label + trailer lines.
	if n := strings.Count(got, "\n") + 1; n != reasoningCapLines+2 {
		t.Errorf("collapsed think should render %d lines (label + %d body + trailer); got %d:\n%s",
			reasoningCapLines+2, reasoningCapLines, n, got)
	}
}

// TestReasoningCommittedExpanded: an expanded think shows its full body with no
// trailer.
func TestReasoningCommittedExpanded(t *testing.T) {
	m := reasoningModel(t)
	b := appendReasoning(m, eightLineThink)
	b.expanded = true

	got := stripANSICodes(m.renderBlockBody(b))
	if strings.Contains(got, "ctrl+o") || strings.Contains(got, "… +") {
		t.Errorf("expanded think must not show the cap trailer:\n%s", got)
	}
	for i := 1; i <= 8; i++ {
		if want := "step " + string(rune('0'+i)) + " here"; !strings.Contains(got, want) {
			t.Errorf("expanded think should show every line, missing %q:\n%s", want, got)
		}
	}
}

// TestReasoningCommittedSingleLineUnchanged pins the compact inline shape of a
// single-line think — the pre-§2c behavior that must not regress.
func TestReasoningCommittedSingleLineUnchanged(t *testing.T) {
	m := reasoningModel(t)
	b := appendReasoning(m, "just one thought")

	got := stripANSICodes(m.renderBlockBody(b))
	if got != "∴ Thought: just one thought" {
		t.Errorf("single-line think render changed:\n got %q\nwant %q", got, "∴ Thought: just one thought")
	}
}

// TestReasoningCommittedUnderCap: a multi-line think under the cap renders its
// full body (label on its own line, both lines shown) with NO trailer.
func TestReasoningCommittedUnderCap(t *testing.T) {
	m := reasoningModel(t)
	b := appendReasoning(m, "first line\nsecond line")

	got := stripANSICodes(m.renderBlockBody(b))
	if strings.Contains(got, "… +") || strings.Contains(got, "ctrl+o") {
		t.Errorf("under-cap think must not show a trailer:\n%s", got)
	}
	if !strings.HasPrefix(got, "∴ Thought\n") {
		t.Errorf("multi-line think should put the label on its own line:\n%s", got)
	}
	for _, want := range []string{"first line", "second line"} {
		if !strings.Contains(got, want) {
			t.Errorf("under-cap think should show %q:\n%s", want, got)
		}
	}
}

// TestToggleLatestExpandableReasoning covers the ctrl+o walk over thinking blocks:
// a capped block toggles, a more-recent tool card wins over an older think, and a
// think under the cap is skipped (nothing to reveal).
func TestToggleLatestExpandableReasoning(t *testing.T) {
	// (a) latest block is a capped reasoning block ⇒ it toggles.
	t.Run("capped reasoning toggles", func(t *testing.T) {
		m := reasoningModel(t)
		b := appendReasoning(m, eightLineThink)
		ver := b.Version()
		if !m.toggleLatestExpandable() {
			t.Fatal("toggleLatestExpandable returned false for a capped think")
		}
		if !b.expanded {
			t.Error("capped think did not become expanded")
		}
		if b.Version() == ver {
			t.Error("toggle did not bump the block version (list would not re-render)")
		}
	})

	// (b) a tool card more recent than the reasoning block ⇒ the tool card toggles,
	// preserving the existing behavior; the older think is left alone.
	t.Run("newer tool card wins", func(t *testing.T) {
		m := reasoningModel(t)
		think := appendReasoning(m, eightLineThink)
		m.startToolCard("Bash", "echo hi")
		m.finishToolCard(toolOK, "3 lines", "Bash", "one\ntwo\nthree", "")
		card := m.blocks[len(m.blocks)-1]

		if !m.toggleLatestExpandable() {
			t.Fatal("toggleLatestExpandable returned false with an expandable tool card present")
		}
		if !card.tool.expanded {
			t.Error("the more-recent tool card should have toggled")
		}
		if think.expanded {
			t.Error("the older think must be left untouched when a newer card wins")
		}
	})

	// (c) a reasoning block under the cap is skipped; with no other expandable
	// block the walk returns false.
	t.Run("under-cap reasoning skipped", func(t *testing.T) {
		m := reasoningModel(t)
		b := appendReasoning(m, "first line\nsecond line")
		if m.toggleLatestExpandable() {
			t.Error("an under-cap think must not be expandable")
		}
		if b.expanded {
			t.Error("under-cap think must not have been toggled")
		}
	})
}

// TestLiveReasoningCap: renderLiveReasoning tail-follows the newest text — with
// >6 wrapped lines it shows a "… +N earlier lines" marker plus the last
// reasoningCapLines lines; a body under the cap renders whole with no marker.
func TestLiveReasoningCap(t *testing.T) {
	m := reasoningModel(t)
	// Eight uniquely-identifiable short lines; the window keeps the last six.
	text := "L1 x\nL2 x\nL3 x\nL4 x\nL5 x\nL6 x\nL7 x\nL8 x"
	got := stripANSICodes(m.renderLiveReasoning(text))
	if !strings.Contains(got, "… +2 earlier lines") {
		t.Errorf("capped live think missing the '… +2 earlier lines' marker:\n%s", got)
	}
	for _, hidden := range []string{"L1 x", "L2 x"} {
		if strings.Contains(got, hidden) {
			t.Errorf("live tail leaked earlier line %q above the window:\n%s", hidden, got)
		}
	}
	for _, want := range []string{"L3 x", "L8 x"} {
		if !strings.Contains(got, want) {
			t.Errorf("live tail window should show %q:\n%s", want, got)
		}
	}

	// A body under the cap renders whole with no marker. Use a fresh model so the
	// E6 wrap cache from the capped render above can't carry over.
	m2 := reasoningModel(t)
	small := stripANSICodes(m2.renderLiveReasoning("a x\nb x\nc x"))
	if strings.Contains(small, "earlier lines") || strings.Contains(small, "… +") {
		t.Errorf("under-cap live think must not show a marker:\n%s", small)
	}
	for _, want := range []string{"a x", "b x", "c x"} {
		if !strings.Contains(small, want) {
			t.Errorf("under-cap live think should show %q:\n%s", want, small)
		}
	}
}
