package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard/chat"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// TestAssistantBlockRoutesThroughChat is the A1 regression: the live
// blockAssistant render path must go through chat.AssistantItem +
// chat.MarkdownRenderer (the pooled glamour renderer), not a private
// per-model glamour renderer. Before A1 the chat package was dead code —
// no non-test file imported it. This test proves the wiring is live and
// byte-identical to a direct pool render.
func TestAssistantBlockRoutesThroughChat(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	// No m.md setup, no layout() call — renderBlock must work standalone,
	// sourcing its renderer from the chat pool.
	const body = "Here is **bold** text and a list:\n\n- one\n- two\n"
	got := m.renderBlock(tblock{kind: blockAssistant, text: body})

	// Oracle: the same pool renderer chat.MarkdownRenderer at the gutter-reduced
	// width, then wrapped in the A2.1 Charple role gutter — exactly what
	// renderBlock does for a finalized assistant block.
	wrap := m.width - 2 - gutterInset
	if wrap < 20 {
		wrap = 20
	}
	r := chat.MarkdownRenderer(wrap)
	if r == nil {
		t.Fatal("chat.MarkdownRenderer returned nil — pool broken")
	}
	out, err := r.Render(body)
	if err != nil {
		t.Fatalf("pool render: %v", err)
	}
	want := gutterPrefix(strings.TrimRight(out, "\n"), theme.Charple)

	if got != want {
		t.Fatalf("blockAssistant does not route through chat pool.\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}

	// The output must contain glamour ANSI escapes (real markdown rendering),
	// proving we did not fall through to the styleTAssistant plain-text path.
	if !strings.ContainsAny(got, "\x1b[") {
		t.Fatalf("rendered assistant block has no ANSI escapes — fallback path taken:\n%q", got)
	}
}

// TestAssistantBlockChatPoolMemoized asserts the pool is actually memoizing:
// two renderBlock calls at the same width must share the same underlying
// renderer (chat.MarkdownRenderer returns the same pointer). This is the
// A3 prerequisite made testable from the dashboard side.
func TestAssistantBlockChatPoolMemoized(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200

	wrap := m.width - 2
	if wrap < 20 {
		wrap = 20
	}
	r1 := chat.MarkdownRenderer(wrap)
	r2 := chat.MarkdownRenderer(wrap)
	if r1 != r2 {
		t.Fatal("chat.MarkdownRenderer did not memoize — pool not engaged")
	}

	// Rendering twice must not panic and must be stable. (Capture into separate
	// vars: comparing the same call expression to itself is a no-op — SA4000.)
	b := tblock{kind: blockAssistant, text: "**hi**"}
	first := m.renderBlock(b)
	second := m.renderBlock(b)
	if first != second {
		t.Fatal("renderBlock(blockAssistant) not stable across calls")
	}
}

// TestAssistantBlockEmptyRendersEmpty guards the empty-content case: an
// assistant block with no text renders to the empty string (RawRender emits no
// section), matching the former m.md path on empty input.
func TestAssistantBlockEmptyRendersEmpty(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	if got := m.renderBlock(tblock{kind: blockAssistant, text: ""}); got != "" {
		t.Fatalf("empty assistant block should render empty, got %q", got)
	}
}
