package dashboard

import (
	"context"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// submitTextMsg triggers an automatic submission of the given prompt text.
type submitTextMsg struct{ text string }

// scrollbarDragTo handles a left-button press/drag at coordinates relative to
// the transcript content's top-left (the caller subtracts the modal's inner
// origin). If it lands on the scrollbar column over the body it jumps the scroll
// position proportionally (drag-to-position) and returns true; otherwise it
// returns false so the event falls through to normal handling. It is the inverse
// of kit.Scrollbar's pos math: a row near the top maps to offset 0, near the
// bottom to the max offset.
func (m *TranscriptModel) scrollbarDragTo(relX, relY int) bool {
	// bodyTop comes from the region stack (header + divider above the body), so
	// this hit-test follows the same band geometry the renderer uses; the
	// scrollbar sits in the rightmost body column (bodyView reserves m.width-1 for
	// content, +1 for the bar).
	bodyTop := m.bodyTop()
	bodyH := m.body.Height()
	if relX != m.width-1 || relY < bodyTop || relY >= bodyTop+bodyH {
		return false
	}
	maxOffset := m.body.TotalHeight() - bodyH
	if maxOffset <= 0 {
		return true // on the bar, but nothing to scroll
	}
	denom := bodyH - 1
	if denom < 1 {
		denom = 1
	}
	frac := float64(relY-bodyTop) / float64(denom)
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}
	target := int(math.Round(frac * float64(maxOffset)))
	m.body.ScrollBy(target - m.body.Offset())
	return true
}

// --------------------------------------------------------------------------
// Key handling
// --------------------------------------------------------------------------

// Permission grace gate (chat-rendering §2.6): an answer key resolves the inline
// permission box only once input has been quiet for permissionGraceQuiet since
// both the box appeared and the previous keystroke — so a keystroke already in
// flight when the box pops can't auto-approve/deny it. A hard cap makes the box
// answerable after permissionGraceCap regardless, so a held/repeating key can't
// lock it out forever.
const (
	permissionGraceQuiet = 250 * time.Millisecond
	permissionGraceCap   = 1500 * time.Millisecond
)

// permissionAnswerable reports whether the pending permission may be resolved by
// the current keystroke. prevKeyAt is the time of the previous key event.
func (m *TranscriptModel) permissionAnswerable(prevKeyAt time.Time) bool {
	if m.pending == nil {
		return false
	}
	now := nowFunc()
	if now.Sub(m.pending.since) >= permissionGraceCap {
		return true
	}
	quietSince := m.pending.since
	if prevKeyAt.After(quietSince) {
		quietSince = prevKeyAt
	}
	return now.Sub(quietSince) >= permissionGraceQuiet
}

// tctx is the transcript's active input sub-context — the overlay/mode that
// owns keys once the globals (help, esc, detach, mode/search toggles) have had
// their turn. Resolution order is fixed by activeSubContext and mirrors the old
// if-chain: search → permission → palette → normal → compose.
type tctx int

const (
	tctxSearch     tctx = iota // m.search.open
	tctxPermission             // m.pending != nil
	tctxPalette                // m.paletteOpen()
	tctxNormal                 // m.vimEnabled && m.imode == modeNormal
	tctxCompose                // default: the prompt collects keys
)

// activeSubContext resolves the current transcript sub-context. Order is
// load-bearing: search preempts a pending permission (both can be open at once),
// and NORMAL only engages when no overlay claims the keys. Help (m.showHelp) is
// NOT a sub-context — it is closed ahead of everything in handleKey.
func (m *TranscriptModel) activeSubContext() tctx {
	switch {
	case m.search.open:
		return tctxSearch
	case m.pending != nil:
		return tctxPermission
	case m.paletteOpen():
		return tctxPalette
	case m.vimEnabled && m.imode == modeNormal:
		return tctxNormal
	default:
		return tctxCompose
	}
}

