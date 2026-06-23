package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

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
