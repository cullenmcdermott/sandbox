package chat

import "testing"

// Regression guards for L2/L3/L4 — the fence/thematic-break boundary fixes that
// previously had no dedicated test (the parity corpus only used same-char, len-3
// fences and "- " list items). Each case feeds the exact shape the fix targets.

// L2: a code fence may only be closed by the SAME fence character. A ``` block
// "closed" by ~~~ is still open (the ~~~ is literal content), so a stable-prefix
// boundary must not be taken there.
func TestFenceCrossCharDoesNotClose(t *testing.T) {
	if !isInsideOpenFence("```\nliteral ~~~ inside\n~~~") {
		t.Error("``` block closed by ~~~ — wrong-char fence must NOT close (L2)")
	}
	// Positive control: same-char closes.
	if isInsideOpenFence("```\ncode\n```") {
		t.Error("``` block closed by ``` should be closed")
	}
}

// L3: a closing fence must be at least as long as the opener. A len-4 opener
// "closed" by a len-3 fence is still open.
func TestFenceLengthMismatchDoesNotClose(t *testing.T) {
	if !isInsideOpenFence("````\nhas ``` inside\n```") {
		t.Error("len-4 opener closed by len-3 fence — must NOT close (L3)")
	}
	// Positive control: equal/greater length closes.
	if isInsideOpenFence("````\ncode\n````") {
		t.Error("len-4 opener closed by len-4 fence should be closed")
	}
}

// L4: a thematic break (`* * *`, `- - -`, `___`) is not a list-item marker.
func TestThematicBreakIsNotListMarker(t *testing.T) {
	for _, tb := range []string{"* * *", "- - -", "___", "***", "---"} {
		if !isThematicBreak(tb) {
			t.Errorf("isThematicBreak(%q) = false, want true", tb)
		}
		if isListItemMarker(tb) {
			t.Errorf("isListItemMarker(%q) = true, want false (thematic break, not a list) (L4)", tb)
		}
	}
	// Positive control: a real list item is still a marker and not a break.
	for _, li := range []string{"* item", "- item", "+ item", "1. item"} {
		if !isListItemMarker(li) {
			t.Errorf("isListItemMarker(%q) = false, want true", li)
		}
	}
}
