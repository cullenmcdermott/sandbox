package dashboard

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// A freshly opened session shows a first-hint welcome in the body instead of a
// blank void; it disappears the moment there's anything to render.
func TestFreshTranscriptShowsWelcome(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	if !m.transcriptEmpty() {
		t.Fatal("a fresh transcript should be empty")
	}
	out := m.renderTranscript(m.width, m.height)
	for _, want := range []string{"ready when you are", "type a message below to begin"} {
		if !strings.Contains(out, want) {
			t.Errorf("welcome missing %q in render:\n%s", want, out)
		}
	}

	// Once a block lands, the transcript is no longer empty and the welcome is gone.
	m.blocks = append(m.blocks, m.newBlockCard(blockUser, "hi"))
	if m.transcriptEmpty() {
		t.Fatal("transcript with a committed block should not be empty")
	}
	if got := m.renderTranscript(m.width, m.height); strings.Contains(got, "ready when you are") {
		t.Error("welcome should disappear once history exists")
	}
}

// emptyTranscriptView must fill exactly width×height at any width so it can't push
// the surrounding chrome around — especially narrow terminals where a hint line
// would otherwise overflow.
func TestEmptyTranscriptViewExactRect(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	for _, w := range []int{20, 40, 80} {
		out := m.emptyTranscriptView(w, 10)
		lines := strings.Split(out, "\n")
		if len(lines) != 10 {
			t.Errorf("w=%d: %d lines, want 10", w, len(lines))
		}
		for i, l := range lines {
			if lw := lipgloss.Width(l); lw != w {
				t.Errorf("w=%d line %d width=%d, want %d", w, i, lw, w)
			}
		}
	}
}

// transcriptEmpty must also suppress the welcome mid-turn (streaming) so it never
// flashes over live output.
func TestTranscriptEmptySuppressedWhileStreaming(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.streaming = true
	if m.transcriptEmpty() {
		t.Error("a streaming transcript must not count as empty")
	}
}
