package dashboard

import "testing"

func TestTranscriptStartsInNormalMode(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	if m.imode != modeNormal {
		t.Fatalf("new transcript imode = %v, want modeNormal", m.imode)
	}
}

func TestNormalIEntersInsert(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.handleKey(keyMsg("i"))
	if m.imode != modeInsert {
		t.Fatalf("after 'i', imode = %v, want modeInsert", m.imode)
	}
}

func TestInsertEscReturnsToNormal(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.imode = modeInsert
	m.handleKey(keyMsg("esc"))
	if m.imode != modeNormal {
		t.Fatalf("after esc in INSERT, imode = %v, want modeNormal", m.imode)
	}
}

func TestEscInterruptsActiveTurn(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.imode = modeInsert
	m.turnActive = true
	_, cmd := m.handleKey(keyMsg("esc"))
	if cmd == nil {
		t.Fatal("esc during active turn produced no command")
	}
	cmd() // executes the InterruptTurn call
	if fc.interrupts != 1 {
		t.Fatalf("esc during active turn: interrupts = %d, want 1", fc.interrupts)
	}
	// Interrupting must not also drop out of INSERT mode — esc only interrupts.
	if m.imode != modeInsert {
		t.Fatalf("esc-interrupt changed imode to %v, want modeInsert", m.imode)
	}
}

func TestEscSteerTakesPriorityOverInterrupt(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.turnActive = true
	m.queuedPrompt = "steer me"
	_, cmd := m.handleKey(keyMsg("esc"))
	if cmd == nil {
		t.Fatal("esc with a queued prompt produced no command")
	}
	// queueSteer (interrupt + inject) consumes the queued prompt; a plain
	// interrupt would leave it intact. The consumed prompt proves steer won.
	if m.queuedPrompt != "" {
		t.Fatalf("esc steer left queuedPrompt = %q, want consumed", m.queuedPrompt)
	}
}

func TestEscapeConsumesDuringActiveTurn(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	// NORMAL mode, no overlay: idle esc would detach, but an active turn must be
	// consumed so the App delegates esc to the transcript to interrupt it.
	if m.escapeConsumes() {
		t.Fatal("precondition: idle NORMAL should not consume esc")
	}
	m.turnActive = true
	if !m.escapeConsumes() {
		t.Error("active turn should consume esc (interrupt, not detach)")
	}
}

func TestNormalSwallowsTyping(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	// A stray letter in NORMAL must not leak into the blurred prompt.
	m.handleKey(keyMsg("z"))
	if got := m.input.Value(); got != "" {
		t.Fatalf("NORMAL leaked typing into prompt: %q", got)
	}
}

func TestNormalQDetaches(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	_, cmd := m.handleKey(keyMsg("q"))
	if cmd == nil {
		t.Fatal("'q' in NORMAL produced no command")
	}
	if _, ok := cmd().(detachMsg); !ok {
		t.Fatalf("'q' did not emit detachMsg")
	}
}

func TestEscapeConsumesInInsertOnly(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	if m.escapeConsumes() {
		t.Error("NORMAL with no overlay should not consume esc (App detaches)")
	}
	m.imode = modeInsert
	if !m.escapeConsumes() {
		t.Error("INSERT should consume esc (return to NORMAL, not detach)")
	}
}
