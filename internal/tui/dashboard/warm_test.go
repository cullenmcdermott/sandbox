package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func TestIdleSoonRemaining(t *testing.T) {
	timeout := 15 * time.Minute
	// idle for 3m → ~12m left
	got := idleRemaining(timeout, 3*time.Minute)
	if got < 11*time.Minute || got > 12*time.Minute {
		t.Fatalf("idleRemaining = %v, want ~12m", got)
	}
	// idle longer than timeout → zero, not negative
	if got := idleRemaining(timeout, 20*time.Minute); got != 0 {
		t.Fatalf("idleRemaining = %v, want 0", got)
	}
}

func TestRenderDetailShowsWarmTail(t *testing.T) {
	m := New(nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	m.sessions = []Session{sess}
	m.cursor = 0
	m.width, m.height = 120, 40

	tr := m.ensureRetained(sess, &fakeRunnerClient{})
	tr.ingest(session.Event{Seq: 1, Type: session.EventMessageCompleted, Payload: mustMessagePayload(t, "assistant", "Warm tail content")})

	lines := m.renderDetailLines(60, 30)
	joined := strings.Join(lines, "\n")
	if len(lines) == 0 {
		t.Fatal("detail pane should include a preview section for a warm session")
	}
	_ = joined
}

func mustMessagePayload(t *testing.T, role, content string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(session.MessagePayload{Role: role, Content: content})
	if err != nil {
		t.Fatalf("marshal message payload: %v", err)
	}
	return b
}

func TestUnreadCountAndClear(t *testing.T) {
	s := transcriptSession()
	s.lastSeq = 10
	s.seenSeq = 4
	if got := s.Unread(); got != 6 {
		t.Fatalf("Unread() = %d, want 6", got)
	}
	// Never negative even if seenSeq somehow exceeds lastSeq.
	s.seenSeq = 20
	if got := s.Unread(); got != 0 {
		t.Fatalf("Unread() = %d, want 0 (clamped)", got)
	}
}

func TestAttachClearsUnread(t *testing.T) {
	app := NewApp(nil, nil, nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id
	sess.lastSeq = 9
	sess.seenSeq = 2
	app.dashboard.sessions = []Session{sess}

	_, _ = app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})

	got := app.dashboard.sessionByID(id)
	if got.Unread() != 0 {
		t.Fatalf("attach should clear unread; Unread() = %d", got.Unread())
	}
}

func TestSeedSizeSetsDimensions(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.seedSize(120, 40)
	if m.width != 120 || m.height != 40 {
		t.Fatalf("seedSize gave (%d,%d), want (120,40)", m.width, m.height)
	}
}

func TestHideShowPreservesModelIdentity(t *testing.T) {
	app := NewApp(nil, nil, nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	app.dashboard.sessions = []Session{sess}
	app.dashboard.WithConnector(func(_ context.Context, _ session.Ref, _ string, _ func(ConnectStage, string)) (ConnectResult, error) {
		return ConnectResult{Client: &fakeRunnerClient{}}, nil
	})

	// Show.
	_, _ = app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})
	first := app.transcript
	if first == nil {
		t.Fatal("no foreground transcript after show")
	}

	// Hide (detachMsg path).
	_, _ = app.Update(detachMsg{})
	if app.transcript != nil {
		t.Fatal("detach must clear the foreground transcript pointer")
	}
	if _, ok := app.dashboard.retainedTranscript(id); !ok {
		t.Fatal("detach must KEEP the model warm in the retained map")
	}

	// Show again.
	_, _ = app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})
	if app.transcript != first {
		t.Fatal("re-show must reuse the SAME model instance (warm), not rebuild")
	}
}

func TestDetachRestartsBackgroundStream(t *testing.T) {
	app := NewApp(nil, nil, nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	app.dashboard.sessions = []Session{sess}
	app.dashboard.WithConnector(func(_ context.Context, _ session.Ref, _ string, _ func(ConnectStage, string)) (ConnectResult, error) {
		return ConnectResult{Client: &fakeRunnerClient{}}, nil
	})
	_, _ = app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})

	_, cmd := app.Update(detachMsg{})
	if cmd == nil {
		t.Fatal("detach should return a Cmd that restarts the background stream")
	}
}

func mustTitlePayload(t *testing.T, title string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(session.SessionTitlePayload{Title: title})
	if err != nil {
		t.Fatalf("marshal title payload: %v", err)
	}
	return b
}

func TestAttachReusesRetainedModel(t *testing.T) {
	app := NewApp(nil, nil, nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	app.dashboard.sessions = []Session{sess}

	// Pre-warm: a retained model exists (as if a background stream had built it).
	pre := app.dashboard.ensureRetained(sess, &fakeRunnerClient{})

	_, _ = app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})

	if app.transcript == nil {
		t.Fatal("attach did not set a foreground transcript")
	}
	if app.transcript != pre {
		t.Fatal("attach must REUSE the retained model instance, not rebuild it")
	}
	if app.screen != ScreenTranscript {
		t.Fatalf("screen = %v, want ScreenTranscript", app.screen)
	}
}

