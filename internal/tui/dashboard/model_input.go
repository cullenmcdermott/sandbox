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

// dctx is the dashboard's active input context — the overlay/mode that owns keys
// before the list's binding table gets its turn. Resolution order is fixed by
// activeContext and mirrors the old intercept if-chain.
type dctx int

const (
	dctxConfirm   dctx = iota // m.confirm != nil
	dctxHelp                  // m.showHelp
	dctxSwitcher              // m.switcher.open
	dctxPermQueue             // m.permQueue.open
	dctxFilter                // m.filtering
	dctxRename                // m.renaming
	dctxConvert               // m.convert != nil
	dctxList                  // default: the session list's binding table
)

// activeContext resolves the current dashboard input context. Order is
// load-bearing: it is exactly the order the old handleKey intercepted each
// overlay/mode ahead of the list bindings.
func (m *Model) activeContext() dctx {
	switch {
	case m.confirm != nil:
		return dctxConfirm
	case m.showHelp:
		return dctxHelp
	case m.switcher.open:
		return dctxSwitcher
	case m.permQueue.open:
		return dctxPermQueue
	case m.filtering:
		return dctxFilter
	case m.renaming:
		return dctxRename
	case m.convert != nil:
		return dctxConvert
	default:
		return dctxList
	}
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	ks := msg.String()
	if ks != "g" {
		m.ggPending = false
	}

	switch m.activeContext() {
	case dctxConfirm:
		return m.confirmKey(ks)
	case dctxHelp:
		return m.helpKey(ks)
	case dctxSwitcher:
		cmd, _ := m.switcherKey(msg)
		return m, cmd
	case dctxPermQueue:
		cmd, _ := m.permQueueKey(msg)
		return m, cmd
	case dctxFilter:
		return m.handleFilterKey(ks)
	case dctxRename:
		return m.handleRenameKey(ks)
	case dctxConvert:
		return m, m.handleConvertKey(msg)
	default: // dctxList
		if cmd, handled := dispatchKey(m, m.dashListTable(), msg); handled {
			return m, cmd
		}
		return m, nil
	}
}

