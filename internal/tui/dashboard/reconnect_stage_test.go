package dashboard

import (
	"context"
	"strings"
	"testing"
)

// FU1: doReconnect must stream the connector's stage updates into the stage
// channel and close it when the attempt finishes.
func TestDoReconnectStreamsStages(t *testing.T) {
	invoked := false
	reconnect := func(ctx context.Context, onStage func(ConnectStage, string)) (RunnerClient, error) {
		onStage(StageResume, "")
		onStage(StageRunner, "")
		invoked = true
		return &fakeRunnerClient{}, nil
	}
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), reconnect)
	m.reconnectStages = make(chan reconnectStageMsg, 8)

	msg := m.doReconnect()() // build the Cmd, then run its body synchronously
	if !invoked {
		t.Fatal("reconnect callback was not invoked")
	}
	if _, ok := msg.(tReconnectedMsg); !ok {
		t.Fatalf("expected tReconnectedMsg, got %T", msg)
	}

	// The stages were buffered, and the channel is closed (range terminates).
	var got []ConnectStage
	for sm := range m.reconnectStages {
		got = append(got, sm.stage)
	}
	if len(got) != 2 || got[0] != StageResume || got[1] != StageRunner {
		t.Errorf("expected [Resume, Runner] buffered, got %v", got)
	}
}

// FU1: a live stage update flips the header from a flat label to the named
// stage; the done sentinel stops draining; a successful reconnect clears it.
func TestReconnectStageDrivesHeader(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), noopReconnect)
	m.width, m.height = 80, 24
	m.reconnecting = true
	m.reconnectStartedAt = nowFunc()

	if h := stripANSI(m.renderHeader()); !strings.Contains(h, "reconnecting") {
		t.Fatalf("expected a reconnecting label before any stage, got %q", h)
	}

	m.Update(reconnectStageMsg{stage: StageResume})
	if !m.reconnectStageKnown {
		t.Fatal("a stage update should mark the stage known")
	}
	if h := stripANSI(m.renderHeader()); !strings.Contains(h, connectStageLabel(StageResume)) {
		t.Errorf("header should show the live stage %q, got %q", connectStageLabel(StageResume), h)
	}

	// The done sentinel must not re-subscribe.
	if _, cmd := m.Update(reconnectStageMsg{done: true}); cmd != nil {
		t.Error("done stage message should not schedule another drain")
	}

	// A successful reconnect clears the live-stage state.
	m.Update(tReconnectedMsg{client: &fakeRunnerClient{}})
	if m.reconnectStageKnown {
		t.Error("reconnect success should clear the live-stage readout")
	}
}
