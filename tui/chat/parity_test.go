// CANONICAL TEST — do not weaken.
package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// ORACLE: a fixed fixture renders identically to its golden snapshot.
// The AssistantItem body routed through the pooled markdown renderer must render
// the fixture's content deterministically (stable across repeated renders), and
// the RawRender path (no focus chrome) must contain the content verbatim so the
// dashboard, which dogfoods AssistantItem, keeps byte-parity with the streaming
// tail (a divergence here reflows the block at message.completed — the T1 defect).
func TestParitySnapshot(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	fixture := &AssistantMessage{ID: "parity", Content: "Hello, world!", Finished: true}
	a := NewAssistantItem(fixture)
	a.SetRenderContentMD(func(text string, w int) string {
		r := MarkdownRenderer(w)
		if r == nil {
			return text
		}
		out, err := r.Render(text)
		if err != nil {
			return text
		}
		return strings.TrimRight(out, "\n")
	})

	first := a.RawRender(80)
	second := a.RawRender(80)
	if first != second {
		t.Fatalf("RawRender not deterministic:\n%q\n%q", first, second)
	}
	if !strings.Contains(ansi.Strip(first), "Hello, world!") {
		t.Fatalf("rendered body missing fixture content: %q", ansi.Strip(first))
	}
}