// confirmKey handles keys while a destructive-action confirmation captures the
// keyboard until resolved.
func (m *Model) confirmKey(ks string) (tea.Model, tea.Cmd) {
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

// helpKey drives the help overlay while it is open: ↑/↓ + space navigate the
// grouped surface; any other key closes it.
func (m *Model) helpKey(ks string) (tea.Model, tea.Cmd) {
	if m.helpUI.handleKey(ks) {
		return m, nil
	}
	m.showHelp = false
	return m, nil
}

// shortHelp returns the footer bindings for the dashboard screen, derived from
// the SAME list table that dispatches keys — so the footer can never advertise a
// key that isn't live (§2d). The footer only renders on the dashboard, so the
// list context is the right source.
func (m *Model) shortHelp() []key.Binding {
	return footerBindings(m, m.dashListTable())
}

// dashListTable is the session-list context's ordered binding table (dctxList).
// Table order IS precedence: it reproduces the old handleKey if-chain exactly,
// each run being the moved branch body. Footer entries carry a footerRank so the
// bottom band renders from these same bindings (see shortHelp).
func (m *Model) dashListTable() []boundAction[*Model] {
	return []boundAction[*Model]{
		// When the initial cluster seed failed, the list shows an error + retry in
		// place of skeleton bars. r re-issues the seed + watch (with no sessions to
		// resume, this takes precedence over the resume binding harmlessly).
		{
			binding: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "retry")),
			when:    func(m *Model) bool { return m.seedErr != nil },
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.seedErr = nil
				return tea.Batch(m.seedCmd(), m.startWatchCmd()), true
			},
		},
		// esc dismisses a lingering connect/action error in the detail pane. Overlays
		// and input modes are handled by earlier contexts, so a bare esc here is safe
		// to consume; when there's nothing to dismiss it falls through to the no-op
		// default.
		{
			binding: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "dismiss error")),
			when:    func(m *Model) bool { return m.connectErr != nil || m.actionErr != nil },
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.connectErr = nil
				m.actionErr = nil
				return nil, true
			},
		},
		// q opens the pending-permission queue when sessions are waiting. With an
		// empty queue this entry's when fails and q falls through to Quit below.
		{
			binding:    key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "perm queue")),
			when:       func(m *Model) bool { return len(m.permQueueItems()) > 0 },
			run:        func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) { m.openPermQueue(); return nil, true },
			footerRank: 7,
		},
		// Rename selected session.
		{
			binding: m.keys.Rename,
			run:     func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) { m.openRename(); return nil, true },
		},
		// Convert-to-branch (`b`): probe the selected session's worktree facts, then
		// open the modal (or toast when it has no worktree). Gated on the injected
		// WorktreeOps so it is a no-op in the library/unit-test default.
		{
			binding: m.keys.Branch,
			when:    func(m *Model) bool { return m.worktreeOps != nil },
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				sel := m.selectedSession()
				if sel != nil {
					return m.worktreeStatusCmd(sel.ID()), true
				}
				return nil, true
			},
		},
		// In group view, space expands/collapses the repo group at the cursor.
		{
			binding: key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "expand group")),
			when:    func(m *Model) bool { return m.groupView.open },
			run:     func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) { m.toggleRepoGroup(); return nil, true },
		},
		// ⌃G: jump the cursor to the next session needing attention (wraps; expands a
		// collapsed group in group view). Already worked from inside the chat modal
		// (app.go); this makes the documented key live on the dashboard screen too.
		{
			binding: m.keys.NextAttention,
			run:     func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) { m.jumpToNextNeedingAttention(); return nil, true },
		},
		// Quick-switcher (⌃K): fuzzy jump to any session. The overlay's own key
		// handling (switcherKey) takes over once open, including ctrl+k to close.
		{
			binding: m.keys.Switcher,
			run:     func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) { m.openSwitcher(); return nil, true },
		},
		// Quit. The complementary when-gate (empty queue) keeps the footer symmetric
		// with the q perm-queue entry above — exactly one of the two is ever a
		// rank-7 footer entry. Dispatch still relies on table order: with a non-empty
		// queue, q matches the perm-queue entry first; ctrl+c never reaches the Model
		// (the App intercepts it as a global quit).
		{
			binding:    m.keys.Quit,
			when:       func(m *Model) bool { return len(m.permQueueItems()) == 0 },
			run:        func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) { m.Cancel(); return tea.Quit, true },
			footerRank: 7,
		},
		// Help overlay (grouped, expandable; sourced from the keymap).
		{
			binding: m.keys.Help,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.helpUI = newHelpModel("keybindings", keymapCategories(m.keys))
				m.showHelp = true
				return nil, true
			},
			footerRank: 6,
		},
		// `g` is overloaded: a lone press toggles group view (handoff: Layout B is a
		// one-keystroke `g` toggle), while a quick `gg` jumps to the top. The first
		// `g` toggles group view and arms the chord; a second `g` reverts that
		// transient toggle and jumps to the top, so `gg` is a clean "go to top".
		{
			binding: key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "group view · gg top")),
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				if m.ggPending {
					m.ggPending = false
					m.toggleGroupView() // revert the toggle the first g applied
					m.cursor = 0
				} else {
					m.ggPending = true
					m.toggleGroupView()
				}
				return m.focusObserverSelected(), true
			},
		},
		{
			binding: key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.ggPending = false
				rows := m.visibleRows() // rows, not sessions: group headers count
				if len(rows) > 0 {
					m.cursor = len(rows) - 1
				}
				return m.focusObserverSelected(), true
			},
		},
		// Navigation: in group view, up/down move over rows in grouped order.
		// Moving the cursor onto a cold (evicted) session reconnects its observer on
		// demand so its live status returns (§1d reconnect-on-focus).
		{
			binding: m.keys.Up,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				if m.cursor > 0 {
					m.cursor--
				}
				return m.focusObserverSelected(), true
			},
			footerRank: 1,
		},
		{
			binding: m.keys.Down,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				visible := m.visibleRows()
				if m.cursor < len(visible)-1 {
					m.cursor++
				}
				return m.focusObserverSelected(), true
			},
			footerRank: 2,
		},
		// Filter start.
		{
			binding: m.keys.Filter,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.filtering = true
				m.filterBuf = ""
				return nil, true
			},
			footerRank: 4,
		},
		// Sort: cycle key (disabled in group view).
		{
			binding: m.keys.SortCycle,
			when:    func(m *Model) bool { return !m.groupView.open },
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.sortKey = m.sortKey.Next()
				m.sortSessions()
				m.clampCursor()
				return nil, true
			},
		},
		// Sort: flip direction (disabled in group view).
		{
			binding: m.keys.SortFlip,
			when:    func(m *Model) bool { return !m.groupView.open },
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.sortDir = m.sortDir.Flip()
				m.sortSessions()
				m.clampCursor()
				return nil, true
			},
		},
		// Attention-first toggle (D4): float Waiting/NeedsInput to the top.
		{
			binding: m.keys.AttentionToggle,
			when:    func(m *Model) bool { return !m.groupView.open },
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.attentionFirst = !m.attentionFirst
				m.clampCursor()
				return nil, true
			},
		},
		// Attach — flip to transcript screen via the App.
		{
			binding: m.keys.Attach,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				sel := m.selectedSession()
				if sel != nil {
					return func() tea.Msg { return attachMsg{sess: *sel} }, true
				}
				return nil, true
			},
			footerRank: 3,
		},
		// View the read-only activity feed (`v`) for an external-pane session — a
		// detached monitor of its normalized events, from which enter/a attaches
		// the pane. For a non-external (claude-sdk transcript) session `v` is a
		// no-op: it has its own attach screen and no feed.
		{
			binding: key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "view feed")),
			when: func(m *Model) bool {
				sel := m.selectedSession()
				return sel != nil && externalPaneBackend(sel.State.Backend)
			},
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				sel := m.selectedSession()
				if sel != nil {
					s := *sel
					return func() tea.Msg { return viewFeedMsg{sess: s} }, true
				}
				return nil, true
			},
		},
		// Approve / Deny — inline permission from the detail pane.
		{
			binding: m.keys.Approve,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				sel := m.selectedSession()
				if sel != nil && sel.DashStatus == StatusWaiting {
					return m.approveCmd(*sel, true), true
				}
				return nil, true
			},
		},
		{
			binding: m.keys.Deny,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				sel := m.selectedSession()
				if sel != nil && sel.DashStatus == StatusWaiting {
					return m.approveCmd(*sel, false), true
				}
				return nil, true
			},
		},
		// New session — delegated to the App, which owns the Creator.
		{
			binding: m.keys.New,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				return func() tea.Msg { return createSessionMsg{} }, true
			},
			footerRank: 5,
		},
		// Suspend — scale the selected session's pod to zero (recoverable).
		// The SSE stream is NOT cancelled here: if the suspend action fails, an
		// eager cancel would leave a still-running session deaf. On success the
		// cluster watch delivers the Suspended state and applyPodEvent's suspend
		// branch cancels the stream (and the pod closing the connection ends it
		// anyway via handleRunnerEvent's StreamEnded path).
		{
			binding: m.keys.Suspend,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				sel := m.selectedSession()
				if sel != nil && sel.DashStatus != StatusSuspended {
					for i := range m.sessions {
						if m.sessions[i].ID() == sel.ID() {
							m.sessions[i].PendingAction = "suspend"
							break
						}
					}
					return m.suspendCmd(session.Ref{ID: sel.ID()}), true
				}
				return nil, true
			},
		},
		// Resume — scale a suspended session's pod back up.
		{
			binding: m.keys.Resume,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				sel := m.selectedSession()
				if sel != nil && sel.DashStatus == StatusSuspended {
					for i := range m.sessions {
						if m.sessions[i].ID() == sel.ID() {
							m.sessions[i].PendingAction = "resume"
							break
						}
					}
					return m.resumeCmd(session.Ref{ID: sel.ID()}), true
				}
				return nil, true
			},
		},
		// Destroy — irreversible; gate behind a confirm dialog.
		{
			binding: m.keys.Destroy,
			run: func(m *Model, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				sel := m.selectedSession()
				if sel != nil {
					m.confirm = &confirmPrompt{
						message: "Destroy " + sel.DisplayTitle() + "?  This deletes the pod and PVC and cannot be undone.",
						action:  m.destroyCmd(session.Ref{ID: sel.ID()}),
						id:      sel.ID(),
					}
				}
				return nil, true
			},
		},
	}
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
