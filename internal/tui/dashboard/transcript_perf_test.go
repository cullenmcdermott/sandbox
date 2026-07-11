package dashboard

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// --- T1: bottom-drift (wrap-width unification) ----------------------------

// TestAssistantWrapWidthUnified locks the shared wrap width: the finalized block
// (renderBlockBody) and the streaming tail (blockCard.renderStreamTail) both call
// this, so a completed message can't reflow to a different line count and lurch
// off the bottom.
func TestAssistantWrapWidthUnified(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width = 50
	if got, want := m.assistantWrapWidth(), m.width-2-gutterInset; got != want {
		t.Fatalf("assistantWrapWidth = %d, want %d", got, want)
	}
	m.width = 10 // narrow: clamps to a floor
	if got := m.assistantWrapWidth(); got != 20 {
		t.Fatalf("narrow assistantWrapWidth = %d, want clamp to 20", got)
	}
}

// TestStreamTailAndFinalizedSameHeight is the behavioral T1 guard: for the same
// (plain-prose) message the live streaming tail and the finalized block must
// wrap to the same number of lines. Plain prose wraps identically under both the
// streaming renderer and the full render at a given width, so an unequal height
// can only mean the two wrap widths diverged (the drift bug).
func TestStreamTailAndFinalizedSameHeight(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 48, 24
	m.layout()
	text := strings.TrimSpace(strings.Repeat("the quick brown fox jumps over the lazy dog ", 6))

	// Set up the streaming renderer the way EventMessageStarted does, fill the
	// live buffer, and build the ephemeral tail item via the production path.
	m.handleEvent(session.Event{Type: session.EventMessageStarted, Payload: jraw(`{}`)})
	m.assistantBuf.Reset()
	m.assistantBuf.WriteString(text)
	m.streaming = true
	m.commitItems()
	if m.streamItem == nil {
		t.Fatal("streaming tail item was not created")
	}
	streamH := lipgloss.Height(m.streamItem.Render(m.width - 1))

	finalH := lipgloss.Height(m.renderBlock(m.newBlockCard(blockAssistant, text)))

	if streamH != finalH {
		t.Fatalf("streaming tail height %d != finalized height %d (reflow on completion → bottom drift)", streamH, finalH)
	}
}

// BenchmarkEnsureStreamTail exercises the streaming-tail change-key hot path (§4
// E7) against a large in-flight message: each iteration grows the live buffer by
// one byte (a delta) and refreshes the tail. Keying on buffer LENGTH instead of a
// hash+copy of the whole buffer makes this O(1) with zero per-delta O(buffer)
// allocations regardless of how large the message has grown — ReportAllocs shows
// the win (the old content-hash path allocated a full []byte copy every delta).
func BenchmarkEnsureStreamTail(b *testing.B) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.streaming = true
	m.assistantBuf.WriteString(strings.Repeat("x", 100*1024)) // 100 KB in flight
	m.ensureStreamTail(false)                                 // prime m.streamItem
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.assistantBuf.WriteByte('y') // a streamed delta grows the buffer
		m.ensureStreamTail(false)
	}
}

// --- T1: event coalescing (lag) -------------------------------------------

// TestWaitForEventCoalescesBatch verifies a burst of already-buffered events is
// drained into ONE batch (collapsing N renders into one) rather than one message
// per event.
func TestWaitForEventCoalescesBatch(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	ch := make(chan session.Event, 16)
	for i := uint64(1); i <= 5; i++ {
		ch <- session.Event{Seq: i, Type: session.EventMessageDelta, Payload: jraw(`{"content":"x"}`)}
	}
	m.events = ch

	msg := m.waitForEvent()() // build the Cmd, then run its body synchronously
	batch, ok := msg.(tEventBatchMsg)
	if !ok {
		t.Fatalf("waitForEvent returned %T, want tEventBatchMsg", msg)
	}
	if len(batch) != 5 {
		t.Fatalf("batch len = %d, want 5 (all buffered events coalesced)", len(batch))
	}
}

// TestEventBatchAppliesAllDeltas verifies the batch handler applies every event
// in order (so the coalesced buffer is identical to one-event-per-Update).
func TestEventBatchAppliesAllDeltas(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 60, 24
	m.layout()
	m.Update(tEventMsg(session.Event{Seq: 1, Type: session.EventMessageStarted, Payload: jraw(`{}`)}))
	m.Update(tEventBatchMsg{
		{Seq: 2, Type: session.EventMessageDelta, Payload: jraw(`{"content":"Hello "}`)},
		{Seq: 3, Type: session.EventMessageDelta, Payload: jraw(`{"content":"world"}`)},
	})
	if got := m.assistantBuf.String(); got != "Hello world" {
		t.Fatalf("after batch, assistantBuf = %q, want %q", got, "Hello world")
	}
}

// --- T1: scrollbar mouse drag ---------------------------------------------

// TestScrollbarDragMapsToOffset verifies the drag hit-test: a left press/drag on
// the scrollbar column maps its row to a proportional scroll offset, and clicks
// off the bar / above the body fall through.
func TestScrollbarDragMapsToOffset(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 40, 12
	for i := 0; i < 40; i++ {
		m.appendBlock(blockInfo, fmt.Sprintf("line %d", i))
	}
	m.layout()
	m.syncItems()

	bodyH := m.body.Height()
	maxOffset := m.body.TotalHeight() - bodyH
	if maxOffset <= 0 {
		t.Fatalf("precondition: content must overflow the viewport (maxOffset=%d)", maxOffset)
	}
	bar := m.width - 1
	const bodyTop = 2

	if !m.scrollbarDragTo(bar, bodyTop) {
		t.Fatal("drag on the scrollbar top was not consumed")
	}
	if got := m.body.Offset(); got != 0 {
		t.Errorf("top-of-bar drag offset = %d, want 0", got)
	}

	if !m.scrollbarDragTo(bar, bodyTop+bodyH-1) {
		t.Fatal("drag on the scrollbar bottom was not consumed")
	}
	if got := m.body.Offset(); got != maxOffset {
		t.Errorf("bottom-of-bar drag offset = %d, want %d", got, maxOffset)
	}

	if m.scrollbarDragTo(bar-1, bodyTop+1) {
		t.Error("a column that isn't the scrollbar must not be consumed")
	}
	if m.scrollbarDragTo(bar, 0) {
		t.Error("a row above the body must not be consumed")
	}
}
