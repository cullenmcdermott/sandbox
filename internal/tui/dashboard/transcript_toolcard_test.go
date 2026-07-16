package dashboard

import (
	"encoding/json"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// toolCardTM builds a laid-out transcript model wide enough that a tool card
// renders without truncation, for the shape/expansion tests.
func toolCardTM(t *testing.T) *TranscriptModel {
	t.Helper()
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 40
	m.layout()
	return m
}

// stripANSICodes removes SGR escapes so a test can assert on the visible text.
func stripANSICodes(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if c == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// TestToolCardTwoLineShape asserts the ⏺-head + ⎿-elbow two-line idiom: line one
// is "⏺ Name(arg)", line two is the indented "⎿  <result>".
func TestToolCardTwoLineShape(t *testing.T) {
	m := toolCardTM(t)
	c := &toolCard{tool: "Bash", arg: "npm test", status: toolOK, summary: "exit 0"}
	out := m.renderToolCard(c, 60)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("collapsed card = %d lines, want 2:\n%q", len(lines), out)
	}
	head := stripANSICodes(lines[0])
	if !strings.HasPrefix(head, toolHeadBullet+" ") {
		t.Errorf("head %q does not start with the ⏺ bullet", head)
	}
	if head != "⏺ Bash(npm test)" {
		t.Errorf("head = %q, want %q", head, "⏺ Bash(npm test)")
	}
	elbow := stripANSICodes(lines[1])
	if !strings.Contains(elbow, toolElbow) {
		t.Errorf("elbow line %q missing the ⎿ elbow", elbow)
	}
	if !strings.Contains(elbow, "exit 0") {
		t.Errorf("elbow line %q missing the result summary", elbow)
	}
}

// TestToolCardBulletTone asserts the head bullet is colored by status via theme
// tokens: running=Malibu, ok=Guac, error=Coral.
func TestToolCardBulletTone(t *testing.T) {
	m := toolCardTM(t)
	cases := []struct {
		status toolStatus
		tone   color.Color
		name   string
	}{
		{toolRunning, theme.Malibu, "running"},
		{toolOK, theme.Guac, "ok"},
		{toolErr, theme.Coral, "error"},
	}
	for _, tc := range cases {
		c := &toolCard{tool: "Bash", arg: "x", status: tc.status}
		head := strings.Split(m.renderToolCard(c, 60), "\n")[0]
		wantBullet := lipgloss.NewStyle().Foreground(tc.tone).Render(toolHeadBullet)
		if !strings.Contains(head, wantBullet) {
			t.Errorf("%s: head %q missing the status-toned bullet %q", tc.name, head, wantBullet)
		}
	}
}

// TestToolCardExpandToggle asserts ctrl+o's toggleLatestExpandable flips the card's
// expanded state, bumps its version (so the list re-renders), and reveals the
// captured output that the collapsed card hid behind "N lines".
func TestToolCardExpandToggle(t *testing.T) {
	m := toolCardTM(t)
	m.startToolCard("Bash", "echo hi")
	m.finishToolCard(toolOK, "3 lines", "Bash", "one\ntwo\nthree", "")

	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockToolCard || last.tool == nil {
		t.Fatalf("expected a trailing tool card, got kind=%v", last.kind)
	}
	if last.tool.expanded {
		t.Fatal("card should start collapsed")
	}
	// Collapsed: the output body is not shown, only the "N lines" summary.
	if got := stripANSICodes(m.renderToolCard(last.tool, 60)); strings.Contains(got, "three") {
		t.Errorf("collapsed card leaked output: %q", got)
	}

	verBefore := last.Version()
	if !m.toggleLatestExpandable() {
		t.Fatal("toggleLatestExpandable returned false; expected a tool card to toggle")
	}
	if !last.tool.expanded {
		t.Error("card did not become expanded after toggle")
	}
	if last.Version() == verBefore {
		t.Error("toggle did not bump the card version (list would not re-render)")
	}
	// Expanded: the captured output is now visible.
	got := stripANSICodes(m.renderToolCard(last.tool, 60))
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(got, want) {
			t.Errorf("expanded card missing output line %q:\n%s", want, got)
		}
	}

	// Toggling again collapses and bumps the version once more.
	verExpanded := last.Version()
	m.toggleLatestExpandable()
	if last.tool.expanded {
		t.Error("second toggle did not collapse the card")
	}
	if last.Version() == verExpanded {
		t.Error("second toggle did not bump the version")
	}
}

// TestToolCardDiffPreservedAfterApproval asserts an edit tool card can render its
// diff from the retained input on expansion — with no permission box present —
// so a post-approval diff stays viewable in scrollback (slice 5i).
func TestToolCardDiffPreservedAfterApproval(t *testing.T) {
	m := toolCardTM(t)
	input := json.RawMessage(`{"file_path":"main.go","old_string":"old line","new_string":"new line"}`)
	c := &toolCard{
		tool:     "Edit",
		arg:      "main.go",
		input:    input,
		status:   toolOK,
		summary:  "+1 −1",
		expanded: true,
	}
	got := stripANSICodes(m.renderToolCard(c, 60))
	if !strings.Contains(got, "+new line") {
		t.Errorf("expanded edit card missing the added diff line:\n%s", got)
	}
	if !strings.Contains(got, "−old line") {
		t.Errorf("expanded edit card missing the removed diff line:\n%s", got)
	}
}

