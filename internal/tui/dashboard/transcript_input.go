package dashboard

import (
	"context"
	"math"
	"strings"
	"time"

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
	// bodyTop = header(1) + divider(1); the scrollbar sits in the rightmost body
	// column (bodyView reserves m.width-1 for content, +1 for the bar).
	const bodyTop = 2
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

func (m *TranscriptModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	// Track inter-key timing for the permission grace gate (chat-rendering §2.6).
	prevKeyAt := m.lastKeyAt
	m.lastKeyAt = nowFunc()
	// Grouped help overlay: ↑/↓ + space drive it; any other key closes it.
	if m.showHelp {
		if m.helpUI.handleKey(key) {
			return m, nil
		}
		m.showHelp = false
		return m, nil
	}
	// `?` opens the help when the prompt is empty (otherwise it types).
	if key == "?" && m.input.Value() == "" {
		m.openHelp()
		return m, nil
	}
	// esc, in priority order: close an open overlay; steer the running turn with
	// a queued prompt (interrupt + inject); interrupt a running turn outright;
	// leave INSERT for NORMAL. When none apply the App intercepts esc as a detach
	// before delegation (see escapeConsumes); ctrl+] / ctrl+4 always detach there.
	if key == "esc" {
		if m.search.open {
			cmd, _ := m.searchKey(msg)
			return m, cmd
		}
		if m.paletteOpen() {
			cmd, _ := m.paletteKey(msg)
			return m, cmd
		}
		if m.queuedPrompt != "" {
			return m, m.queueSteer()
		}
		if m.turnActive {
			return m, m.interruptTurn() // also stops any running driver
		}
		// A /loop or /goal idle between ticks/turns: esc reclaims control and stops
		// the driver, honoring the chip's "esc to stop" contract even when there's
		// no live turn to interrupt (§1e item 5). Detach stays on ctrl+].
		if m.autopilot.active() {
			m.stopAutopilot("autopilot stopped")
			return m, nil
		}
		if m.vimEnabled && m.imode == modeInsert {
			m.enterNormal()
			return m, nil
		}
		// Vim off: a bare esc has no local meaning here. escapeConsumes already
		// returned false for this case, so the App intercepted esc as a detach and
		// this handler isn't reached — the return is just a safe fallthrough.
		return m, nil
	}
	if key == "ctrl+]" || key == "ctrl+4" {
		if m.queuedPrompt != "" {
			return m, m.queueSteer()
		}
		return m, nil
	}
	// space on an empty prompt collapses/expands all subagent cards.
	if key == "space" && m.input.Value() == "" && m.toggleSubagents() {
		return m, nil
	}
	// shift+tab cycles the permission mode (per-attach; reflected in the status
	// line's mode row and applied to the next turn's StartTurn).
	if key == "shift+tab" {
		m.mode = m.mode.next()
		return m, nil
	}

	// ctrl+f opens in-transcript search.
	if key == "ctrl+f" {
		m.openSearch()
		return m, nil
	}

	// (ctrl+c never reaches here: the App intercepts it as a global quit before
	// delegating. space-on-empty-prompt above covers card collapse.)

	if m.search.open {
		cmd, _ := m.searchKey(msg)
		return m, cmd
	}

	if m.pending != nil {
		// Grace gate: a key in flight when the box popped can't auto-answer it.
		answerable := m.permissionAnswerable(prevKeyAt)
		if m.pending.isPlan {
			switch key {
			case "r":
				// reject: keep plan mode, deny the plan.
				if !answerable {
					return m, nil
				}
				return m, m.resolvePermission(false)
			case "a":
				// approve, stay in plan mode.
				if !answerable {
					return m, nil
				}
				return m, m.resolvePermission(true)
			case "enter":
				// approve & switch to accept-edits for subsequent turns.
				if !answerable {
					return m, nil
				}
				m.mode = modeAcceptEdits
				return m, m.resolvePermission(true)
			}
			if m.scrollKey(key) {
				return m, nil
			}
			return m, nil
		}
		switch key {
		case "a":
			if !answerable {
				return m, nil
			}
			return m, m.resolvePermission(true)
		case "d":
			if !answerable {
				return m, nil
			}
			return m, m.resolvePermission(false)
		case "enter":
			m.showDiff = !m.showDiff
			m.layout()
			return m, nil
		}
		if m.scrollKey(key) {
			return m, nil
		}
		return m, nil
	}

	// Slash palette: when the prompt starts with "/", keys drive the palette.
	if m.paletteOpen() {
		cmd, _ := m.paletteKey(msg)
		return m, cmd
	}

	// NORMAL mode (vim modal editing on) owns the keyboard once the overlays and
	// permission prompts above have had their turn: i/a enter INSERT, j/k/g/G
	// scroll, / searches, q detaches, and every other key is swallowed so the
	// blurred prompt never collects stray letters. With vim off imode is pinned
	// to INSERT, so this never engages and keys flow to the prompt.
	if m.vimEnabled && m.imode == modeNormal {
		cmd, handled := m.normalKey(key, msg)
		if handled {
			return m, cmd
		}
	}

	// ctrl+o toggles the most recent tool card's output expansion when the
	// composer is empty (the Claude-Code idiom, and consistent with the other
	// prompt-empty-gated keys `?` and space) — you're reading the transcript, not
	// drafting. With text in the composer it keeps its $EDITOR-composition role, so
	// ctrl+o on a draft opens it in your editor (slice 5g).
	if key == "ctrl+o" {
		if m.input.Value() == "" && m.toggleLatestToolCard() {
			return m, nil
		}
		return m, m.openEditorPrompt()
	}

	// Multi-line composition: shift/alt+enter inserts a newline directly into
	// the input so multi-line prompts can be edited without leaving the chat.
	if key == "shift+enter" || key == "alt+enter" {
		m.input.SetValue(m.input.Value() + "\n")
		m.input.CursorEnd()
		m.layout() // the box grew a row — re-reserve body height
		return m, nil
	}

	if key == "enter" {
		// `!cmd` runs a one-shot shell; otherwise send the prompt as a turn.
		if val := strings.TrimSpace(m.input.Value()); strings.HasPrefix(val, "!") {
			cmd := m.runShell(strings.TrimPrefix(val, "!"))
			m.input.Reset()
			return m, cmd
		}
		return m, m.submit()
	}
	if m.scrollKey(key) {
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Typing the first "/" opens the palette; relayout so it appears immediately.
	if m.paletteOpen() {
		m.cmdSel = 0
		m.layout()
	}
	return m, cmd
}

// scrollKey moves the viewport for navigation keys, returning true if handled.
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
	// A manually typed prompt means the user is taking over from a running
	// /loop or /goal; stop the driver so it doesn't keep firing turns underneath
	// them. (Loop ticks and goal continuations submit via submitText, not here,
	// so they don't self-cancel.)
	m.stopAutopilot("autopilot stopped — you took over")
	if m.turnActive {
		m.queuedPrompt = text
		m.input.Reset()
		return nil
	}
	m.input.Reset()
	return m.submitText(text)
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
	// driver so it doesn't relaunch a turn after this one is torn down.
	m.stopAutopilot("autopilot stopped")
	if !m.turnActive {
		return nil
	}
	// Capture into locals: the closure runs off the Update goroutine, and
	// m.client is swapped by tReconnectedMsg (same pattern as resolvePermission).
	client := m.client
	sref := m.ref
	ref := session.TurnRef{Session: m.ref.ID, Turn: m.activeTurnID}
	return func() tea.Msg {
		if err := client.InterruptTurn(context.Background(), sref, ref); err != nil {
			return interruptFailedMsg{err: err}
		}
		return nil
	}
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