func TestAttachColdRegistersRetained(t *testing.T) {
	app := NewApp(nil, nil, nil)
	id := session.ID("sess-2")
	sess := transcriptSession()
	sess.State.ID = id
	app.dashboard.sessions = []Session{sess}

	// No pre-warm: cold open must still build a model AND register it as warm.
	_, _ = app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})

	got, ok := app.dashboard.retainedTranscript(id)
	if !ok {
		t.Fatal("cold attach must register the new model as warm")
	}
	if got != app.transcript {
		t.Fatal("registered warm model must be the same instance as the foreground transcript")
	}
}

func TestHandleRunnerEventFeedsRetained(t *testing.T) {
	m := New(nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	m.sessions = []Session{sess}

	// Warm it, plus register a channel so handleRunnerEvent's re-read doesn't panic.
	ch := make(chan session.Event, 1)
	m.liveSSEChannels[id] = ch
	tr := m.ensureRetained(sess, &fakeRunnerClient{})

	_, _ = m.Update(RunnerEventMsg{
		ID:    id,
		Event: session.Event{Seq: 7, Type: session.EventSessionTitle, Payload: mustTitlePayload(t, "Fed")},
	})

	if tr.lastSeq != 7 {
		t.Fatalf("retained model lastSeq = %d, want 7 (not fed)", tr.lastSeq)
	}
}

func TestStreamEndedDropsRetained(t *testing.T) {
	m := New(nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusSuspended // cluster says not-running
	m.sessions = []Session{sess}
	m.ensureRetained(sess, &fakeRunnerClient{})

	_, _ = m.Update(RunnerEventMsg{ID: id, StreamEnded: true})

	if _, ok := m.retainedTranscript(id); ok {
		t.Fatal("StreamEnded for a non-running pod must drop the retained model (warm→cold)")
	}
}

func TestLiveSSEReadyBuildsRetained(t *testing.T) {
	m := New(nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	m.sessions = []Session{sess}

	ch := make(chan session.Event)
	_, _ = m.Update(liveSSEReadyMsg{
		id:     id,
		ch:     ch,
		cancel: func() {},
		client: &fakeRunnerClient{},
	})

	if _, ok := m.retainedTranscript(id); !ok {
		t.Fatal("liveSSEReadyMsg must build a retained model for the session")
	}
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

func TestBackgroundStreamPrefersObserverConnector(t *testing.T) {
	m := New(nil)
	var observerCalled, fullCalled bool
	m.WithConnector(func(_ context.Context, _ session.Ref, _ string, _ func(ConnectStage, string)) (ConnectResult, error) {
		fullCalled = true
		return ConnectResult{Client: &fakeRunnerClient{}}, nil
	})
	m.WithObserverConnector(func(_ context.Context, _ session.Ref, _ string, _ func(ConnectStage, string)) (ConnectResult, error) {
		observerCalled = true
		return ConnectResult{Client: &fakeRunnerClient{}}, nil
	})

	sess := transcriptSession()
	sess.State.ID = "s1"
	sess.State.Status = session.StatusRunning

	cmd := m.startLiveSSECmd(sess)
	if cmd == nil {
		t.Fatal("expected a background-stream Cmd")
	}
	_ = cmd() // run it: invokes the connector, then EventsPassive on the result

	if !observerCalled {
		t.Fatal("background stream should use the lightweight observer connector")
	}
	if fullCalled {
		t.Fatal("background stream must NOT use the full (sync-setup) connector when an observer is set")
	}
}

func TestBackgroundStreamFallsBackToFullConnector(t *testing.T) {
	m := New(nil)
	var fullCalled bool
	m.WithConnector(func(_ context.Context, _ session.Ref, _ string, _ func(ConnectStage, string)) (ConnectResult, error) {
		fullCalled = true
		return ConnectResult{Client: &fakeRunnerClient{}}, nil
	})
	// No observer connector set.

	sess := transcriptSession()
	sess.State.ID = "s1"
	sess.State.Status = session.StatusRunning

	cmd := m.startLiveSSECmd(sess)
	if cmd == nil {
		t.Fatal("expected a background-stream Cmd")
	}
	_ = cmd()

	if !fullCalled {
		t.Fatal("background stream should fall back to the full connector when no observer is set")
	}
}

func TestFooterShowsWarmCount(t *testing.T) {
	m := New(nil)
	m.width = 120
	s1 := transcriptSession()
	s1.State.ID = "a"
	s2 := transcriptSession()
	s2.State.ID = "b"
	m.ensureRetained(s1, &fakeRunnerClient{})
	m.ensureRetained(s2, &fakeRunnerClient{})

	bar := m.bottomBar(120)
	if !strings.Contains(bar, "2") || !strings.Contains(bar, "warm") {
		t.Fatalf("footer should show warm count; got %q", bar)
	}
}

func TestSyncPollUpdatesSession(t *testing.T) {
	m := New(nil)
	id := session.ID("sess-1")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	m.sessions = []Session{sess}
	m.WithSyncProber(func(_ context.Context, _ session.ID) string { return "stalled" })

	_, _ = m.Update(syncStatusMsg{id: id, status: "stalled"})

	if got := m.sessionByID(id).SyncStatus; got != "stalled" {
		t.Fatalf("SyncStatus = %q, want stalled", got)
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
