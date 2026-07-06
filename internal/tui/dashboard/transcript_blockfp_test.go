package dashboard

import (
	"fmt"
	"testing"
)

// Appending a block to a long transcript must reprocess only the new block, not
// every prior (committed) one. In the unified representation each card owns its
// version, and a plain append bumps nothing on the existing cards — so committed
// blocks keep a stable version (a tui/list cache hit) no matter how long the
// transcript grows. (Previously reconcileItems re-hashed blockFP for all M items
// on every append — O(M) per event, the live-append cost behind sluggishness.)
func TestReconcileMemoizesImmutableBlocks(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200

	const M = 200
	for i := 0; i < M; i++ {
		m.appendBlock(blockAssistant, fmt.Sprintf("msg %d", i))
	}

	// Snapshot every committed card's version, append one more, and assert none of
	// the prior cards bumped — the new card is the only thing reprocessed.
	before := make([]uint64, len(m.blocks))
	for i, b := range m.blocks {
		before[i] = b.Version()
	}
	m.appendBlock(blockAssistant, "newest")
	for i := range before {
		if got := m.blocks[i].Version(); got != before[i] {
			t.Fatalf("appending one block bumped prior block %d (version %d→%d) — committed blocks not memoized", i, before[i], got)
		}
	}

	// The committed blocks must all sit at version 0: they were never mutated after
	// creation, so no bump ever fired (linear seeding, not quadratic re-hashing).
	for i := 0; i < M; i++ {
		if got := m.blocks[i].Version(); got != 0 {
			t.Fatalf("committed block %d has version %d, want 0 (was reprocessed after creation)", i, got)
		}
	}
}

// A mutable card (tool) must still re-render when its status flips, even though
// committed neighbors are memoized.
func TestReconcileStillUpdatesMutableToolCard(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.appendBlock(blockAssistant, "before the tool")
	m.startToolCard("Bash", "go test")

	// The running card's version before completion.
	toolIdx := len(m.blocks) - 1
	verBefore := m.blocks[toolIdx].Version()

	m.finishToolCard(toolOK, "exit 0", "Bash")
	if m.blocks[toolIdx].Version() == verBefore {
		t.Fatal("tool card version did not bump on completion — mutable card not re-rendered")
	}
}
