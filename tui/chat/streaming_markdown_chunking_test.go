package chat

import (
	"math/rand"
	"strings"
	"testing"
)

// identityRenderer is an append-only, allocation-free stand-in for glamour so the
// benchmark isolates the safe-boundary/scan cost (the O(N²) the incremental cache
// set out to kill) from glamour's own rendering time. It satisfies the two
// properties the streaming cache relies on: render(A) is a prefix of render(A+B),
// and a block's contribution after A depends only on A's tail.
type identityRenderer struct{}

func (identityRenderer) Render(s string) (string, error) { return s + "\n", nil }

// chunkPrefixes returns the cumulative prefix lengths a streaming consumer would
// feed for a given chunking of doc. The final length is always len(doc).
func chunkPrefixes(doc string, mode string, seed int64) []int {
	n := len(doc)
	switch mode {
	case "whole":
		return []int{n}
	case "byte":
		out := make([]int, 0, n)
		for i := 1; i <= n; i++ {
			out = append(out, i)
		}
		return out
	default: // "random"
		rng := rand.New(rand.NewSource(seed))
		var out []int
		p := 0
		for p < n {
			p += 1 + rng.Intn(7)
			if p > n {
				p = n
			}
			out = append(out, p)
		}
		if len(out) == 0 || out[len(out)-1] != n {
			out = append(out, n)
		}
		return out
	}
}

// scanDocs is an adversarial corpus that exercises every construct the boundary
// detector and the incremental scanner reason about, including cases where a
// delta lands mid-line (partial fence lines, a partial link reference definition,
// a partial setext underline).
var scanDocs = append(append([]string{}, corpus...),
	"Intro paragraph wraps.\n\n```go\nfunc f() {\n\treturn // [k]: not a ref\n}\n```\n\nAfter code.\n",
	"````\nnested ``` fence inside\n````\n\nAfter longer fence.\n",
	"Lead line here.\n\n* * *\n\nThematic break above, not a list.\n\n- real item\n- second\n\nEnd.\n",
	"Para.\n\n> quoted [ref]: not-at-doc-scope\n\n[ref]: https://example.com\n\nTail.\n",
	"See [label] later.\n\nMiddle paragraph with words to wrap across.\n\n[label]: https://example.org\n\nEnd paragraph.\n",
	strings.Repeat("# Heading here\n\nBody paragraph with several words to wrap.\n\n", 8),
)

// TestIncrementalScannerMatchesReference is the property test: for every doc, fed
// in every chunking, the incremental scanner's findSafeBoundary/hasLinkRef must
// agree with the from-scratch reference free functions at every fed prefix. The
// reference is chunking-independent, so agreement across every split point (incl.
// mid-line deltas) proves the incremental state is split-invariant.
func TestIncrementalScannerMatchesReference(t *testing.T) {
	for di, doc := range scanDocs {
		for _, mode := range []string{"whole", "byte", "random"} {
			for _, seed := range []int64{1, 7, 42} {
				if mode != "random" && seed != 1 {
					continue // seed only matters for random
				}
				var sc mdScanner
				for _, p := range chunkPrefixes(doc, mode, seed) {
					content := doc[:p]
					sc.sync(content)
					if got, want := sc.findSafeBoundary(content), findSafeBoundary(content); got != want {
						t.Fatalf("doc %d mode %s seed %d prefix %d: incremental findSafeBoundary=%d, reference=%d",
							di, mode, seed, p, got, want)
					}
					if got, want := sc.hasLinkRef(content), hasLinkRefDef(content); got != want {
						t.Fatalf("doc %d mode %s seed %d prefix %d: incremental hasLinkRef=%v, reference=%v",
							di, mode, seed, p, got, want)
					}
				}
			}
		}
	}
}

// TestStreamingRenderChunkingInvariant asserts the full Render path is byte-exact
// against a from-scratch glamour render at every fed prefix, for arbitrary
// chunkings of the same document — the same safe-boundary decisions regardless of
// how the stream is split.
func TestStreamingRenderChunkingInvariant(t *testing.T) {
	const width = 60
	// Uses the canonical corpus (byte-exact at every prefix, per
	// TestStreamingEqualsFullRenderEveryPrefix). scanDocs adds inputs that expose
	// a glamour syntax-highlight context quirk unrelated to boundary scanning
	// (the same code block colors differently by trailing content), so it is
	// reserved for the pure-function scanner test below.
	for di, doc := range corpus {
		for _, mode := range []string{"whole", "byte", "random"} {
			for _, seed := range []int64{3, 11} {
				if mode != "random" && seed != 3 {
					continue
				}
				var s StreamingMarkdown
				r := newRenderer(t, width)
				oracle := newRenderer(t, width)
				var final string
				for _, p := range chunkPrefixes(doc, mode, seed) {
					got := s.Render(doc[:p], width, r)
					want := fullRender(t, oracle, doc[:p])
					if got != want {
						t.Fatalf("doc %d mode %s seed %d prefix %d not byte-exact:\n got=%q\nwant=%q",
							di, mode, seed, p, got, want)
					}
					final = got
				}
				if want := fullRender(t, oracle, doc); final != want {
					t.Fatalf("doc %d mode %s seed %d final render diverged:\n got=%q\nwant=%q",
						di, mode, seed, final, want)
				}
			}
		}
	}
}

// BenchmarkStreamingDeltas streams a multi-block markdown document token-by-token
// through the identity renderer, so the measured time is dominated by the
// safe-boundary/scan work per delta. A from-scratch rescan per delta is O(N²)
// over the turn; the incremental scanner is O(N).
func BenchmarkStreamingDeltas(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("## Section heading number here\n\n")
		sb.WriteString("Body paragraph with a handful of words that wrap across the target width nicely.\n\n")
	}
	doc := sb.String()

	var offs []int
	for i := 6; i < len(doc); i += 6 {
		offs = append(offs, i)
	}
	offs = append(offs, len(doc))

	r := identityRenderer{}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		var s StreamingMarkdown
		for _, off := range offs {
			s.Render(doc[:off], 60, r)
		}
	}
}
