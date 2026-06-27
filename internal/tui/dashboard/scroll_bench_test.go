package dashboard

import (
	"fmt"
	"strings"
	"testing"
)

// buildBigTranscript returns a transcript model seeded with n turns of
// user+assistant blocks (the assistant blocks are multi-paragraph to exercise
// wrapping), sized to a typical modal viewport and pinned to the bottom.
func buildBigTranscript(n int) *TranscriptModel {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	para := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 6)
	for i := 0; i < n; i++ {
		m.appendBlock(blockUser, fmt.Sprintf("question number %d about the codebase", i))
		m.appendBlock(blockAssistant, fmt.Sprintf("## Answer %d\n\n%s\n\n%s", i, para, para))
	}
	m.seedSize(120, 40)
	m.body.GotoBottom()
	return m
}

// BenchmarkBodyView guards the per-frame body render cost on a long transcript.
// This is the scroll-lag hot path: it once spent ~830µs / 33k allocs in a single
// lipgloss Style.Width().Height().Render() call (replaced by fitModal). Keep this
// from regressing — a long transcript must render its viewport cheaply.
func BenchmarkBodyView(b *testing.B) {
	m := buildBigTranscript(300)
	_ = m.bodyView() // warm the height cache
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.bodyView()
	}
}

// BenchmarkScrollAndRender measures a wheel-notch scroll followed by a body
// render — the interactive scroll cycle.
func BenchmarkScrollAndRender(b *testing.B) {
	m := buildBigTranscript(300)
	_ = m.bodyView()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			m.body.ScrollBy(-3)
		} else {
			m.body.ScrollBy(3)
		}
		_ = m.bodyView()
	}
}

// BenchmarkListTotalHeight guards that the scrollbar's full-list height walk
// stays cache-backed (0 allocs when nothing changed) rather than re-rendering
// every off-screen block.
func BenchmarkListTotalHeight(b *testing.B) {
	m := buildBigTranscript(300)
	_ = m.body.TotalHeight() // warm
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.body.TotalHeight()
	}
}
