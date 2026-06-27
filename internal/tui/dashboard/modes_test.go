package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Vim modal editing is OFF by default: the transcript opens in INSERT with the
// prompt focused so every key types (no "press i" surprise). /vim (setVim)
// toggles modal editing on, restoring the NORMAL/INSERT chords.

func TestTranscriptStartsFocusedVimOff(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	if m.vimEnabled {
		t.Fatal("vim modal editing should be off by default")
	}
	if m.imode != modeInsert {
		t.Fatalf("new transcript imode = %v, want modeInsert (vim off → always focused)", m.imode)
	}
}

func TestVimToggleEnablesNormalAndBack(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24

	m.setVim(true)
	if !m.vimEnabled || m.imode != modeNormal {
		t.Fatalf("after /vim on: vimEnabled=%v imode=%v, want true/modeNormal", m.vimEnabled, m.imode)
	}

	m.setVim(false)
	if m.vimEnabled || m.imode != modeInsert {
		t.Fatalf("after /vim off: vimEnabled=%v imode=%v, want false/modeInsert", m.vimEnabled, m.imode)
	}
}

func TestVimOffTypingReachesPrompt(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.input.Focus() // Init focuses the prompt when vim is off

	// Keys that are NORMAL-mode chords when vim is on (q detach, j scroll) must
	// type normally when vim is off.
	for _, k := range []string{"q", "j", "z"} {
		m.handleKey(keyMsg(k))
	}
	if got := m.input.Value(); got != "qjz" {
		t.Fatalf("vim-off typing = %q, want %q", got, "qjz")
	}
}

func TestVimOnNormalIEntersInsert(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.setVim(true) // NORMAL
	m.handleKey(keyMsg("i"))
	if m.imode != modeInsert {
		t.Fatalf("after 'i' in NORMAL, imode = %v, want modeInsert", m.imode)
	}
}

func TestVimOnInsertEscReturnsToNormal(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.vimEnabled = true
	m.imode = modeInsert
	m.handleKey(keyMsg("esc"))
	if m.imode != modeNormal {
		t.Fatalf("after esc in INSERT (vim on), imode = %v, want modeNormal", m.imode)
	}
}

func TestEscInterruptsActiveTurn(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.turnActive = true // imode defaults to modeInsert (vim off)
	_, cmd := m.handleKey(keyMsg("esc"))
	if cmd == nil {
		t.Fatal("esc during active turn produced no command")
	}
	cmd() // executes the InterruptTurn call
	if fc.interrupts != 1 {
		t.Fatalf("esc during active turn: interrupts = %d, want 1", fc.interrupts)
	}
	// Interrupting must not also drop the input mode — esc only interrupts.
	if m.imode != modeInsert {
		t.Fatalf("esc-interrupt changed imode to %v, want modeInsert", m.imode)
	}
}

func TestEscInterruptTargetsLiveTurnID(t *testing.T) {
	// Regression: the interrupt used to send an empty TurnRef, producing the URL
	// /sessions/<id>/turns//interrupt which the runner route never matched, so esc
	// never actually cancelled the turn. The TUI must send the turn id captured
	// from turn.started.
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.handleEvent(session.Event{Type: session.EventTurnStarted, TurnID: "turn-42"})
	if !m.turnActive {
		t.Fatal("turn.started did not mark the turn active")
	}
	_, cmd := m.handleKey(keyMsg("esc"))
	if cmd == nil {
		t.Fatal("esc during active turn produced no command")
	}
	cmd()
	if len(fc.interruptRefs) != 1 {
		t.Fatalf("interrupts = %d, want 1", len(fc.interruptRefs))
	}
	if got := fc.interruptRefs[0].Turn; got != "turn-42" {
		t.Fatalf("interrupt sent turn id %q, want %q (empty id is the bug)", got, "turn-42")
	}
}

func TestEscSteerInterruptsThenSubmitsAfterInterruptEvent(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.turnActive = true
	m.queuedPrompt = "steer me"

	// esc with a queued prompt STEERS (not a bare interrupt): it fires the
	// interrupt now but RETAINS the queued prompt, deferring the new turn until
	// turn.interrupted confirms the old turn is gone — otherwise the follow-up
	// POST 409s against the still-active turn (R4).
	_, cmd := m.handleKey(keyMsg("esc"))
	if cmd == nil {
		t.Fatal("esc with a queued prompt produced no command")
	}
	cmd() // executes the InterruptTurn call
	if fc.interrupts != 1 {
		t.Fatalf("steer: interrupts = %d, want 1", fc.interrupts)
	}
	if m.queuedPrompt != "steer me" {
		t.Fatalf("steer dropped the queued prompt early: %q (want retained until turn.interrupted)", m.queuedPrompt)
	}

	// The runner answers turn.interrupted → the queued prompt is flushed as the
	// next turn (begins a new turn; the start cmd POSTs the prompt).
	flush := m.handleEvent(mkEvent(session.EventTurnInterrupted, nil))
	if m.queuedPrompt != "" {
		t.Fatalf("turn.interrupted left queuedPrompt = %q, want flushed", m.queuedPrompt)
	}
	if !m.turnActive {
		t.Fatal("steer flush did not begin the next turn (turnActive=false)")
	}
	if flush == nil {
		t.Fatal("turn.interrupted with a queued steer produced no follow-up command")
	}
	execCmd(flush) // drives startTurnCmd → StartTurn
	if len(fc.startedPrompts) != 1 || fc.startedPrompts[0] != "steer me" {
		t.Fatalf("steer did not POST the queued prompt: startedPrompts = %v", fc.startedPrompts)
	}
}

func TestEscapeConsumesDuringActiveTurn(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	// Vim off, no overlay: idle esc would detach, but an active turn must be
	// consumed so the App delegates esc to the transcript to interrupt it.
	if m.escapeConsumes() {
		t.Fatal("precondition: idle (vim off) should not consume esc")
	}
	m.turnActive = true
	if !m.escapeConsumes() {
		t.Error("active turn should consume esc (interrupt, not detach)")
	}
}

func TestVimOnNormalSwallowsTyping(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.setVim(true) // NORMAL, prompt blurred
	// A stray letter in NORMAL must not leak into the blurred prompt.
	m.handleKey(keyMsg("z"))
	if got := m.input.Value(); got != "" {
		t.Fatalf("NORMAL leaked typing into prompt: %q", got)
	}
}

func TestVimOnNormalQDetaches(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.setVim(true) // NORMAL
	_, cmd := m.handleKey(keyMsg("q"))
	if cmd == nil {
		t.Fatal("'q' in NORMAL produced no command")
	}
	if _, ok := cmd().(detachMsg); !ok {
		t.Fatalf("'q' did not emit detachMsg")
	}
}

func TestEscapeConsumesVimAware(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)

	// Vim off (default): INSERT, idle → not consumed, so the App detaches.
	if m.escapeConsumes() {
		t.Error("vim off + idle INSERT should not consume esc (App detaches)")
	}

	// Vim on, INSERT, idle → consumed (esc returns to NORMAL).
	m.vimEnabled = true
	m.imode = modeInsert
	if !m.escapeConsumes() {
		t.Error("vim on + INSERT should consume esc (return to NORMAL)")
	}

	// Vim on, NORMAL, idle → not consumed (App detaches; q also detaches).
	m.imode = modeNormal
	if m.escapeConsumes() {
		t.Error("vim on + NORMAL idle should not consume esc (App detaches)")
	}
}
