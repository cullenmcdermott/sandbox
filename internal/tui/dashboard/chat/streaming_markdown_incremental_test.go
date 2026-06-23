package chat

import (
	"strings"
	"testing"

	glamour "charm.land/glamour/v2"
)

// countingRendererInc mirrors the canonical counter but lives in this file so
// the regression below is self-contained.
type countingRendererInc struct {
	inner    *glamour.TermRenderer
	calls    int
	bytesIn  int
	maxInput int
}

func (c *countingRendererInc) Render(in string) (string, error) {
	c.calls++
	c.bytesIn += len(in)
	if len(in) > c.maxInput {
		c.maxInput = len(in)
	}
	return c.inner.Render(in)
}

func newRendererInc(t *testing.T, width int) *glamour.TermRenderer {
	t.Helper()
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(width))
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	return r
}

// REGRESSION (A2): streaming-prefix caching must be byte-exact across HETERO-
// GENEOUS block junctions (heading→paragraph), not just paragraph→paragraph.
// A naive fixed-paragraph-context delta passes a paragraph-only corpus but
// mis-renders the heading→paragraph margin — this test would have caught that.
// It also asserts the cache actually engages: no single render call is fed the
// whole document, and total bytes stay sub-quadratic, even with the mixed
// block types that broke the first implementation.
func TestStreamingIncrementalAcrossHeadingJunctions(t *testing.T) {
	const width = 60

	// Many (heading + paragraph) pairs → every other junction is heading→para.
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("# Section heading number here\n\n")
		b.WriteString("Body paragraph with a handful of words to wrap across width.\n\n")
	}
	doc := b.String()

	cr := &countingRendererInc{inner: newRendererInc(t, width)}
	var s StreamingMarkdown

	// Stream at every block boundary and assert byte-exact parity with a full
	// render of the same prefix.
	oracle := newRendererInc(t, width)
	full := func(in string) string {
		out, err := oracle.Render(in)
		if err != nil {
			t.Fatalf("oracle render: %v", err)
		}
		return strings.TrimSuffix(out, "\n")
	}

	for _, p := range boundaryPrefixes(doc) {
		got := s.Render(p, width, cr)
		if want := full(p); got != want {
			t.Fatalf("prefix len %d not byte-exact:\n got=%q\nwant=%q", len(p), got, want)
		}
	}

	// The cache must engage: no single render saw the whole doc, and total
	// renderer input is far below the quadratic full-render-each-flush cost.
	if cr.maxInput >= len(doc) {
		t.Fatalf("a single render took the whole doc (%d >= %d): not incremental", cr.maxInput, len(doc))
	}
	if cr.bytesIn > 5*len(doc) {
		t.Fatalf("rendered %d bytes for a %d-byte doc; want <= %d (cache not engaging across heading junctions)",
			cr.bytesIn, len(doc), 5*len(doc))
	}
}

// boundaryPrefixes returns the cumulative prefixes of doc ending at each
// blank-line block boundary (the points a streaming flush typically lands).
func boundaryPrefixes(doc string) []string {
	var out []string
	for i := 2; i <= len(doc); i++ {
		if doc[i-2] == '\n' && doc[i-1] == '\n' {
			out = append(out, doc[:i])
		}
	}
	if len(out) == 0 || out[len(out)-1] != doc {
		out = append(out, doc)
	}
	return out
}

// REGRESSION (B7): the canonical TestStreamingIsSubQuadratic is only meaningful
// if Render actually pushes bytes through the INJECTED renderer. The old Render
// ignored its r parameter and used an internal glamour renderer, so the
// counting renderer was never called and both sub-quadratic assertions held
// vacuously (cr.bytesIn / cr.maxInput stayed 0). This asserts non-vacuity
// directly: a Render that ignores r leaves cr.calls == 0 and fails here.
func TestStreamingInvokesInjectedRenderer(t *testing.T) {
	const width = 60
	cr := &countingRendererInc{inner: newRendererInc(t, width)}
	var s StreamingMarkdown
	out := s.Render("First paragraph here with several words.\n\nSecond paragraph follows.\n\n", width, cr)
	if cr.calls == 0 || cr.bytesIn == 0 {
		t.Fatalf("injected renderer never invoked (calls=%d bytesIn=%d): the sub-quadratic counter is vacuous",
			cr.calls, cr.bytesIn)
	}
	if out == "" {
		t.Fatal("Render produced empty output")
	}
}

// REGRESSION (A2): a link reference definition retroactively changes earlier
// blocks; the renderer must stay byte-exact at every prefix despite the cache.
func TestStreamingLinkRefStaysExact(t *testing.T) {
	const width = 60
	doc := "See [ref] for details here in this line.\n\n[ref]: https://example.com\n\nTrailing words.\n"
	oracle := newRendererInc(t, width)
	var s StreamingMarkdown
	r := newRendererInc(t, width)
	for i := 0; i <= len(doc); i++ {
		got := s.Render(doc[:i], width, r)
		out, err := oracle.Render(doc[:i])
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		if want := strings.TrimSuffix(out, "\n"); got != want {
			t.Fatalf("prefix len %d:\n got=%q\nwant=%q", i, got, want)
		}
	}
}
