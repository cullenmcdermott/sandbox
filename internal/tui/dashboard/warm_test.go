package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func mustTitlePayload(t *testing.T, title string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(session.SessionTitlePayload{Title: title})
	if err != nil {
		t.Fatalf("marshal title payload: %v", err)
	}
	return b
}

func TestIngestAppliesEventAndDedupes(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)

	ev := session.Event{Seq: 5, Type: session.EventSessionTitle, Payload: mustTitlePayload(t, "Hello")}
	m.ingest(ev)
	if m.lastSeq != 5 {
		t.Fatalf("lastSeq = %d after ingest, want 5", m.lastSeq)
	}

	// Re-ingesting a seq <= lastSeq is a no-op (dedup): lastSeq must not regress
	// and must not advance.
	m.ingest(session.Event{Seq: 3, Type: session.EventSessionTitle, Payload: mustTitlePayload(t, "Old")})
	if m.lastSeq != 5 {
		t.Fatalf("lastSeq = %d after stale ingest, want 5 (dedup)", m.lastSeq)
	}
}

func TestRetainedStoreLifecycle(t *testing.T) {
	m := New(nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id

	if _, ok := m.retainedTranscript(id); ok {
		t.Fatal("expected no retained model before ensureRetained")
	}

	t1 := m.ensureRetained(sess, &fakeRunnerClient{})
	if t1 == nil {
		t.Fatal("ensureRetained returned nil")
	}
	if got := m.warmCount(); got != 1 {
		t.Fatalf("warmCount = %d, want 1", got)
	}

	// ensureRetained is idempotent: same id returns the same instance.
	t2 := m.ensureRetained(sess, &fakeRunnerClient{})
	if t1 != t2 {
		t.Fatal("ensureRetained must return the existing instance for a known id")
	}
	if got := m.warmCount(); got != 1 {
		t.Fatalf("warmCount = %d after re-ensure, want 1", got)
	}

	got, ok := m.retainedTranscript(id)
	if !ok || got != t1 {
		t.Fatal("retainedTranscript did not return the stored instance")
	}

	m.dropRetained(id)
	if _, ok := m.retainedTranscript(id); ok {
		t.Fatal("dropRetained did not remove the model")
	}
	if got := m.warmCount(); got != 0 {
		t.Fatalf("warmCount = %d after drop, want 0", got)
	}
}