// transcriptGlobalTable is the transcript's ordered global binding table: keys
// that fire regardless of sub-context, so shift+tab cycles the permission mode
// even while a permission is pending and `?` opens help even while search is
// open. Table order IS precedence (first match wins). space reports
// handled=false when there are no subagent cards so it falls through to typing a
// space in the composer.
func transcriptGlobalTable() []boundAction[*TranscriptModel] {
	return []boundAction[*TranscriptModel]{
		// `?` opens the help when the prompt is empty (otherwise it types).
		{
			binding: key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
			when:    func(m *TranscriptModel) bool { return m.input.Value() == "" },
			run:     func(m *TranscriptModel, _ tea.KeyPressMsg) (tea.Cmd, bool) { m.openHelp(); return nil, true },
		},
		// esc, in priority order: close an open overlay; steer the running turn
		// with a queued prompt (interrupt + inject); interrupt a running turn
		// outright; stop an idle driver; leave INSERT for NORMAL. The order lives
		// in one place — escCascade (modes.go) — which escapeConsumes reads too, so
		// the two can't drift. Run the first step that applies. When none apply the
		// App intercepts esc as a detach before delegation (see escapeConsumes);
		// ctrl+] / ctrl+4 always detach there.
		{
			binding: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "interrupt / steer / close")),
			run: func(m *TranscriptModel, msg tea.KeyPressMsg) (tea.Cmd, bool) {
				for _, step := range m.escCascade() {
					if step.applies() {
						return step.run(msg), true
					}
				}
				// Vim off: a bare esc has no local meaning here. escapeConsumes
				// already returned false for this case, so the App intercepted esc as
				// a detach and this run isn't reached — swallowing is a safe fallback.
				return nil, true
			},
		},
		{
			binding: key.NewBinding(key.WithKeys("ctrl+]", "ctrl+4"), key.WithHelp("ctrl+]", "detach")),
			run: func(m *TranscriptModel, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				if m.queuedPrompt != "" {
					return m.queueSteer(), true
				}
				return nil, true
			},
		},
		// space on an empty prompt collapses/expands all subagent cards; with no
		// cards to toggle it reports unhandled so the key types a space.
		{
			binding: key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "collapse agents")),
			when:    func(m *TranscriptModel) bool { return m.input.Value() == "" },
			run:     func(m *TranscriptModel, _ tea.KeyPressMsg) (tea.Cmd, bool) { return nil, m.toggleSubagents() },
		},
		// shift+tab cycles the permission mode (per-attach; reflected in the status
		// line's mode row and applied to the next turn's StartTurn).
		{
			binding: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "permission mode")),
			run:     func(m *TranscriptModel, _ tea.KeyPressMsg) (tea.Cmd, bool) { m.mode = m.mode.next(); return nil, true },
		},
		// ctrl+f opens in-transcript search.
		{
			binding: key.NewBinding(key.WithKeys("ctrl+f"), key.WithHelp("ctrl+f", "search")),
			run:     func(m *TranscriptModel, _ tea.KeyPressMsg) (tea.Cmd, bool) { m.openSearch(); return nil, true },
		},
	}
}

// transcriptComposeTable is the compose sub-context's ordered binding table:
// the keys that act on the draft prompt before it falls through to the textinput
// and scroll handlers.
func transcriptComposeTable() []boundAction[*TranscriptModel] {
	return []boundAction[*TranscriptModel]{
		// ctrl+o toggles the most recent expandable block (a tool card's output or a
		// capped thinking block) — the Claude-Code idiom. It is expand-ONLY and
		// ungated by composer content: a draft in the box no longer surprise-opens
		// $EDITOR (that moved to ctrl+e below). When nothing is expandable
		// toggleLatestExpandable returns false and ctrl+o is a swallowed no-op.
		{
			binding: key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "expand output")),
			run: func(m *TranscriptModel, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.toggleLatestExpandable()
				return nil, true
			},
		},
		// ctrl+e composes the current draft in $EDITOR (slice 5g). Split out from
		// ctrl+o so expanding a tool card and opening your editor are distinct keys.
		{
			binding: key.NewBinding(key.WithKeys("ctrl+e"), key.WithHelp("ctrl+e", "compose in $EDITOR")),
			run: func(m *TranscriptModel, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				return m.openEditorPrompt(), true
			},
		},
		// Multi-line composition: shift/alt+enter inserts a newline directly into
		// the input so multi-line prompts can be edited without leaving the chat.
		{
			binding: key.NewBinding(key.WithKeys("shift+enter", "alt+enter"), key.WithHelp("shift+enter", "newline")),
			run: func(m *TranscriptModel, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				m.input.SetValue(m.input.Value() + "\n")
				m.input.CursorEnd()
				m.layout() // the box grew a row — re-reserve body height
				return nil, true
			},
		},
		{
			binding: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
			run: func(m *TranscriptModel, _ tea.KeyPressMsg) (tea.Cmd, bool) {
				// `!cmd` runs a one-shot shell; otherwise send the prompt as a turn.
				if val := strings.TrimSpace(m.input.Value()); strings.HasPrefix(val, "!") {
					cmd := m.runShell(strings.TrimPrefix(val, "!"))
					m.input.Reset()
					return cmd, true
				}
				return m.submit(), true
			},
		},
		// ↑/↓ in the composer own history recall and cursor movement (§2d) — they
		// are ALWAYS consumed here and NEVER scroll the transcript (the wheel,
		// pgup/pgdn, ctrl+u/d, and vim NORMAL j/k own scrolling). ↑: keep walking
		// history when already navigating; else move the cursor up a logical line
		// when it isn't on the first line; else enter recall (saving the draft), or
		// a consumed no-op when there's no history. ↓ mirrors it: walk newer while
		// navigating (past the newest restores the draft); else move the cursor
		// down a line; else a consumed no-op on the last line.
		{
			binding: key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "prev prompt / cursor up")),
			run: func(m *TranscriptModel, msg tea.KeyPressMsg) (tea.Cmd, bool) {
				switch {
				case m.histIdx >= 0:
					m.histPrev() // already navigating: step older (clamps at oldest)
				case m.input.Line() > 0:
					var cmd tea.Cmd
					m.input, cmd = m.input.Update(msg) // cursor up within a multi-line draft
					return cmd, true
				default:
					m.histPrev() // first line / empty: enter recall (no-op when no history)
				}
				return nil, true
			},
		},
		{
			binding: key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "next prompt / cursor down")),
			run: func(m *TranscriptModel, msg tea.KeyPressMsg) (tea.Cmd, bool) {
				switch {
				case m.histIdx >= 0:
					m.histNext() // navigating: step newer (past newest restores the draft)
				case m.input.Line() < m.input.LineCount()-1:
					var cmd tea.Cmd
					m.input, cmd = m.input.Update(msg) // cursor down within a multi-line draft
					return cmd, true
				default:
					// last line (or single-line): consumed no-op — never scrolls.
				}
				return nil, true
			},
		},
	}
}

