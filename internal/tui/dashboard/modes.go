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

// escStep is one level of the transcript's esc priority cascade. applies
// reports whether the step has a local meaning right now; run performs it.
type escStep struct {
	name    string
	applies func() bool
	run     func(msg tea.KeyPressMsg) tea.Cmd
}

// escCascade returns the ordered esc priority list. escapeConsumes and the
// esc key handler both read this list — it is the single encoding of the
// cascade (§2a input contexts): close an open overlay (search, then slash
// palette); steer a running turn with a queued prompt (interrupt + inject);
// interrupt a running turn outright; stop an idle /loop or /goal driver; leave
// INSERT for NORMAL. Help is handled ahead of this (any key closes it), so it
// is not a cascade step — see escapeConsumes.
func (m *TranscriptModel) escCascade() []escStep {
	return []escStep{
		{"search", func() bool { return m.search.open }, func(msg tea.KeyPressMsg) tea.Cmd {
			cmd, _ := m.searchKey(msg)
			return cmd
		}},
		{"palette", m.paletteOpen, func(msg tea.KeyPressMsg) tea.Cmd {
			cmd, _ := m.paletteKey(msg)
			return cmd
		}},
		// A queued prompt steers the running turn (interrupt + inject) instead of
		// detaching. queueSteer retains the prompt until turn.interrupted so the
		// follow-up POST doesn't 409 the still-active turn (R4).
		{"steer", func() bool { return m.queuedPrompt != "" }, func(tea.KeyPressMsg) tea.Cmd { return m.queueSteer() }},
		{"interrupt", func() bool { return m.turnActive }, func(tea.KeyPressMsg) tea.Cmd { return m.interruptTurn() }},
		// A /loop or /goal idle between ticks/turns: esc reclaims control and stops
		// the driver, honoring the chip's "esc to stop" contract even when there's
		// no live turn to interrupt (§1e item 5). Detach stays on ctrl+].
		{"driver", m.driverActive, func(tea.KeyPressMsg) tea.Cmd { return m.stopDriver("autopilot stopped") }},
		// Vim on, INSERT: esc returns to NORMAL rather than detaching.
		{"vim-insert", func() bool { return m.vimEnabled && m.imode == modeInsert }, func(tea.KeyPressMsg) tea.Cmd {
			m.enterNormal()
			return nil
		}},
	}
}

// escapeConsumes reports whether esc should be handled inside the transcript
// rather than detaching to the dashboard. It derives entirely from escCascade:
// esc is consumed when any cascade step has a local meaning right now (an
// overlay is open, a turn/driver is active, a prompt is queued to steer, or vim
// INSERT can return to NORMAL). showHelp is a separate leading term because the
// help overlay is closed ahead of the esc branch (any key closes it), so it is
// not a cascade step. With vim off and none of these true, a bare idle esc has
// no local meaning and falls through to the App's detach (preserving the old
// NORMAL-mode esc-detach). ctrl+] / ctrl+4 (and NORMAL-mode q) always detach
// regardless.
func (m *TranscriptModel) escapeConsumes() bool {
	if m.showHelp {
		return true
	}
	for _, step := range m.escCascade() {
		if step.applies() {
			return true
		}
	}
	return false
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
