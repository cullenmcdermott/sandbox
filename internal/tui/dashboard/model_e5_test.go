package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §4 E5: the passive SSE observer streams used to run one full Update+View
// pipeline PER event (liveSSENextCmd delivered one RunnerEventMsg and re-armed
// itself per message). liveSSEBatchCmd now blocks for the first event then
// non-blockingly drains a burst into ONE RunnerEventBatchMsg, so N buffered
// events cost ceil(N/cap) messages — and one Update+View each — instead of N.

// e5Model builds a Model with a single Running session plus a warm retained
// transcript, and a buffered channel + generation registered for it.
func e5Model(t *testing.T, id session.ID, gen uint64, buf int) (*Model, chan session.Event, *TranscriptModel) {
	t.Helper()
	m := New(nil)
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	sess.CtxLimit = 200_000
	m.sessions = []Session{sess}
	ch := make(chan session.Event, buf)
	m.liveSSEChannels[id] = ch
	m.liveSSEStreamGen[id] = gen
	tr := m.ensureRetained(sess, &fakeRunnerClient{})
	return m, ch, tr
}

// A burst of N (< cap) buffered events must be drained into a SINGLE
// RunnerEventBatchMsg carrying the whole burst — the coalescing that turns N
// render pipelines into one. This drives the Cmd body synchronously.
func TestLiveSSEBatchCoalescesBurst(t *testing.T) {
	m, ch, _ := e5Model(t, "s", 4, 16)
	_ = m
	const n = 5
	for i := uint64(1); i <= n; i++ {
		ch <- mkEventSeq(i, session.EventUsageUpdated, session.UsagePayload{InputTokens: int(i * 1000)})
	}

	msg := liveSSEBatchCmd("s", ch, 4)() // build the Cmd, run its body synchronously
	batch, ok := msg.(RunnerEventBatchMsg)
	if !ok {
		t.Fatalf("liveSSEBatchCmd returned %T, want RunnerEventBatchMsg", msg)
	}
	if batch.StreamEnded {
		t.Error("an open channel must not report StreamEnded")
	}
	if batch.gen != 4 {
		t.Errorf("batch gen = %d, want 4 (carried forward for the stale-stream guard)", batch.gen)
	}
	if len(batch.Events) != n {
		t.Fatalf("batch delivered %d events, want %d (whole burst coalesced into one message)", len(batch.Events), n)
	}
}

// The batch handler must apply EVERY event in the burst (identical per-event
// side effects to the single path): the read-model reflects the LAST event and
// the warm transcript received all of them — proving batching changed only the
// render granularity, not the reduction.
func TestLiveSSEBatchAppliesAllEvents(t *testing.T) {
	m, _, tr := e5Model(t, "s", 4, 0)
	const n = 6
	events := make([]session.Event, 0, n)
	for i := uint64(1); i <= n; i++ {
		events = append(events, mkEventSeq(i, session.EventUsageUpdated, session.UsagePayload{InputTokens: int(i * 1000)}))
	}

	_, cmd := m.handleRunnerEventBatch(RunnerEventBatchMsg{ID: "s", Events: events, gen: 4})

	if got := m.sessionByID("s").InputTokens; got != n*1000 {
		t.Errorf("read-model InputTokens = %d, want %d (last event applied)", got, n*1000)
	}
	if got := m.sessionByID("s").lastSeq; got != n {
		t.Errorf("read-model lastSeq = %d, want %d (resume cursor advanced through the whole batch)", got, n)
	}
	if tr.lastSeq != n {
		t.Errorf("warm transcript lastSeq = %d, want %d (all events fed to the retained model)", tr.lastSeq, n)
	}
	if cmd == nil {
		t.Error("an open-channel batch must re-arm the batch reader (non-nil Cmd)")
	}
}