// recordPrompt appends a user-submitted prompt to the recall history, collapsing
// a consecutive duplicate, and resets any in-progress recall (§2d). Called only
// from the user-origin submit() path so the auto initialPrompt and driver ticks
// are excluded.
func (m *TranscriptModel) recordPrompt(text string) {
	if n := len(m.promptHistory); n == 0 || m.promptHistory[n-1] != text {
		m.promptHistory = append(m.promptHistory, text)
	}
	m.resetHistoryNav()
}

// resetHistoryNav clears the recall cursor and its saved draft/shown state
// without touching the composer text. Invoked on submit, on paging past the
// newest entry, and when the user edits a recalled prompt.
func (m *TranscriptModel) resetHistoryNav() {
	m.histIdx = -1
	m.histDraft = ""
	m.histShown = ""
}

// histPrev steps one entry older in the prompt history (↑). The first step saves
// the current composer text as the draft (empty is valid) and shows the newest
// entry; further steps walk older, clamping at the oldest. Returns whether a
// recall happened (false when there is no history); the compose caller consumes
// ↑ regardless — it never falls through to scrollKey.
func (m *TranscriptModel) histPrev() bool {
	if len(m.promptHistory) == 0 {
		return false
	}
	switch {
	case m.histIdx < 0:
		m.histDraft = m.input.Value() // entering nav: preserve the draft
		m.histIdx = 0
	case m.histIdx < len(m.promptHistory)-1:
		m.histIdx++
	default:
		// already at the oldest entry — clamp, but stay in nav.
	}
	m.showHistoryEntry()
	return true
}

// histNext steps one entry newer in the prompt history (↓). Paging forward past
// the newest entry restores the saved draft and exits nav. Returns whether it
// navigated (false when not in nav); the compose caller consumes ↓ regardless —
// it never falls through to scrollKey.
func (m *TranscriptModel) histNext() bool {
	if m.histIdx < 0 {
		return false
	}
	if m.histIdx == 0 {
		draft := m.histDraft
		m.resetHistoryNav()
		m.input.SetValue(draft)
		m.input.CursorEnd()
		m.layout()
		return true
	}
	m.histIdx--
	m.showHistoryEntry()
	return true
}

// showHistoryEntry loads the entry at the current cursor (0 = newest) into the
// composer and records it as histShown so a later edit can be detected.
func (m *TranscriptModel) showHistoryEntry() {
	entry := m.promptHistory[len(m.promptHistory)-1-m.histIdx]
	m.input.SetValue(entry)
	m.input.CursorEnd()
	m.histShown = entry
	m.layout() // the box may have grown/shrunk rows — re-reserve body height
}

