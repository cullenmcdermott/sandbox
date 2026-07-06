package dashboard

import (
	"context"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// A late background-sync advisory (§5: Connect no longer waits for the flush /
// config syncs / reaper) must land in the session's transcript wherever it
// lives — attached or retained-warm — exactly like a connect-time warning.
func TestSyncAdvisoryAppendsToRetainedTranscript(t *testing.T) {
	app := NewApp(nil, nil, nil)
	id := session.ID("sess-adv")
	sess := transcriptSession()
	sess.State.ID = id
	app.dashboard.sessions = []Session{sess}

	tr := NewTranscript(nil, sess, nil)
	app.dashboard.putRetained(id, tr)
	before := len(tr.blocks)

	app.Update(syncAdvisoryMsg{id: id, warning: "reaper ensure failed: boom"})
	if len(tr.blocks) != before+1 {
		t.Fatalf("advisory must append one block, got %d -> %d", before, len(tr.blocks))
	}
	got := tr.blocks[len(tr.blocks)-1]
	if got.kind != blockInfo {
		t.Errorf("advisory block kind = %v, want blockInfo", got.kind)
	}
	if want := "⚠ reaper ensure failed: boom"; got.text != want {
		t.Errorf("advisory text = %q, want %q", got.text, want)
	}

	// Unknown session or empty warning: no-op, no panic.
	app.Update(syncAdvisoryMsg{id: "nope", warning: "x"})
	app.Update(syncAdvisoryMsg{id: id, warning: ""})
	if len(tr.blocks) != before+1 {
		t.Errorf("no-op advisories must not append blocks")
	}
}

// The attachReadyMsg path polls awaitWarning once and converts a non-empty
// advisory into a syncAdvisoryMsg addressed to the attached session.
func TestAttachReadySpawnsAdvisoryPoll(t *testing.T) {
	app := NewApp(nil, nil, nil)
	sess := transcriptSession()
	app.dashboard.sessions = []Session{sess}

	aw := func(ctx context.Context) (string, error) { return "sync warning", nil }
	_, cmd := app.Update(attachReadyMsg{sess: sess, awaitWarning: aw})
	if cmd == nil {
		t.Fatal("attachReadyMsg must return a command batch")
	}
	for _, msg := range collectLeafMsgs(cmd) {
		if adv, ok := msg.(syncAdvisoryMsg); ok {
			if adv.warning != "sync warning" || adv.id != sess.ID() {
				t.Errorf("advisory poll produced %+v", adv)
			}
			return
		}
	}
	t.Fatal("attachReadyMsg with awaitWarning produced no syncAdvisoryMsg")
}
