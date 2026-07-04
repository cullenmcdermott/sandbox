package dashboard

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// inputMode is the vim-style modal input state of the transcript prompt. The
// transcript opens in NORMAL so single-key commands work without a "press i
// first" surprise being lost to the prompt; INSERT routes keystrokes into the
// prompt and esc returns to NORMAL. This is distinct from permMode (the SDK
// permission policy cycled with shift+tab).
type inputMode int

const (
	modeNormal inputMode = iota
	modeInsert
)

// enterInsert switches to INSERT and focuses the prompt. When toEnd is true the
// cursor is placed after the existing text (vim `a`), otherwise it is left where
// it is (vim `i`). Returns the textinput focus Cmd (drives the cursor blink).
func (m *TranscriptModel) enterInsert(toEnd bool) tea.Cmd {
	m.imode = modeInsert
	if toEnd {
		m.input.CursorEnd()
	}
	return m.input.Focus()
}

// enterNormal switches to NORMAL and blurs the prompt so single-key commands
// stop typing into it.
func (m *TranscriptModel) enterNormal() {
	m.imode = modeNormal
	m.input.Blur()
}

// escapeConsumes reports whether esc should be handled inside the transcript
// rather than detaching to the dashboard. True when an overlay is open (help,
// search, slash palette) or a turn is active — esc has a local meaning there
// (close / interrupt the turn). With vim modal editing on, esc in INSERT is also
// consumed (it returns to NORMAL). With vim off the prompt is always focused, so
// a bare idle esc has no local meaning and falls through to the App's detach
// (preserving the old NORMAL-mode esc-detach). ctrl+] / ctrl+4 (and NORMAL-mode
// q) always detach regardless.
func (m *TranscriptModel) escapeConsumes() bool {
	if m.showHelp || m.search.open || m.paletteOpen() || m.turnActive {
		return true
	}
	// A running /loop or /goal gives a bare esc a local meaning even when idle
	// between ticks: it stops the driver (the chip promises "esc to stop") rather
	// than detaching with the driver still armed (§1e item 5). Detach is ctrl+].
	if m.autopilot.active() {
		return true
	}
	return m.vimEnabled && m.imode == modeInsert
}

// setVim turns vim-style modal editing on or off and returns the follow-up Cmd
// that applies the resulting input mode. Off (the default): pin to INSERT and
// focus the prompt so every key types and esc keeps its interrupt/steer/detach
// meaning; NORMAL is never entered. On: drop to NORMAL so the single-key chords
// (i/a/j/k/g/G/q) work and the mode badge shows.
func (m *TranscriptModel) setVim(enabled bool) tea.Cmd {
	m.vimEnabled = enabled
	if !enabled {
		return m.enterInsert(false)
	}
	m.enterNormal()
	return nil
}

// normalKey handles a key press while in NORMAL mode. It returns (cmd, true)
// when the key was a NORMAL-mode command and (nil, false) when it should fall
// through to the shared handlers. Unhandled printable keys are swallowed (return
// nil, true) so stray letters never leak into the blurred prompt.
func (m *TranscriptModel) normalKey(key string, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch key {
	case "i":
		return m.enterInsert(false), true
	case "a":
		return m.enterInsert(true), true
	case "q":
		// Detach to the dashboard. The App owns the screen switch, so emit the
		// same message its esc path produces.
		return func() tea.Msg { return detachMsg{} }, true
	case "/":
		m.openSearch()
		return nil, true
	case "j":
		m.body.ScrollBy(1)
		return nil, true
	case "k":
		m.body.ScrollBy(-1)
		return nil, true
	case "g":
		m.body.GotoTop()
		return nil, true
	case "G":
		m.body.GotoBottom()
		return nil, true
	}
	// Shared scroll keys (arrows, page, ctrl+u/d, home/end) work in NORMAL too.
	if m.scrollKey(key) {
		return nil, true
	}
	// Swallow everything else: in NORMAL the prompt is blurred and must not type.
	return nil, true
}

// modeBadge renders the small NORMAL/INSERT indicator shown on the prompt line.
func (m *TranscriptModel) modeBadge() string {
	label, fg := " NORMAL ", theme.Malibu
	if m.imode == modeInsert {
		label, fg = " INSERT ", theme.Guac
	}
	return lipgloss.NewStyle().Foreground(theme.Page).Background(fg).Bold(true).Render(label)
}