func (m *TranscriptModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	// Track inter-key timing for the permission grace gate (chat-rendering §2.6).
	// prevKeyAt is republished on the model so permissionKey can read it.
	prevKeyAt := m.lastKeyAt
	m.lastKeyAt = nowFunc()
	m.prevKeyAt = prevKeyAt
	// Grouped help overlay: ↑/↓ + space drive it; any other key closes it. It
	// preempts everything else (not a sub-context) — closed ahead of the globals.
	if m.showHelp {
		if m.helpUI.handleKey(key) {
			return m, nil
		}
		m.showHelp = false
		return m, nil
	}
	// /model picker overlay: a full-capture overlay like showHelp — closed ahead
	// of the globals so a bare esc closes it instead of detaching (escapeConsumes
	// mirrors this). esc closes; the pure grammar handles nav/digit/enter; other
	// keys are swallowed (CC parity: numbered model selection).
	if m.modelPicker.open {
		return m, m.modelPickerKeyHandler(msg)
	}

	// Globals run BEFORE the sub-context delegation: e.g. shift+tab cycles the
	// permission mode even while a permission is pending, and `?` on an empty
	// composer opens help even while search is open (§2a input contexts). Table
	// order is precedence; a global that reports unhandled (space with no cards)
	// falls through to the sub-context below.
	// (ctrl+c never reaches here: the App intercepts it as a global quit before
	// delegating.)
	if cmd, handled := dispatchKey(m, transcriptGlobalTable(), msg); handled {
		return m, cmd
	}

	switch m.activeSubContext() {
	case tctxSearch:
		cmd, _ := m.searchKey(msg)
		return m, cmd
	case tctxPermission:
		return m, m.permissionKey(msg)
	case tctxPalette:
		// Slash palette: when the prompt starts with "/", keys drive the palette.
		cmd, _ := m.paletteKey(msg)
		return m, cmd
	case tctxNormal:
		// NORMAL mode (vim modal editing on) owns the keyboard once the overlays
		// and permission prompts above have had their turn: i/a enter INSERT,
		// j/k/g/G scroll, / searches, q detaches, and every other key is swallowed
		// so the blurred prompt never collects stray letters. With vim off imode is
		// pinned to INSERT, so this never engages and keys flow to the prompt.
		cmd, _ := m.normalKey(key, msg)
		return m, cmd
	default: // tctxCompose
		return m.composeKey(msg)
	}
}

// permissionKey handles a key while an inline permission box is pending. The
// key grammar for BOTH variants (plan r/a/enter, tool nav/number/accelerators,
// ctrl+o diff toggle) lives in the §2a permPrompt component's HandleKey
// (permprompt.go); this drives the model-owned side effects it decides on. The
// grace gate reads m.prevKeyAt (pinned by permission_clock_test.go) and is
// passed to HandleKey — it stays model-side. The numbered-option grammar still
// bottoms out in permPromptKey (permprompt_test.go). j/k and other non-grammar
// keys fall through to scrollKey so the transcript stays scrollable behind the
// prompt.
func (m *TranscriptModel) permissionKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	answerable := m.permissionAnswerable(m.prevKeyAt)
	if act, handled := m.permComp().HandleKey(key, answerable); handled {
		switch act.kind {
		case permActResolve:
			if act.setMode {
				// approve & switch to accept-edits for subsequent turns.
				m.mode = modeAcceptEdits
			}
			return m.resolvePermission(act.allow, act.scope)
		case permActToggleDiff:
			m.showDiff = !m.showDiff
			m.layout()
			return nil
		default: // permActNone: nav / grace-swallow / consumed-no-op
			return nil
		}
	}
	m.scrollKey(key)
	return nil
}

// composeKey handles a key in the default compose sub-context: the compose
// binding table (ctrl+o / newline / send), then the shared scroll keys, then the
// textinput fallthrough that also opens the slash palette on the first "/".
func (m *TranscriptModel) composeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if cmd, handled := dispatchKey(m, transcriptComposeTable(), msg); handled {
		return m, cmd
	}
	if m.scrollKey(msg.String()) {
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Editing while a recalled history entry is showing exits recall: the shown
	// entry becomes the live draft (§2d — any non-↑/↓ key that changes the
	// composer clears nav state, but is otherwise handled normally).
	if m.histIdx >= 0 && m.input.Value() != m.histShown {
		m.resetHistoryNav()
	}
	// Typing the first "/" opens the palette; relayout so it appears immediately.
	if m.paletteOpen() {
		m.cmdSel = 0
		m.layout()
	}
	return m, cmd
}