// TestExpandedOutputSanitized (H4): control sequences in captured output must
// not survive into the frame — cursor movement / erase-line would execute
// inside the composited frame and smear the transcript. CR progress rewrites
// collapse to their final state; SGR color runs survive (RemapANSI maps them).
func TestExpandedOutputSanitized(t *testing.T) {
	m := toolCardTM(t)
	out := "downloading 10%\rdownloading 100%\n\x1b[1A\x1b[2Kdone \x1b[32mok\x1b[0m\x1b]0;title\x07"
	c := &toolCard{tool: "Bash", arg: "dl", status: toolOK, summary: "exit 0", output: out, expanded: true}
	got := m.renderToolCard(c, 60)
	if strings.Contains(got, "\r") {
		t.Errorf("expanded card leaked a CR:\n%q", got)
	}
	for _, esc := range []string{"\x1b[1A", "\x1b[2K", "\x1b]0;"} {
		if strings.Contains(got, esc) {
			t.Errorf("expanded card leaked control sequence %q:\n%q", esc, got)
		}
	}
	plain := stripANSICodes(got)
	if strings.Contains(plain, "downloading 10%") || !strings.Contains(plain, "downloading 100%") {
		t.Errorf("CR rewrite did not keep only the final line state:\n%q", plain)
	}
	if !strings.Contains(plain, "ok") {
		t.Errorf("SGR-styled text was lost by sanitization:\n%q", plain)
	}
}

// TestExpandedOutputTabsExpanded (H5): tabs measure width 0 in lipgloss but
// render up to 8 columns in a terminal, so they must become spaces before
// truncation for the no-overflow budget to hold — both for captured output
// and for diff lines (via styleDiffLine).
func TestExpandedOutputTabsExpanded(t *testing.T) {
	m := toolCardTM(t)
	c := &toolCard{tool: "Bash", arg: "cat", status: toolOK, summary: "exit 0",
		output: "\tindented\nx\ty", expanded: true}
	got := m.renderToolCard(c, 60)
	if strings.Contains(got, "\t") {
		t.Errorf("expanded output leaked a raw tab:\n%q", got)
	}
	plain := stripANSICodes(got)
	if !strings.Contains(plain, "        indented") {
		t.Errorf("leading tab was not expanded to the 8-column stop:\n%q", plain)
	}
	if !strings.Contains(plain, "x       y") {
		t.Errorf("mid-line tab was not expanded to the next stop:\n%q", plain)
	}

	input := json.RawMessage(`{"file_path":"a.go","old_string":"\tif x {","new_string":"\tif y {"}`)
	d := &toolCard{tool: "Edit", arg: "a.go", input: input, status: toolOK, expanded: true}
	if got := m.renderToolCard(d, 60); strings.Contains(got, "\t") {
		t.Errorf("expanded diff leaked a raw tab:\n%q", got)
	}
}

// TestToggleSkipsInexpandableCards (H7): ctrl+o must not flip a card that has
// nothing to reveal — that stranded expanded=true, popping the card open by
// itself when a later completion delivered output. The toggle skips to the
// most recent expandable card instead; collapse of an already-open card
// always works.
func TestToggleSkipsInexpandableCards(t *testing.T) {
	m := toolCardTM(t)
	m.startToolCard("Bash", "make")
	m.finishToolCard(toolOK, "3 lines", "Bash", "one\ntwo\nthree", "")
	expandable := m.blocks[len(m.blocks)-1]
	// A newer card with nothing to reveal: no output, no diff, arg fits in full.
	m.startToolCard("Bash", "true")
	m.finishToolCard(toolOK, "done", "Bash", "", "")
	bare := m.blocks[len(m.blocks)-1]

	if !m.toggleLatestExpandable() {
		t.Fatal("toggle found no card; expected it to skip to the expandable one")
	}
	if bare.tool.expanded {
		t.Error("content-less card was toggled")
	}
	if !expandable.tool.expanded {
		t.Error("expandable card was not toggled")
	}
	if !m.toggleLatestExpandable() {
		t.Fatal("second toggle found no card")
	}
	if expandable.tool.expanded {
		t.Error("second toggle did not collapse the expanded card")
	}
}

// TestToggleNoExpandableCardFallsThrough (H7): when every card is content-less
// the toggle reports false so ctrl+o is a swallowed no-op.
func TestToggleNoExpandableCardFallsThrough(t *testing.T) {
	m := toolCardTM(t)
	m.startToolCard("Bash", "true")
	m.finishToolCard(toolOK, "done", "Bash", "", "")
	if m.toggleLatestExpandable() {
		t.Error("toggle claimed a card with nothing to expand")
	}
}

// TestToolCardNoOverflowNarrow is the §1c construction guarantee: at narrow
// widths the two-line layout (collapsed AND expanded) budgets from the measured
// remaining width and never renders a line wider than the terminal.
func TestToolCardNoOverflowNarrow(t *testing.T) {
	m := toolCardTM(t)
	longArg := "some/very/long/path/that/keeps/going/and/going/main.go --with --many --flags"
	longOut := strings.Repeat("a very long output line that exceeds the card width by a lot\n", 5)
	for _, w := range []int{20, 30, 40} {
		cards := []*toolCard{
			{tool: "Bash", arg: longArg, status: toolOK, summary: "exit 0 with a rather long summary line here", output: longOut},
			{tool: "Edit", arg: longArg, input: json.RawMessage(`{"old_string":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","new_string":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`), status: toolOK, summary: "+1 −1"},
		}
		for _, c := range cards {
			for _, expanded := range []bool{false, true} {
				c.expanded = expanded
				out := m.renderToolCard(c, w)
				for i, line := range strings.Split(out, "\n") {
					if got := lipgloss.Width(line); got > w {
						t.Errorf("width=%d %s expanded=%v line %d width=%d > %d: %q",
							w, c.tool, expanded, i, got, w, stripANSICodes(line))
					}
				}
			}
		}
	}
}
