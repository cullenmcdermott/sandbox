package dashboard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Fix B: a cold cache replay must reconcile the list exactly once, not once per
// event. The per-event reconcile re-fingerprints every prior item (hashing each
// block's full text) and rebuilds the list, so a naive replay is O(N^2). The
// reconcile counter is the hard-to-fake behavioral oracle: with K cached events
// it must stay at 1.
func TestBulkReplayIsSingleReconcile(t *testing.T) {
	const K = 300
	evs := make([]session.Event, 0, K)
	for i := 0; i < K; i++ {
		evs = append(evs, session.Event{
			Seq:     uint64(i + 1),
			Type:    session.EventMessageCompleted,
			Payload: jraw(fmt.Sprintf(`{"role":"assistant","content":"block %d"}`, i)),
		})
	}

	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.cache = &fakeEventCache{loaded: evs}

	before := m.reconciles
	m.loadCachedTranscript()

	if m.bulkReplay {
		t.Error("bulkReplay must be cleared after replay")
	}
	if got := m.reconciles - before; got != 1 {
		t.Fatalf("replaying %d cached events caused %d reconciles, want exactly 1 (O(N), not O(N^2))", K, got)
	}
	if len(m.blocks) != K {
		t.Fatalf("rebuilt %d blocks, want %d", len(m.blocks), K)
	}

	// Correctness: the final reconcile must still produce a renderable, bottom-pinned
	// transcript with the replayed content present.
	m.layout()
	out := stripANSI(m.body.Render())
	if !strings.Contains(out, fmt.Sprintf("block %d", K-1)) {
		t.Errorf("bulk replay lost the last block:\n%s", out)
	}
	if m.lastSeq != K {
		t.Errorf("lastSeq after replay = %d, want %d (resume from cached head)", m.lastSeq, K)
	}
}
