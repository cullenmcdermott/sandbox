package dashboard

import (
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// --------------------------------------------------------------------------
// Key handling
// --------------------------------------------------------------------------

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	ks := msg.String()
	if ks != "g" {
		m.ggPending = false
	}

	// A destructive-action confirmation captures all keys until resolved.
	if m.confirm != nil {
		switch ks {
		case "y", "Y":
			action := m.confirm.action
			for i := range m.sessions {
				if m.sessions[i].ID() == m.confirm.id {
					m.sessions[i].PendingAction = "destroy"
					break
				}
			}
			m.confirm = nil
			return m, action
		case "n", "N", "esc":
			for i := range m.sessions {
				if m.sessions[i].ID() == m.confirm.id {
					m.sessions[i].PendingAction = ""
					break
				}
			}
			m.confirm = nil
		}
		return m, nil
	}

	// While the help overlay is open, ↑/↓ + space drive the grouped surface;
	// any other key closes it.
	if m.showHelp {
		if m.helpUI.handleKey(ks) {
			return m, nil
		}
		m.showHelp = false
		return m, nil
	}

	// Switcher overlay takes all keys while open.
	if m.switcher.open {
		cmd, _ := m.switcherKey(msg)
		return m, cmd
	}

	// Permission queue overlay takes all keys while open.
	if m.permQueue.open {
		cmd, _ := m.permQueueKey(msg)
		return m, cmd
	}

	// Filtering mode intercepts most keys for the filter buffer.
	if m.filtering {
		return m.handleFilterKey(ks)
	}

	// Rename mode intercepts text input for the rename buffer.
	if m.renaming {
		return m.handleRenameKey(ks)
	}

	// When the initial cluster seed failed, the list shows an error + retry in
	// place of skeleton bars. r re-issues the seed + watch (with no sessions to
	// resume, this takes precedence over the resume binding harmlessly).
	if m.seedErr != nil && ks == "r" {
		m.seedErr = nil
		return m, tea.Batch(m.seedCmd(), m.startWatchCmd())
	}

	// esc dismisses a lingering connect/action error in the detail pane. Overlays
	// and input modes are handled above, so a bare esc here is safe to consume;
	// when there's nothing to dismiss it falls through to the no-op default.
	if ks == "esc" && (m.connectErr != nil || m.actionErr != nil) {
		m.connectErr = nil
		m.actionErr = nil
		return m, nil
	}

	// q opens the pending-permission queue when sessions are waiting.
	if ks == "q" {
		if len(m.permQueueItems()) > 0 {
			m.openPermQueue()
			return m, nil
		}
	}

	// Rename selected session.
	if key.Matches(msg, m.keys.Rename) {
		m.openRename()
		return m, nil
	}

	// In group view, space expands/collapses the repo group at the cursor.
	if m.groupView.open && ks == "space" {
		m.toggleRepoGroup()
		return m, nil
	}

	// ⌃G: jump the cursor to the next session needing attention (wraps; expands a
	// collapsed group in group view). Already worked from inside the chat modal
	// (app.go); this makes the documented key live on the dashboard screen too.
	if key.Matches(msg, m.keys.NextAttention) {
		m.jumpToNextNeedingAttention()
		return m, nil
	}

	// Quick-switcher (⌃K): fuzzy jump to any session. The overlay's own key
	// handling (switcherKey) takes over once open, including ctrl+k to close.
	if key.Matches(msg, m.keys.Switcher) {
		m.openSwitcher()
		return m, nil
	}

	// Quit
	if key.Matches(msg, m.keys.Quit) {
		m.Cancel()
		return m, tea.Quit
	}

	// Help overlay (grouped, expandable; sourced from the keymap).
	if key.Matches(msg, m.keys.Help) {
		m.helpUI = newHelpModel("keybindings", keymapCategories(m.keys))
		m.showHelp = true
		return m, nil
	}
	// `g` is overloaded: a lone press toggles group view (handoff: Layout B is a
	// one-keystroke `g` toggle), while a quick `gg` jumps to the top. The first
	// `g` toggles group view and arms the chord; a second `g` reverts that
	// transient toggle and jumps to the top, so `gg` is a clean "go to top".
	if ks == "g" {
		if m.ggPending {
			m.ggPending = false
			m.toggleGroupView() // revert the toggle the first g applied
			m.cursor = 0
		} else {
			m.ggPending = true
			m.toggleGroupView()
		}
		return m, nil
	}
	if ks == "G" {
		m.ggPending = false
		rows := m.visibleRows() // rows, not sessions: group headers count
		if len(rows) > 0 {
			m.cursor = len(rows) - 1
		}
		return m, nil
	}
	// Navigation: in group view, up/down move over rows in grouped order.
	if key.Matches(msg, m.keys.Up) {
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Down) {
		visible := m.visibleRows()
		if m.cursor < len(visible)-1 {
			m.cursor++
		}
		return m, nil
	}

	// Filter start
	if key.Matches(msg, m.keys.Filter) {
		m.filtering = true
		m.filterBuf = ""
		return m, nil
	}

	// Sort: cycle key (disabled in group view).
	if !m.groupView.open && key.Matches(msg, m.keys.SortCycle) {
		m.sortKey = m.sortKey.Next()
		m.sortSessions()
		m.clampCursor()
		return m, nil
	}
	// Sort: flip direction (disabled in group view).
	if !m.groupView.open && key.Matches(msg, m.keys.SortFlip) {
		m.sortDir = m.sortDir.Flip()
		m.sortSessions()
		m.clampCursor()
		return m, nil
	}

	// Attention-first toggle (D4): float Waiting/NeedsInput to the top.
	if !m.groupView.open && key.Matches(msg, m.keys.AttentionToggle) {
		m.attentionFirst = !m.attentionFirst
		m.clampCursor()
		return m, nil
	}

	// Attach — flip to transcript screen via the App.
	if key.Matches(msg, m.keys.Attach) {
		sel := m.selectedRowSession()
		if sel != nil {
			return m, func() tea.Msg { return attachMsg{sess: *sel} }
		}
		return m, nil
	}

	// Approve / Deny — inline permission from the detail pane.
	if key.Matches(msg, m.keys.Approve) {
		sel := m.selectedRowSession()
		if sel != nil && sel.DashStatus == StatusWaiting {
			return m, m.approveCmd(*sel, true)
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Deny) {
		sel := m.selectedRowSession()
		if sel != nil && sel.DashStatus == StatusWaiting {
			return m, m.approveCmd(*sel, false)
		}
		return m, nil
	}

	// New session — delegated to the App, which owns the Creator.
	if key.Matches(msg, m.keys.New) {
		return m, func() tea.Msg { return createSessionMsg{} }
	}

	// Suspend — scale the selected session's pod to zero (recoverable).
	// The SSE stream is NOT cancelled here: if the suspend action fails, an
	// eager cancel would leave a still-running session deaf. On success the
	// cluster watch delivers the Suspended state and applyPodEvent's suspend
	// branch cancels the stream (and the pod closing the connection ends it
	// anyway via handleRunnerEvent's StreamEnded path).
	if key.Matches(msg, m.keys.Suspend) {
		sel := m.selectedRowSession()
		if sel != nil && sel.DashStatus != StatusSuspended {
			for i := range m.sessions {
				if m.sessions[i].ID() == sel.ID() {
					m.sessions[i].PendingAction = "suspend"
					break
				}
			}
			return m, m.suspendCmd(session.Ref{ID: sel.ID()})
		}
		return m, nil
	}

	// Resume — scale a suspended session's pod back up.
	if key.Matches(msg, m.keys.Resume) {
		sel := m.selectedRowSession()
		if sel != nil && sel.DashStatus == StatusSuspended {
			for i := range m.sessions {
				if m.sessions[i].ID() == sel.ID() {
					m.sessions[i].PendingAction = "resume"
					break
				}
			}
			return m, m.resumeCmd(session.Ref{ID: sel.ID()})
		}
		return m, nil
	}

	// Destroy — irreversible; gate behind a confirm dialog.
	if key.Matches(msg, m.keys.Destroy) {
		sel := m.selectedRowSession()
		if sel != nil {
			m.confirm = &confirmPrompt{
				message: "Destroy " + sel.DisplayTitle() + "?  This deletes the pod and PVC and cannot be undone.",
				action:  m.destroyCmd(session.Ref{ID: sel.ID()}),
				id:      sel.ID(),
			}
		}
	}
	return m, nil
}

func (m *Model) handleFilterKey(ks string) (tea.Model, tea.Cmd) {
	switch ks {
	case "esc":
		// Clear filter and exit filtering mode.
		m.filtering = false
		m.filterBuf = ""
		m.filter = ""
		m.clampCursor()
		return m, nil

	case "enter":
		// Commit the filter and drop back to list navigation.
		m.filter = m.filterBuf
		m.filtering = false
		m.clampCursor()
		return m, nil

	case "backspace", "delete":
		if r, size := utf8.DecodeLastRuneInString(m.filterBuf); r != utf8.RuneError {
			m.filterBuf = m.filterBuf[:len(m.filterBuf)-size]
		}

	default:
		// Arrow keys navigate while the filter buffer captures text, so letters
		// like j/k/g stay typeable in the query. Clamp against visibleRows() —
		// group headers count as rows, so the old visibleSessions() bound left the
		// last rows of a grouped list unreachable while filtering.
		if ks == "down" {
			rows := m.visibleRows()
			if m.cursor < len(rows)-1 {
				m.cursor++
			}
			return m, nil
		}
		if ks == "up" {
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		}
		if len(ks) == 1 && ks[0] >= 32 && ks[0] < 127 {
			m.filterBuf += ks
		}
	}

	// Reset cursor to top when filter changes.
	m.cursor = 0
	m.clampCursor()
	return m, nil
}

// handleRenameKey routes keys to the rename buffer while the rename overlay is
// open: enter commits, esc cancels, backspace deletes, printable runes append.
func (m *Model) handleRenameKey(ks string) (tea.Model, tea.Cmd) {
	switch ks {
	case "esc":
		m.renaming = false
		m.renameBuf = ""
	case "enter":
		m.commitRename()
	case "backspace", "delete":
		if r, size := utf8.DecodeLastRuneInString(m.renameBuf); r != utf8.RuneError {
			m.renameBuf = m.renameBuf[:len(m.renameBuf)-size]
		}
	default:
		// Accept a single printable ASCII character (matches filter input).
		if len(ks) == 1 && ks[0] >= 32 && ks[0] < 127 {
			m.renameBuf += ks
		}
	}
	return m, nil
}
