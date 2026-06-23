package dashboard

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// xtermModParam returns the xterm modifier parameter (1 + shift + 2·alt + 4·ctrl)
// for a key, or 0 when none of Shift/Alt/Ctrl are set. The CSI form appends this
// as the second parameter, e.g. Shift+Up = ESC[1;2A, Ctrl+Right = ESC[1;5C.
func xtermModParam(m tea.KeyMod) int {
	v := 0
	if m.Contains(tea.ModShift) {
		v |= 1
	}
	if m.Contains(tea.ModAlt) {
		v |= 2
	}
	if m.Contains(tea.ModCtrl) {
		v |= 4
	}
	return v
}

// encodeModifiedSpecial encodes a navigation/function key carrying a Shift/Alt/
// Ctrl modifier as the xterm CSI parameterized sequence the child terminal
// expects (O6). Returns nil when the key is not such a key or has no relevant
// modifier, so the caller falls through to the plain (unmodified) encoding.
func encodeModifiedSpecial(msg tea.KeyPressMsg) []byte {
	v := xtermModParam(msg.Mod)
	if v == 0 {
		return nil
	}
	p := v + 1
	csiLetter := func(final byte) []byte { return []byte(fmt.Sprintf("\x1b[1;%d%c", p, final)) }
	csiTilde := func(num int) []byte { return []byte(fmt.Sprintf("\x1b[%d;%d~", num, p)) }
	switch msg.Code {
	case tea.KeyUp:
		return csiLetter('A')
	case tea.KeyDown:
		return csiLetter('B')
	case tea.KeyRight:
		return csiLetter('C')
	case tea.KeyLeft:
		return csiLetter('D')
	case tea.KeyHome:
		return csiLetter('H')
	case tea.KeyEnd:
		return csiLetter('F')
	case tea.KeyPgUp:
		return csiTilde(5)
	case tea.KeyPgDown:
		return csiTilde(6)
	case tea.KeyInsert:
		return csiTilde(2)
	case tea.KeyDelete:
		return csiTilde(3)
	case tea.KeyF1:
		return csiLetter('P')
	case tea.KeyF2:
		return csiLetter('Q')
	case tea.KeyF3:
		return csiLetter('R')
	case tea.KeyF4:
		return csiLetter('S')
	case tea.KeyF5:
		return csiTilde(15)
	case tea.KeyF6:
		return csiTilde(17)
	case tea.KeyF7:
		return csiTilde(18)
	case tea.KeyF8:
		return csiTilde(19)
	case tea.KeyF9:
		return csiTilde(20)
	case tea.KeyF10:
		return csiTilde(21)
	case tea.KeyF11:
		return csiTilde(23)
	case tea.KeyF12:
		return csiTilde(24)
	case tea.KeyTab:
		// Shift+Tab is the universal back-tab; other Tab modifiers have no
		// portable encoding, so fall through to a plain Tab.
		if v == 1 {
			return []byte("\x1b[Z")
		}
	}
	return nil
}

// encodeKey turns a Bubble Tea key press back into the byte sequence a terminal
// would send to a child program, for forwarding to the embedded `opencode
// attach` PTY (see external_pane.go). The child runs with TERM=xterm-256color,
// so we emit xterm-style sequences.
//
// This is a pragmatic subset covering printable input, Ctrl/Alt chords, and the
// navigation/editing/function keys an interactive TUI needs. The universal
// escape (esc / ctrl+]) is intercepted by the App before it reaches the pane,
// so it never has to be encoded here.
func encodeKey(msg tea.KeyPressMsg) []byte {
	// Modified navigation/function keys (Shift/Ctrl/Alt + arrows, Home/End,
	// PgUp/Dn, Insert/Delete, F-keys, Shift+Tab) use the xterm CSI modifier
	// parameter — handled first so the Alt-as-ESC-prefix path below doesn't
	// mangle them and the modifier reaches the embedded TUI (O6).
	if seq := encodeModifiedSpecial(msg); seq != nil {
		return seq
	}

	// Alt is an ESC prefix on the otherwise-unmodified key.
	if msg.Mod.Contains(tea.ModAlt) {
		inner := msg
		inner.Mod &^= tea.ModAlt
		base := encodeKey(inner)
		if len(base) == 0 {
			return nil
		}
		return append([]byte{0x1b}, base...)
	}

	// Ctrl chords: map Ctrl+<a-z> to control bytes 0x01–0x1a, plus the common
	// non-letter controls.
	if msg.Mod.Contains(tea.ModCtrl) {
		switch {
		case msg.Code >= 'a' && msg.Code <= 'z':
			return []byte{byte(msg.Code) & 0x1f}
		case msg.Code >= 'A' && msg.Code <= 'Z':
			return []byte{byte(msg.Code-'A'+'a') & 0x1f}
		case msg.Code == ' ' || msg.Code == '@':
			return []byte{0x00}
		case msg.Code == '\\':
			return []byte{0x1c}
		case msg.Code == '_' || msg.Code == '/':
			return []byte{0x1f}
		}
		// Other Ctrl combinations fall through to special-key handling.
	}

	switch msg.Code {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	}

	// Printable input: Text holds the actual (already shift-resolved) characters.
	if msg.Text != "" {
		return []byte(msg.Text)
	}
	return nil
}
