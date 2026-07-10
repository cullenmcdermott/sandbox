package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// toolDelta drives one tool.delta (input_json_delta) fragment through the reducer,
// exactly as the runner emits it while the model types a tool's input.
func toolDelta(m *TranscriptModel, frag string) {
	m.handleEvent(session.Event{Type: session.EventToolDelta,
		Payload: json.RawMessage(`{"partialJson":` + mustJSONString(frag) + `}`)})
}

func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// newestToolCard returns the most-recently-started (pending) tool card, the one
// tool.delta targets.
func newestToolCard(t *testing.T, m *TranscriptModel) *toolCard {
	t.Helper()
	if len(m.pendingTools) == 0 {
		t.Fatal("no pending tool card")
	}
	idx := m.pendingTools[len(m.pendingTools)-1]
	if idx < 0 || idx >= len(m.blocks) || m.blocks[idx].tool == nil {
		t.Fatal("pending tool index does not point at a tool card")
	}
	return m.blocks[idx].tool
}

// D6: an id-attributed tool.delta targets the exact card, not the newest
// pending one; a parented (subagent-child) delta whose id has no flat card is
// dropped rather than animating onto a main-thread card's argument.
func TestToolDeltaTargetsCardByID(t *testing.T) {
	m := d1Model(t)
	toolStart(m, "Bash", "tu_a")
	toolStart(m, "Write", "tu_b") // newest pending — the pre-D6 fallback target
	cards := toolCards(m)
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(cards))
	}
	a, b := cards[0], cards[1]

	m.handleEvent(session.Event{Type: session.EventToolDelta,
		Payload: json.RawMessage(`{"partialJson":"{\"command\":\"make test\"}","toolUseId":"tu_a"}`)})
	if !strings.Contains(a.arg, "make test") {
		t.Errorf("id-attributed delta missed its card: arg=%q", a.arg)
	}
	if strings.Contains(b.arg, "make test") {
		t.Errorf("delta leaked onto the newest pending card: arg=%q", b.arg)
	}

	// A subagent child's delta: id unknown to flatTools, parent set — dropped
	// whether or not the child's own id rides along.
	m.handleEvent(session.Event{Type: session.EventToolDelta,
		Payload: json.RawMessage(`{"partialJson":"{\"file_path\":\"/child.go\"}","toolUseId":"tu_child","parentToolUseId":"task_1"}`)})
	m.handleEvent(session.Event{Type: session.EventToolDelta,
		Payload: json.RawMessage(`{"partialJson":"{\"file_path\":\"/child.go\"}","parentToolUseId":"task_1"}`)})
	for _, c := range []*toolCard{a, b} {
		if strings.Contains(c.arg, "child.go") {
			t.Errorf("subagent-child delta animated onto a main-thread card (%s): arg=%q", c.tool, c.arg)
		}
	}
}

// E1 behavior pin: streaming several small tool.delta fragments onto a running
// card still materializes a non-empty live arg preview, and the accumulation
// buffer holds the full concatenated JSON (the path the finalized content reads).
func TestToolDeltaSmallInputPreviewsArgAndAccumulates(t *testing.T) {
	m := d1Model(t)
	toolStart(m, "Write", "tu_1")
	c := newestToolCard(t, m)

	// Fragments that together form a valid Write input with a file_path.
	frags := []string{`{"file_pa`, `th":"/proj/`, `pkg/main.go"`, `,"content":"hi`, `there"}`}
	var want strings.Builder
	for _, f := range frags {
		want.WriteString(f)
		toolDelta(m, f)
	}

	if c.arg == "" {
		t.Fatal("small streamed input produced an empty live arg preview")
	}
	// toolArg(Write) previews the (shortened) file_path.
	if !strings.Contains(c.arg, "main.go") {
		t.Errorf("arg preview = %q, want it to contain the streamed file_path", c.arg)
	}
	// The buffer must hold the full JSON — the source the finalized/expanded
	// content path relies on, not just the last fragment.
	if got := c.rawBuf.String(); got != want.String() {
		t.Errorf("rawBuf accumulation = %q, want full concat %q", got, want.String())
	}
}

// E1 cost pin (preview parse): a large streamed input must NOT parse the whole
// buffer on every delta. With the eager<2KB-then-every-+2KB throttle, 200
// 100-byte fragments deterministically trigger 28 full-buffer parses (20 in the
// <2KB eager window + 8 at each +2KB step) — bounded far below one-per-delta.
func TestToolDeltaLargeInputThrottlesArgExtracts(t *testing.T) {
	m := d1Model(t)
	toolStart(m, "Bash", "tu_1")

	const (
		nDeltas  = 200
		fragSize = 100
	)
	frag := strings.Repeat("x", fragSize)

	before := m.argExtracts
	for i := 0; i < nDeltas; i++ {
		toolDelta(m, frag)
	}
	extracts := m.argExtracts - before

	if extracts >= nDeltas {
		t.Fatalf("argExtracts = %d for %d deltas — extraction not throttled (O(N²) parse cost)", extracts, nDeltas)
	}
	if extracts > 30 {
		t.Errorf("argExtracts = %d, want <= 30 (throttle bound; deterministic count is 28)", extracts)
	}
}

// E1 cost pin (syncItems): a tool.delta must refresh the card in place via Bump
// alone — it must NOT rebuild the whole list item set (commitItems) per delta.
// reconciles counts commitItems() calls; deltas must add zero. The card version
// must still advance so the (already-registered) card re-renders — the list
// cache is keyed on (item, version), mirroring streamDelta.
func TestToolDeltaDoesNotReconcilePerDelta(t *testing.T) {
	m := d1Model(t)
	toolStart(m, "Write", "tu_1")
	idx := m.pendingTools[len(m.pendingTools)-1]

	reconcilesBefore := m.reconciles
	verBefore := m.blocks[idx].Version()

	for _, f := range []string{`{"file_pa`, `th":"/a/b.go"`, `}`} {
		toolDelta(m, f)
	}

	if got := m.reconciles - reconcilesBefore; got != 0 {
		t.Errorf("streaming deltas caused %d list rebuilds, want 0 (Bump-in-place, no syncItems)", got)
	}
	if m.blocks[idx].Version() == verBefore {
		t.Error("tool card version did not advance across deltas — the card would not re-render")
	}
}