// The drain is capped at eventBatchMax so a relentless stream still yields to the
// render/key loop between batches: a burst LARGER than the cap splits across
// multiple messages (≤ ceil(N/cap)), leaving the surplus buffered for the next
// read.
func TestLiveSSEBatchCapsDrainAtEventBatchMax(t *testing.T) {
	const extra = 3
	total := eventBatchMax + extra
	m, ch, _ := e5Model(t, "s", 4, total)
	_ = m
	for i := 0; i < total; i++ {
		ch <- mkEventSeq(uint64(i+1), session.EventUsageUpdated, session.UsagePayload{InputTokens: i})
	}

	msg := liveSSEBatchCmd("s", ch, 4)()
	batch := msg.(RunnerEventBatchMsg)
	if len(batch.Events) != eventBatchMax {
		t.Fatalf("first batch drained %d events, want the cap %d", len(batch.Events), eventBatchMax)
	}
	if batch.StreamEnded {
		t.Error("hitting the cap on an open channel must not report StreamEnded")
	}
	if got := len(ch); got != extra {
		t.Errorf("%d events left buffered, want %d (surplus deferred to the next batch)", got, extra)
	}
}

// If the channel closes mid-drain, the events read BEFORE the close must still be
// applied AND the stream-ended handling must run, in order — no drained event is
// lost. On a still-Running pod that means: read-model advanced through the events
// + stream torn down + a reconnect scheduled.
func TestLiveSSEBatchCloseMidDrainAppliesThenEnds(t *testing.T) {
	m, ch, _ := e5Model(t, "s", 4, 8)
	const n = 3
	for i := uint64(1); i <= n; i++ {
		ch <- mkEventSeq(i, session.EventUsageUpdated, session.UsagePayload{InputTokens: int(i * 1000)})
	}
	close(ch)

	msg := liveSSEBatchCmd("s", ch, 4)()
	batch := msg.(RunnerEventBatchMsg)
	if !batch.StreamEnded {
		t.Error("a closed channel must report StreamEnded")
	}
	if len(batch.Events) != n {
		t.Fatalf("close-mid-drain delivered %d events, want %d (events read before close not lost)", len(batch.Events), n)
	}

	_, cmd := m.handleRunnerEventBatch(batch)
	if got := m.sessionByID("s").InputTokens; got != n*1000 {
		t.Errorf("read-model InputTokens = %d, want %d (drained events applied before the end handling)", got, n*1000)
	}
	if got := m.sessionByID("s").lastSeq; got != n {
		t.Errorf("read-model lastSeq = %d, want %d", got, n)
	}
	if _, ok := m.liveSSEChannels["s"]; ok {
		t.Error("StreamEnded must tear down the live channel")
	}
	if cmd == nil {
		t.Error("StreamEnded on a still-Running pod must schedule a reconnect (non-nil Cmd)")
	}
}

// A stale-generation batch (from a superseded/orphaned connect) must be ignored
// WHOLE — no event applied and no teardown — via the single guard that gates the
// batch (one channel = one generation).
func TestLiveSSEBatchStaleGenIgnored(t *testing.T) {
	m, _, tr := e5Model(t, "s", 7, 0) // healthy stream is generation 7
	events := []session.Event{
		mkEventSeq(1, session.EventUsageUpdated, session.UsagePayload{InputTokens: 99_000}),
		mkEventSeq(2, session.EventUsageUpdated, session.UsagePayload{InputTokens: 88_000}),
	}

	// A stale gen-3 batch — even one that claims StreamEnded — must be a no-op.
	_, cmd := m.handleRunnerEventBatch(RunnerEventBatchMsg{ID: "s", Events: events, StreamEnded: true, gen: 3})

	if got := m.sessionByID("s").InputTokens; got != 0 {
		t.Errorf("stale-generation batch applied events (InputTokens=%d), want fully ignored", got)
	}
	if tr.lastSeq != 0 {
		t.Errorf("stale-generation batch fed the warm transcript (lastSeq=%d), want ignored", tr.lastSeq)
	}
	if _, ok := m.liveSSEChannels["s"]; !ok {
		t.Error("a stale StreamEnded must NOT tear down the healthy stream")
	}
	if cmd != nil {
		t.Error("a stale batch must return no Cmd")
	}
}
