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
