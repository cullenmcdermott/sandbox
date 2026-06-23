package dashboard

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEncodeKey(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{"printable", tea.KeyPressMsg{Code: 'a', Text: "a"}, "a"},
		{"shifted printable", tea.KeyPressMsg{Code: 'a', Text: "A", Mod: tea.ModShift}, "A"},
		{"ctrl-c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "\x03"},
		{"ctrl-a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, "\x01"},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "\r"},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, "\t"},
		{"backspace", tea.KeyPressMsg{Code: tea.KeyBackspace}, "\x7f"},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1b[A"},
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, "\x1b[B"},
		{"home", tea.KeyPressMsg{Code: tea.KeyHome}, "\x1b[H"},
		{"f1", tea.KeyPressMsg{Code: tea.KeyF1}, "\x1bOP"},
		{"alt-b", tea.KeyPressMsg{Code: 'b', Text: "b", Mod: tea.ModAlt}, "\x1bb"},

		// O6 regression: modifiers on navigation/function keys must reach the
		// embedded TUI via the xterm CSI parameter form (old code dropped them
		// and returned the plain sequence).
		{"shift-up", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}, "\x1b[1;2A"},
		{"ctrl-up", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl}, "\x1b[1;5A"},
		{"ctrl-right (word jump)", tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}, "\x1b[1;5C"},
		{"shift-ctrl-left", tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift | tea.ModCtrl}, "\x1b[1;6D"},
		{"alt-up", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}, "\x1b[1;3A"},
		{"shift-home", tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift}, "\x1b[1;2H"},
		{"ctrl-delete", tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModCtrl}, "\x1b[3;5~"},
		{"ctrl-pgup", tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl}, "\x1b[5;5~"},
		{"shift-f5", tea.KeyPressMsg{Code: tea.KeyF5, Mod: tea.ModShift}, "\x1b[15;2~"},
		{"shift-f1", tea.KeyPressMsg{Code: tea.KeyF1, Mod: tea.ModShift}, "\x1b[1;2P"},
		{"shift-tab (back-tab)", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "\x1b[Z"},
		// Unmodified keys are unaffected (no CSI parameter).
		{"plain-up-still-bare", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1b[A"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(encodeKey(c.msg)); got != c.want {
				t.Errorf("encodeKey(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}
