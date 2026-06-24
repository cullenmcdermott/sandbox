package dashboard

import (
	"context"
	"testing"
	"time"
)

// M37: the transcript model is reused across an auto-reconnect, so a pending
// permission box keeps its original `since`. After a multi-second drop that is
// already past permissionGraceCap, so a key held during the drop would instantly
// answer the box as it becomes live again. Reconnect must re-anchor the grace.
func TestPermissionGraceReanchorsOnReconnect(t *testing.T) {
	reconnect := func(context.Context, func(ConnectStage, string)) (RunnerClient, error) {
		return &fakeRunnerClient{}, nil
	}
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), reconnect)
	m.width, m.height = 80, 24
	m.layout()

	// A permission box that appeared 10s ago — well past the grace cap.
	m.pending = &transcriptPermission{id: "p1", tool: "Edit", since: time.Now().Add(-10 * time.Second)}
	if !m.permissionAnswerable(time.Now().Add(-10 * time.Second)) {
		t.Fatal("precondition: a long-pending box should be answerable")
	}

	ret, _ := m.Update(tReconnectedMsg{client: &fakeRunnerClient{}})
	tm, ok := ret.(*TranscriptModel)
	if !ok {
		t.Fatalf("Update returned %T, want *TranscriptModel", ret)
	}
	if tm.pending == nil {
		t.Fatal("pending permission should survive reconnect")
	}
	if tm.permissionAnswerable(time.Now()) {
		t.Error("permission must NOT be answerable immediately after reconnect (M37 regression)")
	}
}
