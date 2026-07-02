package dashboard

import (
	"strings"
	"testing"
)

func TestHelpModelExpandCollapse(t *testing.T) {
	cats := []helpCategory{
		{name: "Session", entries: []helpEntry{{"/clear", "clear"}, {"/diff", "diff"}}},
		{name: "Mode", entries: []helpEntry{{"/plan", "plan mode"}}},
	}
	h := newHelpModel("help", cats)

	// Starts fully expanded: every entry visible.
	if v := h.view(80); !strings.Contains(v, "/plan") || !strings.Contains(v, "/clear") {
		t.Fatalf("expanded view missing entries:\n%s", v)
	}
	// Category counts render in the header.
	if !strings.Contains(h.view(80), "(2)") {
		t.Errorf("category count not shown")
	}

	// Navigate to the Mode category and collapse it (space).
	if !h.handleKey("down") {
		t.Fatal("down not consumed")
	}
	if h.sel != 1 {
		t.Fatalf("sel = %d, want 1", h.sel)
	}
	if !h.handleKey("space") {
		t.Fatal("space not consumed")
	}
	if v := h.view(80); strings.Contains(v, "plan mode") {
		t.Errorf("collapsed Mode still shows its entry:\n%s", v)
	}
	// Session (index 0) stays expanded.
	if !strings.Contains(h.view(80), "/clear") {
		t.Errorf("collapsing Mode wrongly hid Session entries")
	}

	// A non-nav key is not consumed (caller closes the overlay).
	if h.handleKey("x") {
		t.Errorf("unexpected consume of 'x'")
	}
}

func TestKeymapCategoriesNoDrift(t *testing.T) {
	cats := keymapCategories(DefaultKeyMap())
	if len(cats) == 0 {
		t.Fatal("no categories built from keymap")
	}
	var all string
	for _, c := range cats {
		for _, e := range c.entries {
			all += e.key + " " + e.desc + "\n"
		}
	}
	// An enabled binding shows up...
	if !strings.Contains(all, "new") {
		t.Errorf("enabled 'new' binding missing from help: %s", all)
	}
	// ...the live group-view binding is documented...
	if !strings.Contains(all, "group view") {
		t.Errorf("enabled 'group view' binding missing from help: %s", all)
	}
	// ...and a disabled reserved binding (Command ":") does not leak in.
	if strings.Contains(all, "command") {
		t.Errorf("disabled binding leaked into help: %s", all)
	}
}

func TestChatHelpCategories(t *testing.T) {
	cats := chatHelpCategories()
	names := map[string]bool{}
	hasPlan, hasKeys := false, false
	for _, c := range cats {
		names[c.name] = true
		for _, e := range c.entries {
			if e.key == "/plan" {
				hasPlan = true
			}
			if e.key == "shift+tab" {
				hasKeys = true
			}
		}
	}
	for _, want := range []string{"Session", "Mode", "Tools", "Help", "Keys"} {
		if !names[want] {
			t.Errorf("chat help missing category %q", want)
		}
	}
	if !hasPlan || !hasKeys {
		t.Errorf("chat help missing commands/keys (plan=%v keys=%v)", hasPlan, hasKeys)
	}
}

func TestTranscriptHelpToggle(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	// `?` with an empty prompt opens the help; non-empty types instead.
	m.input.SetValue("hi?")
	m.handleKey(keyMsg("?"))
	if m.showHelp {
		t.Fatal("? opened help while composing a message")
	}
	m.input.Reset()
	m.handleKey(keyMsg("?"))
	if !m.showHelp {
		t.Fatal("? did not open help on an empty prompt")
	}
	if v := m.helpUI.view(m.width); !strings.Contains(v, "/plan") {
		t.Errorf("chat help overlay missing commands:\n%s", v)
	}

	// space/up navigate (stay open); a plain key closes.
	if _, _ = m.handleKey(keyMsg("down")); !m.showHelp {
		t.Errorf("nav closed the help overlay")
	}
	m.handleKey(keyMsg("x"))
	if m.showHelp {
		t.Errorf("a non-nav key did not close the help overlay")
	}

	// /help opens the same overlay.
	m.input.SetValue("/help")
	m.handleKey(keyMsg("enter"))
	if !m.showHelp {
		t.Errorf("/help did not open the help overlay")
	}
}
