package dashboard

import "testing"

// Fix C: an ungraceful stream drop can leave turnActive set with no event to
// clear it. The work-tick loop must NOT keep re-firing while reconnecting, or it
// spins a full-screen repaint every 150ms forever and starves keystrokes.
func TestWorkTickStopsWhileReconnecting(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.turnActive = true
	m.working = true
	m.reconnecting = true

	_, cmd := m.Update(workTickMsg{})
	if cmd != nil {
		t.Fatal("work-tick loop must not re-fire while reconnecting")
	}
	if m.working {
		t.Error("working must clear when the tick loop stops")
	}
}

// The loop must keep running for a genuinely live turn so the spinner/clock
// still animate.
func TestWorkTickContinuesWhileLive(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.turnActive = true
	m.working = true
	m.reconnecting = false

	_, cmd := m.Update(workTickMsg{})
	if cmd == nil {
		t.Fatal("work-tick loop must keep running for a live turn")
	}
}
