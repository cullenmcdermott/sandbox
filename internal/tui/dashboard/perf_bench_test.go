package dashboard

// Benchmarks for the two §4 "measure-first" perf items, so the decision to
// memoize (or not) rests on numbers instead of on how the code reads:
//
//   - visibleSessions() re-filters + re-sorts, and the render path calls it
//     several times per frame.
//   - fitModal does an ANSI width scan per visible line, every frame.
//
// The bar is a 60fps frame budget: 16.6ms for EVERYTHING the dashboard does in
// a frame. A helper costing single-digit microseconds cannot matter at any
// plausible session count; one costing hundreds of microseconds, called 4x,
// would.

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// benchSessions builds n sessions with realistic-length titles/paths, since
// both the fuzzy filter and the sort read those strings.
func benchSessions(n int) []Session {
	out := make([]Session, 0, n)
	for i := range n {
		s := makeSession(fmt.Sprintf("claude-pane-proj%d-%08x", i%7, i), StatusIdle)
		s.Title = fmt.Sprintf("refactor the %d-th subsystem for parity", i)
		s.State.ProjectPath = fmt.Sprintf("/Users/dev/git/project-%d", i%7)
		s.State.Backend = "claude-pane"
		out = append(out, s)
	}
	return out
}

func benchModel(n int) *Model {
	m := &Model{sessions: benchSessions(n)}
	return m
}

// The realistic fleet sizes. 8 is a busy day; 50 is well past what the session
// list is usable at; 200 is there to show the growth curve, not to model
// reality.
var benchSizes = []int{8, 50, 200}

func BenchmarkVisibleSessions(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("n=%d/no-filter", n), func(b *testing.B) {
			m := benchModel(n)
			b.ReportAllocs()
			for b.Loop() {
				_ = m.visibleSessions()
			}
		})
		// The filtering case is the expensive one: a non-empty query runs
		// fuzzyMatch over every session's combined title+path+backend.
		b.Run(fmt.Sprintf("n=%d/filtered", n), func(b *testing.B) {
			m := benchModel(n)
			m.filter = "parity"
			b.ReportAllocs()
			for b.Loop() {
				_ = m.visibleSessions()
			}
		})
		// attentionFirst is the other cost: sortByAttention is a PASSTHROUGH when
		// it is off (which is why the unfiltered case is ~5ns at any n), and a
		// two-pass partition into a fresh slice when it is on.
		b.Run(fmt.Sprintf("n=%d/attention-first", n), func(b *testing.B) {
			m := benchModel(n)
			m.attentionFirst = true
			b.ReportAllocs()
			for b.Loop() {
				_ = m.visibleSessions()
			}
		})
		// What the render path actually does: several calls per frame, in the
		// most expensive configuration (filter active AND attention sorting on).
		b.Run(fmt.Sprintf("n=%d/x4-per-frame", n), func(b *testing.B) {
			m := benchModel(n)
			m.filter = "parity"
			m.attentionFirst = true
			b.ReportAllocs()
			for b.Loop() {
				for range 4 {
					_ = m.visibleSessions()
				}
			}
		})
	}
}

// fitModal's input in the wild is styled output: SGR sequences interleaved with
// text, which is exactly what makes lipgloss.Width non-trivial.
func benchModalBody(lines, width int) string {
	style := lipgloss.NewStyle().Bold(true)
	var b strings.Builder
	for i := range lines {
		b.WriteString(style.Render(fmt.Sprintf("row %d ", i)))
		b.WriteString(strings.Repeat("x", max(0, width-12)))
		if i < lines-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func BenchmarkFitModal(b *testing.B) {
	// A tall feed pane is the worst realistic case — fitModal feeds the feed
	// view as well as modals.
	for _, dims := range []struct{ h, w int }{{20, 80}, {50, 120}, {120, 200}} {
		b.Run(fmt.Sprintf("h=%d/w=%d", dims.h, dims.w), func(b *testing.B) {
			body := benchModalBody(dims.h, dims.w)
			b.ReportAllocs()
			for b.Loop() {
				_ = fitModal(body, dims.w, dims.h)
			}
		})
	}
}

// [P7]: the feed's assistant-delta path rebuilds the whole message on EVERY
// delta — streamBuf.String() copies the accumulated text, TrimSpace walks it,
// and feedItem.set compares it against the previous full string. That is O(L·k)
// for a message of length L arriving in k deltas.
//
// The transcript had the same shape and it mattered there ([E1]/[E7]); the feed
// is a smaller surface, so the item is filed "only if felt". These benchmarks
// decide it. The realistic shapes: a chatty reply (~4 KB in 200 deltas) and a
// long one (~32 KB in 1600 deltas) — the second is roughly the worst a single
// assistant turn produces.
func benchDeltaEvents(totalBytes, deltas int) []session.Event {
	chunk := strings.Repeat("a", max(1, totalBytes/deltas))
	out := make([]session.Event, 0, deltas)
	for range deltas {
		out = append(out, mkEvent(session.EventMessageDelta, session.MessagePayload{
			Role: "assistant", Content: chunk,
		}))
	}
	return out
}

func BenchmarkFeedAssistantStream(b *testing.B) {
	for _, tc := range []struct {
		name         string
		bytes, delta int
	}{
		{"4KB/200deltas", 4 << 10, 200},
		{"32KB/1600deltas", 32 << 10, 1600},
		// Same bytes as the case above, 8x fewer deltas: separates the O(L·k)
		// rebuild term from the per-delta constants (unmarshal + allocs). If the
		// quadratic term dominated, holding L fixed while cutting k would barely
		// help; if the constants dominate, time falls with k.
		{"32KB/200deltas", 32 << 10, 200},
	} {
		b.Run(tc.name, func(b *testing.B) {
			events := benchDeltaEvents(tc.bytes, tc.delta)
			b.ReportAllocs()
			for b.Loop() {
				m := newFeedModel(session.Ref{}, "t", "claude")
				for _, ev := range events {
					m.reduce(ev)
				}
			}
		})
	}
}
