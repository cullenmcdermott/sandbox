package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// drive runs the model through a window-size + the full scripted event stream at
// the given size and returns the final rendered frame plus the model. It proves
// the public-package-only example builds the transcript from client.Event values
// with no network and no hand-assembled items.
func drive(w, h, ticks int) (string, *model) {
	var m tea.Model = newModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	for i := 0; i < ticks; i++ {
		m, _ = m.Update(tickMsg{})
	}
	mm := m.(*model)
	return mm.View().Content, mm
}

// TestScriptCompletesWidthSafe drives the whole scripted turn at the required
// sizes and asserts it completes and every rendered line fits width.
func TestScriptCompletesWidthSafe(t *testing.T) {
	for _, s := range [][2]int{{80, 24}, {100, 30}, {140, 40}} {
		frame, m := drive(s[0], s[1], 120)
		if !m.turnDone {
			t.Errorf("%v: scripted turn did not complete", s)
		}
		if strings.TrimSpace(ansi.Strip(frame)) == "" {
			t.Fatalf("%v: empty frame", s)
		}
		for i, l := range strings.Split(frame, "\n") {
			if lw := lipgloss.Width(l); lw > s[0] {
				t.Errorf("%v: line %d overflows (%d cols): %q", s, i, lw, l)
			}
		}
	}
}

// TestTranscriptBuiltFromEvents proves the transcript body is actually produced
// by the event stream (its distinctive grammar appears in the frame).
func TestTranscriptBuiltFromEvents(t *testing.T) {
	frame, _ := drive(120, 40, 120)
	got := ansi.Strip(frame)
	for _, want := range []string{"reconnect is flaky", "∴", "Bash", "◇"} {
		if !strings.Contains(got, want) {
			t.Errorf("event-sourced transcript missing %q:\n%s", want, got)
		}
	}
}

// TestCtrlTReskinsWholeTranscript proves the theme-swap wiring re-skins the
// committed transcript body (not just the footer) — the transcript package
// registers the theme.OnChange hook itself, and the demo dogfoods it via ctrl+t.
func TestCtrlTReskinsWholeTranscript(t *testing.T) {
	before, m := drive(120, 40, 120)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	after := mm.(*model).View().Content

	body := func(s string) string {
		var keep []string
		for _, l := range strings.Split(s, "\n") {
			if strings.ContainsAny(ansi.Strip(l), "⏺⎿⊟∴◇✓▸○>") {
				keep = append(keep, l)
			}
		}
		return strings.Join(keep, "\n")
	}
	if body(before) == "" {
		t.Fatal("no transcript body found")
	}
	if body(before) == body(after) {
		t.Error("ctrl+t left the committed transcript in the old palette (reskin bug)")
	}
	if ansi.Strip(body(before)) != ansi.Strip(body(after)) {
		t.Error("theme swap changed transcript structure (should be color-only)")
	}
}

// TestCtrlOExpandsTool exercises ctrl+o toggling the latest expandable tool card
// through the transcript's ToggleExpand.
func TestCtrlOExpandsTool(t *testing.T) {
	_, m := drive(120, 40, 120)
	var mm tea.Model = m
	before := mm.(*model).View().Content
	mm, _ = mm.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	after := mm.(*model).View().Content
	if before == after {
		t.Error("ctrl+o did not toggle the latest tool card")
	}
}

// TestTypingRoutesToComposer proves keystrokes land in the composer (its send
// path is wired back into the transcript), not the transcript's demo keys.
func TestTypingRoutesToComposer(t *testing.T) {
	_, m := drive(100, 30, 120)
	var mm tea.Model = m
	for _, r := range "hello" {
		mm, _ = mm.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := mm.(*model).comp.Value(); got != "hello" {
		t.Errorf("composer did not collect typed text: %q", got)
	}
	// 'r' and 'q' must type into a non-empty composer, not replay/quit.
	if mm.(*model).turnDone == false {
		t.Fatal("precondition: turn should be done")
	}
	mm, cmd := mm.(*model).Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd != nil {
		t.Error("'r' with a non-empty draft triggered replay instead of typing")
	}
	if got := mm.(*model).comp.Value(); got != "hellor" {
		t.Errorf("'r' did not type into the draft: %q", got)
	}
}
