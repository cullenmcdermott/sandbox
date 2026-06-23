// CANONICAL TEST — do not weaken.
package kit

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// ORACLE: a key hint renders in the canonical "[key] label" shape so every call
// site agrees; COUNTER: it is not the empty placeholder. [D0]
func TestKbdFormat(t *testing.T) {
	if out := stripSGR(Kbd("a", "approve")); out != "[a] approve" {
		t.Fatalf("Kbd(a,approve) = %q, want %q", out, "[a] approve")
	}
}

// ORACLE: a key-hint row preserves every key+label; COUNTER: it joins N hints
// with exactly N-1 shared "·" separators (one consistent separator, not a
// per-call-site reinvention). [D0]
func TestKbdRowSeparators(t *testing.T) {
	row := stripSGR(KbdRow([2]string{"a", "approve"}, [2]string{"d", "deny"}, [2]string{"q", "close"}))
	for _, want := range []string{"[a] approve", "[d] deny", "[q] close"} {
		if !strings.Contains(row, want) {
			t.Fatalf("KbdRow dropped %q: %q", want, row)
		}
	}
	if got := strings.Count(row, "·"); got != 2 {
		t.Fatalf("KbdRow used %d separators for 3 hints, want 2: %q", got, row)
	}
}

// ORACLE: a card fills its Width on every line and shows its title and body;
// COUNTER: it actually frames (more than one line). [D2]
func TestCardFillsWidth(t *testing.T) {
	const w = 40
	out := Card(CardOpts{Title: "Tool", Body: "running npm test", Width: w, Accent: RoleInfo})
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("Card did not frame (got %d line(s)): %q", len(lines), out)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != w {
			t.Fatalf("Card line %d width %d, want %d", i, got, w)
		}
	}
	plain := stripSGR(out)
	if !strings.Contains(plain, "Tool") || !strings.Contains(plain, "running npm test") {
		t.Fatalf("Card dropped title/body: %q", plain)
	}
}

// COUNTER: below a minimal width a card truncates instead of overflowing — no
// line is wider than the requested Width. [D2]
func TestCardDegradesNarrow(t *testing.T) {
	out := Card(CardOpts{Title: "Permission", Body: "a very long body that cannot fit", Width: 12, Accent: RoleWaiting})
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 12 {
			t.Fatalf("Card overflowed min width: %q (%d)", line, w)
		}
	}
}

// ORACLE: a KV row keeps its key and value, with the value beginning at or after
// the fixed key column. [D2]
func TestKVAligns(t *testing.T) {
	out := stripSGR(KV("status", "ready", 10))
	if !strings.HasPrefix(out, "status") {
		t.Fatalf("KV lost key: %q", out)
	}
	if idx := strings.Index(out, "ready"); idx < 10 {
		t.Fatalf("KV value started at %d, want >= keyWidth 10: %q", idx, out)
	}
}

// COUNTER: a key longer than keyWidth is truncated so the value stays in
// alignment rather than being pushed right by the long key. [D2]
func TestKVTruncatesLongKey(t *testing.T) {
	out := stripSGR(KV("longlonglongkey", "VAL", 8))
	if idx := strings.Index(out, "VAL"); idx > 12 {
		t.Fatalf("long key pushed value to %d, want truncated near keyWidth 8: %q", idx, out)
	}
}

// ORACLE: Section is the kit-owned section header — fills width exactly, keeps
// the title, right-aligns the info. [D2]
func TestSectionFillsWidth(t *testing.T) {
	const w = 50
	out := Section("Details", w, "3 items")
	if got := lipgloss.Width(out); got != w {
		t.Fatalf("Section width %d, want %d", got, w)
	}
	plain := stripSGR(out)
	if !strings.Contains(plain, "Details") {
		t.Fatalf("Section dropped title: %q", plain)
	}
	if !strings.HasSuffix(strings.TrimRight(plain, " "), "3 items") {
		t.Fatalf("Section info not right-aligned: %q", plain)
	}
}

// ORACLE: an error block surfaces title, detail and action; COUNTER: with no
// detail/action it still shows the title and emits no empty lines. [D2]
func TestErrorBlockContents(t *testing.T) {
	out := stripSGR(ErrorBlock("Provision failed", "PVC bind timeout", "r retry"))
	for _, want := range []string{"Provision failed", "PVC bind timeout", "r retry"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ErrorBlock dropped %q: %q", want, out)
		}
	}
	bare := stripSGR(ErrorBlock("Boom", "", ""))
	if !strings.Contains(bare, "Boom") {
		t.Fatalf("ErrorBlock lost title with no detail/action: %q", bare)
	}
	if strings.Contains(bare, "\n\n") {
		t.Fatalf("ErrorBlock emitted empty lines for missing detail/action: %q", bare)
	}
}

// ORACLE: a badge renders its text; COUNTER: its display width is the same for
// the same text across roles (role changes color, not geometry). [D2]
func TestBadge(t *testing.T) {
	a := Badge("WAIT", RoleWaiting)
	if !strings.Contains(stripSGR(a), "WAIT") {
		t.Fatalf("Badge dropped text: %q", stripSGR(a))
	}
	if b := Badge("WAIT", RoleSuccess); lipgloss.Width(a) != lipgloss.Width(b) {
		t.Fatalf("Badge width varies by role: %d vs %d", lipgloss.Width(a), lipgloss.Width(b))
	}
}

// ORACLE: a button renders its label; COUNTER: the focused variant differs from
// the blurred one so focus is visible. [D2]
func TestButtonFocusDiffers(t *testing.T) {
	f := Button("Approve", true)
	b := Button("Approve", false)
	if !strings.Contains(stripSGR(f), "Approve") {
		t.Fatalf("Button dropped label: %q", stripSGR(f))
	}
	if f == b {
		t.Fatalf("focused button identical to blurred")
	}
}