// scrollKey moves the viewport for navigation keys, returning true if handled.
// NB: the compose sub-context no longer routes ↑/↓ here — there they own history
// recall and cursor movement and are always consumed. The "up"/"down" cases stay
// for the remaining callers: the plan-permission scroll fallback (permissionKey)
// and the vim NORMAL arrow fallthrough (normalKey).
func (m *TranscriptModel) scrollKey(key string) bool {
	page := max(1, m.body.Height())
	switch key {
	case "pgup":
		m.body.ScrollBy(-page)
	case "pgdown", "pgdn":
		m.body.ScrollBy(page)
	case "ctrl+u":
		m.body.ScrollBy(-max(1, page/2))
	case "ctrl+d":
		m.body.ScrollBy(max(1, page/2))
	case "up":
		m.body.ScrollBy(-1)
	case "down":
		m.body.ScrollBy(1)
	case "home":
		m.body.GotoTop()
	case "end":
		m.body.GotoBottom()
	default:
		return false
	}
	return true
}

// submit sends the current prompt input as a new turn. If a turn is already
// running, the prompt is queued instead and sent when the turn completes.
func (m *TranscriptModel) submit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	// Record the prompt for ↑/↓ recall (§2d). submit() is the sole user-origin
	// entry point — whether the prompt sends now or queues behind a running turn,
	// the user authored it here. The auto initialPrompt and driver ticks reach
	// submitText directly, so they never land in history.
	m.recordPrompt(text)
	// A manually typed prompt means the user is taking over from a running
	// /loop or /goal; stop the driver so it doesn't keep firing turns underneath
	// them. (Loop ticks and goal continuations submit via submitText, not here,
	// so they don't self-cancel.) For the runner-owned driver this returns the
	// DELETE Cmd, batched with the manual turn.
	dis := m.stopDriver("autopilot stopped — you took over")
	if m.turnActive {
		m.queuedPrompt = text
		m.input.Reset()
		return dis
	}
	m.input.Reset()
	return tea.Batch(dis, m.submitText(text))
}

// queueSteer steers the active turn with the queued prompt: interrupt now, and
// let the EventTurnInterrupted handler submit the queued prompt once the runner
// has torn the turn down. It must NOT submit concurrently — a new turn POSTed
// while the old one is still active is rejected with 409 (R4 single-active-turn
// gate). Keeping queuedPrompt set defers the submit to interrupt-confirmation,
// sequencing it correctly. (If the interrupt is somehow lost, the queued prompt
// still flushes when the turn finishes naturally — EventTurnCompleted.)
func (m *TranscriptModel) queueSteer() tea.Cmd {
	if m.queuedPrompt == "" {
		return nil
	}
	if !m.turnActive {
		// No turn to interrupt (it already ended) — just send the queued prompt.
		text := m.queuedPrompt
		m.queuedPrompt = ""
		return m.submitText(text)
	}
	return m.interruptTurn()
}

// interruptTurn cancels the active turn. A bare esc maps here while a turn runs;
// the runner answers with EventTurnInterrupted, which clears turnActive and
// appends the "[interrupted]" marker (and flushes any queued steer prompt). The
// turn id targets this exact turn; when it is still empty (esc before
// turn.started landed) the runner falls back to its sole active turn.
func (m *TranscriptModel) interruptTurn() tea.Cmd {
	// An esc interrupt is the user reclaiming control: stop any /loop or /goal
	// driver so it doesn't relaunch a turn after this one is torn down. For the
	// runner-owned driver this issues the DELETE (batched with the interrupt).
	dis := m.stopDriver("autopilot stopped")
	if !m.turnActive {
		return dis
	}
	// Capture into locals: the closure runs off the Update goroutine, and
	// m.client is swapped by tReconnectedMsg (same pattern as resolvePermission).
	client := m.client
	sref := m.ref
	ref := session.TurnRef{Session: m.ref.ID, Turn: m.activeTurnID}
	return tea.Batch(dis, func() tea.Msg {
		if err := client.InterruptTurn(context.Background(), sref, ref); err != nil {
			return interruptFailedMsg{err: err}
		}
		return nil
	})
}

// interruptFailedMsg surfaces an interrupt request failure so a future
// regression is visible (a dim notice) instead of a silent no-op.
type interruptFailedMsg struct{ err error }

// submitText starts a turn for the given prompt text, recording the user block
// and entering the busy state. Shared by interactive submit and the auto-
// submitted initial prompt.
func (m *TranscriptModel) submitText(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	m.dropTrailingFooter() // A2.2: only the latest turn keeps a footer
	m.appendBlock(blockUser, text)
	m.beginTurn()
	return tea.Batch(startTurnCmd(m.client, m.ref, text, m.mode.apiValue(), m.modelOverride, m.effortOverride, m.advisorEnabled), m.maybeStartWorking())
}
