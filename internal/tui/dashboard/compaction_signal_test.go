package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §2b gap 4 "No compaction signal": when the SDK compacts (summarizes) the
// conversation to fit the context window it emits a context.compacted event.
// Both consumers of the ctx% baseline — the list-row reducer (ApplyRunnerEvent)
// and the transcript header (handleEvent) — must reset the token baseline to the
// post-compaction size so the gauge stops reporting the stale pre-compaction
// count, and the transcript must drop a scrollback marker.

// The reducer resets the ctx% baseline to PostTokens (cache counters zeroed) so
// the list row's ctx% gauge reflects the compacted conversation, and it does not
// change the six-state status.
func TestContextCompacted_ReducerResetsCtxBaseline(t *testing.T) {
	sess := makeSession("sess-compact", StatusBusy)
	sess.CtxLimit = 200_000
	// A near-full pre-compaction baseline: input + both cache buckets.
	sess.InputTokens = 40_000
	sess.CacheReadTokens = 120_000
	sess.CacheWriteTokens = 20_000
	if pct := sess.CtxPercent(); pct != 90 {
		t.Fatalf("precondition: expected 90%% ctx before compaction, got %d", pct)
	}

	changed := ApplyRunnerEvent(&sess, mkEvent(session.EventContextCompacted, session.ContextCompactedPayload{
		Trigger:    "auto",
		PreTokens:  180_000,
		PostTokens: 30_000,
	}))

	if changed {
		t.Error("context.compacted must not change the six-state status")
	}
	if sess.DashStatus != StatusBusy {
		t.Errorf("status must stay busy, got %v", sess.DashStatus)
	}
	if sess.InputTokens != 30_000 {
		t.Errorf("InputTokens must reset to PostTokens (30000), got %d", sess.InputTokens)
	}
	if sess.CacheReadTokens != 0 || sess.CacheWriteTokens != 0 {
		t.Errorf("cache counters must zero on compaction, got read=%d write=%d",
			sess.CacheReadTokens, sess.CacheWriteTokens)
	}
	if pct := sess.CtxPercent(); pct != 15 {
		t.Errorf("ctx%% must drop to the post-compaction size (15%%), got %d", pct)
	}
}

// When PostTokens is absent (0) on the wire the reducer must leave the counters
// untouched — a partial event must not blank a known baseline to zero.
func TestContextCompacted_ReducerNoPostTokensPreservesBaseline(t *testing.T) {
	sess := makeSession("sess-compact", StatusBusy)
	sess.CtxLimit = 200_000
	sess.InputTokens = 40_000
	sess.CacheReadTokens = 120_000
	sess.CacheWriteTokens = 20_000

	ApplyRunnerEvent(&sess, mkEvent(session.EventContextCompacted, session.ContextCompactedPayload{
		Trigger:   "manual",
		PreTokens: 180_000,
		// PostTokens omitted.
	}))

	if sess.InputTokens != 40_000 || sess.CacheReadTokens != 120_000 || sess.CacheWriteTokens != 20_000 {
		t.Errorf("absent PostTokens must not touch counters, got in=%d read=%d write=%d",
			sess.InputTokens, sess.CacheReadTokens, sess.CacheWriteTokens)
	}
}

// The transcript resets its header token baseline to PostTokens and drops a
// one-line "context compacted · N→M tokens" marker into scrollback.
func TestContextCompacted_TranscriptResetsAndMarks(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.InputTokens = 40_000
	m.CacheReadTokens = 120_000
	m.CacheWriteTokens = 20_000

	m.handleEvent(mkEvent(session.EventContextCompacted, session.ContextCompactedPayload{
		Trigger:    "auto",
		PreTokens:  180_000,
		PostTokens: 30_000,
	}))

	if m.InputTokens != 30_000 || m.CacheReadTokens != 0 || m.CacheWriteTokens != 0 {
		t.Errorf("transcript baseline must reset to PostTokens with cache zeroed, got in=%d read=%d write=%d",
			m.InputTokens, m.CacheReadTokens, m.CacheWriteTokens)
	}
	got, ok := lastBlockOfKind(m, blockInfo)
	if !ok {
		t.Fatal("compaction must append an info marker to scrollback")
	}
	if want := "context compacted · 180k→30k tokens"; got != want {
		t.Errorf("marker text = %q, want %q", got, want)
	}
}

// The PostTokens-only branch (PreTokens absent): the SDK types pre_tokens as
// required so this is defensive, but the marker must still format from PostTokens
// and the baseline must reset — guards the third arm of the marker switch.
func TestContextCompacted_TranscriptPostTokensOnlyMarks(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.InputTokens = 40_000
	m.CacheReadTokens = 120_000
	m.CacheWriteTokens = 20_000

	m.handleEvent(mkEvent(session.EventContextCompacted, session.ContextCompactedPayload{
		Trigger:    "auto",
		PostTokens: 30_000,
		// PreTokens omitted.
	}))

	if m.InputTokens != 30_000 || m.CacheReadTokens != 0 || m.CacheWriteTokens != 0 {
		t.Errorf("PostTokens-only must still reset baseline, got in=%d read=%d write=%d",
			m.InputTokens, m.CacheReadTokens, m.CacheWriteTokens)
	}
	got, ok := lastBlockOfKind(m, blockInfo)
	if !ok {
		t.Fatal("compaction must append an info marker")
	}
	if want := "context compacted · 30k tokens"; got != want {
		t.Errorf("marker text = %q, want %q", got, want)
	}
}

// A compaction event with no PostTokens still drops a marker (so the run's
// compaction stays visible) but must not touch the header token baseline.
func TestContextCompacted_TranscriptNoPostTokensStillMarks(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.InputTokens = 40_000
	m.CacheReadTokens = 120_000
	m.CacheWriteTokens = 20_000

	m.handleEvent(mkEvent(session.EventContextCompacted, session.ContextCompactedPayload{
		Trigger:   "manual",
		PreTokens: 180_000,
	}))

	if m.InputTokens != 40_000 || m.CacheReadTokens != 120_000 || m.CacheWriteTokens != 20_000 {
		t.Errorf("absent PostTokens must not touch header baseline, got in=%d read=%d write=%d",
			m.InputTokens, m.CacheReadTokens, m.CacheWriteTokens)
	}
	got, ok := lastBlockOfKind(m, blockInfo)
	if !ok {
		t.Fatal("compaction must append an info marker even without PostTokens")
	}
	if want := "context compacted · 180k tokens"; got != want {
		t.Errorf("marker text = %q, want %q", got, want)
	}
}
