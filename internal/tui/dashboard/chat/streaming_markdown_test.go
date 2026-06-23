// CANONICAL TEST — do not weaken. internal/tui/dashboard/chat/streaming_markdown_test.go
package chat

import (
	"strings"
	"testing"

	glamour "charm.land/glamour/v2"
)

func newRenderer(t *testing.T, width int) *glamour.TermRenderer {
	t.Helper()
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(width))
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	return r
}

// countingRenderer wraps a real renderer and sums input bytes — the behavioral counter.
type countingRenderer struct {
	inner    *glamour.TermRenderer
	calls    int
	bytesIn  int
	maxInput int
}

func (c *countingRenderer) Render(in string) (string, error) {
	c.calls++
	c.bytesIn += len(in)
	if len(in) > c.maxInput {
		c.maxInput = len(in)
	}
	return c.inner.Render(in)
}

func fullRender(t *testing.T, r *glamour.TermRenderer, s string) string {
	out, err := r.Render(s)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return strings.TrimSuffix(out, "\n")
}

// Adversarial corpus — MUST include each construct the boundary detector reasons about.
var corpus = []string{
	"# Title\n\nFirst paragraph here.\n\nSecond paragraph with more words to wrap.\n\nThird.\n",
	"Para one.\n\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\n\nPara after code.\n",
	"Intro.\n\n- item one\n- item two\n- item three\n\nAfter the list.\n",
	"Lead.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nAfter table.\n",
	"Quote:\n\n> a quoted line\n> another\n\nAfter quote.\n",
	"Heading\n=======\n\nBody under setext header.\n\nMore.\n",
	"Before.\n\n<div>\nraw html\n</div>\n\nAfter html.\n",
	"See [ref].\n\n[ref]: https://example.com\n\nTrailing paragraph.\n",
	strings.Repeat("A paragraph of several words that should wrap nicely.\n\n", 30),
}

// ORACLE: for every byte prefix of every corpus doc, the streaming render equals
// a full render of that same prefix. Cutting unsafely or gluing wrong fails here.
func TestStreamingEqualsFullRenderEveryPrefix(t *testing.T) {
	const width = 60
	for di, doc := range corpus {
		r := newRenderer(t, width)
		var s StreamingMarkdown
		for i := 0; i <= len(doc); i++ {
			got := s.Render(doc[:i], width, r)
			want := fullRender(t, r, doc[:i])
			if got != want {
				t.Fatalf("doc %d prefix len %d:\n got=%q\nwant=%q", di, i, got, want)
			}
		}
	}
}

// ORACLE: width change mid-stream still equals full render at the new width.
func TestStreamingWidthChangeMidStream(t *testing.T) {
	doc := corpus[8]
	var s StreamingMarkdown
	for _, w := range []int{40, 80, 40} {
		r := newRenderer(t, w)
		mid := len(doc) / 2
		got := s.Render(doc[:mid], w, r)
		if want := fullRender(t, r, doc[:mid]); got != want {
			t.Fatalf("width %d: got=%q want=%q", w, got, want)
		}
	}
}

// ORACLE: a non-prefix rewrite (retry) drops the cache and renders correctly.
func TestStreamingRewriteResets(t *testing.T) {
	const width = 60
	r := newRenderer(t, width)
	var s StreamingMarkdown
	_ = s.Render("Original answer.\n\nWith two paragraphs.\n", width, r)
	got := s.Render("Totally different answer now.\n", width, r)
	if want := fullRender(t, r, "Totally different answer now.\n"); got != want {
		t.Fatalf("rewrite: got=%q want=%q", got, want)
	}
}

// COUNTER: streaming a 30-paragraph doc paragraph-by-paragraph renders far fewer
// bytes than the quadratic naive path. Naive (full render each flush) would push
// ~ n/2 * len(doc) bytes; we require <= 4 * len(doc).
func TestStreamingIsSubQuadratic(t *testing.T) {
	const width = 60
	paras := make([]string, 30)
	for i := range paras {
		paras[i] = "Paragraph number with a handful of words to wrap across the width."
	}
	doc := strings.Join(paras, "\n\n") + "\n"
	cr := &countingRenderer{inner: newRenderer(t, width)}
	var s StreamingMarkdown

	// Feed cumulative content ending at each paragraph boundary.
	acc := ""
	for i, p := range paras {
		if i > 0 {
			acc += "\n\n"
		}
		acc += p
		s.Render(acc+"\n\n", width, cr)
	}
	if cr.bytesIn > 4*len(doc) {
		t.Fatalf("rendered %d bytes for a %d-byte doc; want <= %d (cache not engaging)",
			cr.bytesIn, len(doc), 4*len(doc))
	}
	// And the final flush must NOT have re-rendered the whole document.
	if cr.maxInput >= len(doc) {
		t.Fatalf("a single render took the whole doc (%d >= %d): not incremental", cr.maxInput, len(doc))
	}
}

// COUNTER+ORACLE: the boundary detector actually finds boundaries where it should,
// and refuses where it must.
func TestBoundaryDecisions(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool // true => a boundary >= 0 is expected
	}{
		{"two paragraphs", "a\n\nb", true},
		{"open code fence", "```go\ncode\n\nmore", false},
		{"closed code fence then para", "```\ncode\n```\n\nafter", true},
		{"open list", "- one\n\ntext", false},
		{"table", "| a |\n\nafter", false},
		{"blockquote", "> q\n\nafter", false},
		{"link ref def", "[r]: u\n\nafter", false},
		{"setext underline follows", "title\n\n=====", false},
		{"no blank line", "single line", false},
	}
	for _, c := range cases {
		got := findSafeBoundary(c.in) >= 0
		if got != c.want {
			t.Fatalf("%s: findSafeBoundary>=0 = %v, want %v (in=%q)", c.name, got, c.want, c.in)
		}
	}
}
