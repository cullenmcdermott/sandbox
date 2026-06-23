package dashboard

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// warmSoftLimit is the advisory ceiling on simultaneously warm sessions. It is
// NOT enforced in v1 — exceeding it only emits a log-warn (see maybeWarnWarm).
// It exists as the single tunable for adding LRU eviction later if N grows.
const warmSoftLimit = 12

// ensureRetained returns the retained TranscriptModel for sess, building a
// background (headless) model fed via handleRunnerEvent if none exists yet. The
// returned model has NOT started its own SSE stream; while warm it is fed by the
// dashboard's passive background stream. client is the live runner client for
// the session's pod. ensureRetained is idempotent.
func (m *Model) ensureRetained(sess Session, client RunnerClient) *TranscriptModel {
	if m.retained == nil {
		m.retained = make(map[session.ID]*TranscriptModel)
	}
	id := sess.ID()
	if t, ok := m.retained[id]; ok {
		return t
	}
	// reconnect is nil for background models: they never run their own stream, so
	// they never reconnect. The foreground promotion path (Phase 2) installs the
	// real client + reconnect before starting the active stream.
	t := NewTranscript(client, sess, nil)
	t.caps = m.caps
	m.retained[id] = t
	return t
}

// retainedTranscript returns the warm model for id, if any.
func (m *Model) retainedTranscript(id session.ID) (*TranscriptModel, bool) {
	t, ok := m.retained[id]
	return t, ok
}

// putRetained stores an externally-built model (the foreground attach path uses
// this so a cold-opened session joins the warm set).
func (m *Model) putRetained(id session.ID, t *TranscriptModel) {
	if m.retained == nil {
		m.retained = make(map[session.ID]*TranscriptModel)
	}
	m.retained[id] = t
}

// dropRetained removes the warm model for id (warm→cold). Called when a pod
// suspends, is deleted, or its stream is exhausted.
func (m *Model) dropRetained(id session.ID) {
	delete(m.retained, id)
}

// warmCount is the number of warm (retained) sessions. Surfaced in the footer
// and logged; tracked, not enforced.
func (m *Model) warmCount() int { return len(m.retained) }

// maybeWarnWarm logs when the warm set exceeds the soft limit. v1 does not
// evict; this is the observability hook for a future cap.
func (m *Model) maybeWarnWarm() {
	if len(m.retained) > warmSoftLimit {
		slog.Warn("warm session set exceeds soft limit",
			"warm", len(m.retained), "softLimit", warmSoftLimit)
	}
}

// idleRemaining returns how long until the reaper suspends a session that has
// been idle for `idleFor`, clamped to [0, timeout]. Zero means "due now".
func idleRemaining(timeout, idleFor time.Duration) time.Duration {
	if timeout <= 0 {
		return 0
	}
	rem := timeout - idleFor
	if rem < 0 {
		return 0
	}
	return rem
}

// roundDur is a compact, whole-unit duration string for the idle-soon hint
// ("45s", "12m") — precision the user doesn't need in a passive indicator.
func roundDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
