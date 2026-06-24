package dashboard

import (
	"fmt"
	"testing"
)

// Fix F-blockFP: appending a block to a long transcript must fingerprint only
// the new block, not re-hash every prior (immutable) block. Before memoization,
// reconcileItems recomputed blockFP for all M items on every append — O(M) full
// re-hashes per event, the live-append cost behind general sluggishness.
func TestReconcileMemoizesImmutableBlocks(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200

	const M = 200
	for i := 0; i < M; i++ {
		m.appendBlock(blockAssistant, fmt.Sprintf("msg %d", i))
	}

	// One more append should recompute exactly one fingerprint (the new block).
	before := m.fpComputes
	m.appendBlock(blockAssistant, "newest")
	if got := m.fpComputes - before; got != 1 {
		t.Fatalf("appending one block recomputed %d fingerprints, want 1 (immutable blocks memoized)", got)
	}

	// Seeding all M blocks must also have been linear, not quadratic: each block
	// is fingerprinted once as it is appended.
	if m.fpComputes > M+1 {
		t.Fatalf("seeding %d blocks took %d fingerprint computes, want <= %d (linear)", M, m.fpComputes, M+1)
	}
}

// A mutable card (tool) must still re-render when its status flips, even though
// immutable neighbors are memoized.
func TestReconcileStillUpdatesMutableToolCard(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.appendBlock(blockAssistant, "before the tool")
	m.startToolCard("Bash", "go test")

	// The running card's item version before completion.
	toolIdx := len(m.blocks) - 1
	verBefore := m.items[toolIdx].Version()

	m.finishToolCard(toolOK, "exit 0", "Bash")
	if m.items[toolIdx].Version() == verBefore {
		t.Fatal("tool card version did not bump on completion — mutable card not re-fingerprinted")
	}
}
