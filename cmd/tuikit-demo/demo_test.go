package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// key builds a KeyPressMsg for a named special/control key so msg.String()
// resolves the way the app's switch expects.
func key(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "ctrl+g":
		return tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
	case "ctrl+t":
		return tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	case "ctrl+n":
		return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Text: name}
	}
}

// send pushes a message through Update and returns the model, asserting the
// concrete type is preserved.
func send(t *testing.T, m *model, msg tea.Msg) *model {
	t.Helper()
	next, _ := m.Update(msg)
	mm, ok := next.(*model)
	if !ok {
		t.Fatalf("Update returned %T, want *model", next)
	}
	return mm
}

// typeRunes feeds each rune of s as a printable key press.
func typeRunes(t *testing.T, m *model, s string) *model {
	t.Helper()
	for _, r := range s {
		m = send(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	return m
}

// runTurn pumps tick messages until the turn returns to idle (bounded).
func runTurn(t *testing.T, m *model) *model {
	t.Helper()
	for i := 0; i < 2000 && m.phase != phaseIdle; i++ {
		m = send(t, m, tickMsg{})
	}
	if m.phase != phaseIdle {
		t.Fatal("turn never returned to idle")
	}
	return m
}

func TestFlowPickerToChat(t *testing.T) {
	m := newModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// The app opens directly on the model picker.
	if m.screen != screenPicker {
		t.Fatalf("want picker on start, got %v", m.screen)
	}
	if m.View().Content == "" {
		t.Fatal("picker view empty")
	}

	// Move selection and confirm enters chat with a greeting.
	m = send(t, m, key("down"))
	if m.pickerSel != 1 {
		t.Fatalf("down should advance selection, got sel=%d", m.pickerSel)
	}
	m = send(t, m, key("up"))
	m = send(t, m, key("enter"))
	if m.screen != screenChat {
		t.Fatalf("enter should start chat, got %v", m.screen)
	}
	if len(m.rows) != 1 {
		t.Fatalf("chat should seed one greeting, got %d rows", len(m.rows))
	}
	content := m.View().Content
	if content == "" {
		t.Fatal("chat view empty")
	}
	// The composed frame must not overflow the viewport height.
	if lines := strings.Count(content, "\n") + 1; lines > 30 {
		t.Fatalf("chat frame overflows height: %d lines > 30", lines)
	}
}

func TestMockedTurnStreamsToolAndText(t *testing.T) {
	m := newModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = send(t, m, key("enter")) // → chat (claude)

	before := len(m.rows)
	m = typeRunes(t, m, "run the tests")
	if m.input != "run the tests" {
		t.Fatalf("input not captured: %q", m.input)
	}
	m = send(t, m, key("enter")) // submit
	if m.phase == phaseIdle {
		t.Fatal("submit should start a turn")
	}
	m = runTurn(t, m)

	// Expect: user msg + tool card + assistant msg appended.
	if len(m.rows) < before+3 {
		t.Fatalf("turn should append user+tool+assistant, rows %d→%d", before, len(m.rows))
	}
	var sawTool, sawAssistant bool
	for _, it := range m.rows {
		switch v := it.(type) {
		case *toolItem:
			if v.done && v.result != "" {
				sawTool = true
			}
		case *msgItem:
			if v.role == roleAssistant && v.done && strings.TrimSpace(v.text) != "" {
				sawAssistant = true
			}
		}
	}
	if !sawTool {
		t.Error("expected a completed tool card")
	}
	if !sawAssistant {
		t.Error("expected a streamed assistant message")
	}
	if m.tokens == 0 {
		t.Error("tokens should have accumulated")
	}
}

func TestCapsAndThemeOverlays(t *testing.T) {
	m := newModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = send(t, m, key("enter"))

	// ctrl+g opens the capability overlay; any key dismisses it.
	m = send(t, m, key("ctrl+g"))
	if !m.showCaps {
		t.Fatal("ctrl+g should open caps panel")
	}
	if m.View().Content == "" {
		t.Fatal("caps overlay view empty")
	}
	m = send(t, m, tea.KeyPressMsg{Text: "z"})
	if m.showCaps {
		t.Fatal("any key should dismiss caps panel")
	}

	// ctrl+t cycles the theme without panicking and keeps rendering.
	m = send(t, m, key("ctrl+t"))
	if m.View().Content == "" {
		t.Fatal("view empty after theme cycle")
	}
}

func TestPasteAppendsToInput(t *testing.T) {
	m := newModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = send(t, m, key("enter")) // → chat

	m = typeRunes(t, m, "see ")
	m = send(t, m, tea.PasteMsg{Content: "line one\nline two\ttabbed"})
	if want := "see line one line two tabbed"; m.input != want {
		t.Fatalf("paste not appended/sanitized: got %q want %q", m.input, want)
	}
}

func TestKittyImagePopup(t *testing.T) {
	m := newModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = send(t, m, key("enter"))

	m = typeRunes(t, m, "show me a kitty image")
	m = send(t, m, key("enter"))
	if !m.showKitty {
		t.Fatal("kitty keyword should open the cat popup")
	}
	if m.caps.KittyGraphics && m.catXmit == "" {
		t.Fatal("on a kitty terminal the image transmission should be built")
	}
	if m.View().Content == "" {
		t.Fatal("kitty popup view empty")
	}
	before := m.input
	m = send(t, m, tea.KeyPressMsg{Text: "z"})
	if m.showKitty {
		t.Fatal("any key should dismiss the popup")
	}
	if m.input != before {
		t.Fatalf("dismiss key should not type: %q", m.input)
	}
}

func TestCatGridIsRectangular(t *testing.T) {
	w := len(catGrid[0])
	for i, row := range catGrid {
		if len(row) != w {
			t.Fatalf("catGrid row %d has width %d, want %d", i, len(row), w)
		}
	}
}

func TestEmbeddedCatPhotoDecodes(t *testing.T) {
	rgba, w, h, ok := catPhotoRGBA()
	if !ok {
		t.Fatal("embedded cat.jpg failed to decode")
	}
	if w <= 0 || h <= 0 || len(rgba) != w*h*4 {
		t.Fatalf("decoded photo dims off: w=%d h=%d len=%d", w, h, len(rgba))
	}
	if catPhotoTransmission() == "" {
		t.Fatal("cat photo transmission should be non-empty")
	}
}

func TestGaugeIsVisibleBlockBar(t *testing.T) {
	m := newModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = send(t, m, key("enter"))
	m.tokens = ctxLimit / 2
	m.updateGauge()
	bar := m.gaugeView()
	if !strings.Contains(bar, "█") || !strings.Contains(bar, "░") {
		t.Fatalf("gauge should be a visible block bar (fill+track): %q", bar)
	}
}

func TestMotionGatingStopsWhenIdle(t *testing.T) {
	m := newModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = send(t, m, key("enter"))

	// Idle chat schedules no tick.
	if cmd := m.ensureTick(); cmd != nil {
		t.Fatal("idle chat should not schedule a tick")
	}
	// A turn in flight does schedule one.
	m = typeRunes(t, m, "hello")
	next, cmd := m.Update(key("enter"))
	m = next.(*model)
	if cmd == nil {
		t.Fatal("an active turn should schedule a tick")
	}
	m = runTurn(t, m)
	if cmd := m.ensureTick(); cmd != nil {
		t.Fatal("turn finished: motion should stop")
	}
}
