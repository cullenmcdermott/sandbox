// Package composer is a public, reusable chat input: a multi-line prompt box
// with bracketed paste, responsive growth (grows with content up to a cap, then
// scrolls internally), prompt history with draft preservation, queue-while-busy
// with editable queued steering, and an escape cascade (steer a queued prompt →
// interrupt a running turn → detach when idle). It is a Charm Bubble Tea v2
// component built on bubbles/v2 textarea; it imports nothing under internal/.
//
// The keystroke-routing and escape cascade are derived, behavior-for-behavior,
// from the dashboard's transcript_input.go / esc_cascade_test.go. The host wires
// the actual side effects (turn submission, interruption, permission decisions,
// detach) through callbacks — the composer owns the keyboard and the draft, not
// the transport.
package composer

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// State is the composer's readiness. It gates what Enter does and what the
// escape cascade means.
type State int

const (
	// StateReady is idle: Enter submits, Esc detaches.
	StateReady State = iota
	// StateBusy means a turn is running: Enter arms an (editable) queued steer,
	// Esc steers the queued prompt or interrupts the turn.
	StateBusy
	// StateDisabled ignores all keystrokes (e.g. while reconnecting).
	StateDisabled
)

const (
	// defaultMaxRows caps how tall the box grows before it scrolls internally.
	defaultMaxRows = 6
	// permissionGraceQuiet / permissionGraceCap gate answering a pending
	// permission so type-ahead (keys already in flight when the request pops)
	// can't auto-approve or -deny it. Mirrors the dashboard's grace constants.
	permissionGraceQuiet = 250 * time.Millisecond
	permissionGraceCap   = 1500 * time.Millisecond
)

// Option configures a Model at construction.
type Option func(*Model)

// WithNow injects the clock (for deterministic tests). Defaults to time.Now.
func WithNow(fn func() time.Time) Option { return func(m *Model) { m.now = fn } }

// WithMaxRows sets the growth cap (rows) before the box scrolls. Defaults to 6.
func WithMaxRows(n int) Option {
	return func(m *Model) {
		if n > 0 {
			m.maxRows = n
		}
	}
}

// WithPlaceholder sets the hint shown while the box is empty.
func WithPlaceholder(s string) Option { return func(m *Model) { m.ta.Placeholder = s } }

// WithSubmit registers the callback fired when a ready prompt is sent (Enter, or
// a queued steer flushed when the turn ends). The text is trimmed and non-empty.
func WithSubmit(fn func(text string)) Option { return func(m *Model) { m.onSubmit = fn } }

// WithSteer registers the callback fired when Esc steers a queued prompt into a
// running turn (interrupt + inject). The host performs the interrupt+submit.
func WithSteer(fn func(text string)) Option { return func(m *Model) { m.onSteer = fn } }

// WithInterrupt registers the callback fired when Esc interrupts a running turn
// with no queued prompt.
func WithInterrupt(fn func()) Option { return func(m *Model) { m.onInterrupt = fn } }

// WithApprove / WithDeny register the grace-gated permission decision callbacks
// ("a" approves once, "d" denies). Keys arriving before the type-ahead grace
// elapses go to the draft instead.
func WithApprove(fn func(scope string)) Option { return func(m *Model) { m.onApprove = fn } }
func WithDeny(fn func()) Option                { return func(m *Model) { m.onDeny = fn } }

// WithDetach registers the callback fired when Esc is pressed with nothing to
// steer or interrupt (an idle escape).
func WithDetach(fn func()) Option { return func(m *Model) { m.onDetach = fn } }

// Model is the composer. Build one with New; drive it with Update; render with
// View.
type Model struct {
	ta      textarea.Model
	state   State
	width   int
	maxRows int

	// queued steer: while busy, Enter arms this and the draft stays editable in
	// the box until the turn ends (flush) or Esc steers it now.
	queued bool

	// prompt history + draft-preserving recall (↑/↓).
	history   []string
	histIdx   int
	histDraft string
	histShown string

	// permission anti-type-ahead grace gate.
	permPending bool
	permSince   time.Time
	lastKeyAt   time.Time

	// callbacks (all optional).
	onSubmit    func(string)
	onSteer     func(string)
	onInterrupt func()
	onApprove   func(scope string)
	onDeny      func()
	onDetach    func()

	now func() time.Time
}

// New builds a composer.
func New(opts ...Option) *Model {
	ta := textarea.New()
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "❯ "
		}
		return "  "
	})
	ta.Placeholder = "type a message…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)

	m := &Model{
		ta:      ta,
		maxRows: defaultMaxRows,
		histIdx: -1,
		now:     time.Now,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Init focuses the composer (drives the cursor blink).
func (m *Model) Init() tea.Cmd { return m.ta.Focus() }

// Focus / Blur / Focused proxy the textarea focus state.
func (m *Model) Focus() tea.Cmd { return m.ta.Focus() }
func (m *Model) Blur()          { m.ta.Blur() }
func (m *Model) Focused() bool  { return m.ta.Focused() }

// Value / SetValue / Reset expose the draft text.
func (m *Model) Value() string { return m.ta.Value() }
func (m *Model) SetValue(s string) {
	m.ta.SetValue(s)
	m.ta.CursorEnd()
	m.resize()
}
func (m *Model) Reset() {
	m.ta.Reset()
	m.resize()
}

// State / SetState read and set readiness. Setting Ready while a steer is queued
// flushes it as the next turn (the host wired it to submit).
func (m *Model) State() State { return m.state }
func (m *Model) SetState(s State) {
	prev := m.state
	m.state = s
	if prev == StateBusy && s == StateReady && m.queued {
		m.flushQueued()
	}
}

// Queued reports whether an editable steer prompt is armed behind a running turn.
func (m *Model) Queued() bool { return m.queued }

// SetPermissionPending arms (or disarms) the anti-type-ahead grace gate for a
// pending permission request. While pending, the "a"/"d" answer keys are only
// consumed once the user has paused (see permissionAnswerable); until then they
// type into the draft.
func (m *Model) SetPermissionPending(pending bool) {
	if pending && !m.permPending {
		m.permSince = m.now()
	}
	m.permPending = pending
}

// SetWidth sizes the composer and re-flows the box height.
func (m *Model) SetWidth(w int) {
	m.width = w
	m.ta.SetWidth(m.innerWidth())
	m.resize()
}

// Width reports the composer's outer width.
func (m *Model) Width() int { return m.width }

// Height reports the composer's rendered height in rows (the box grows with the
// draft up to the cap, plus one hint row).
func (m *Model) Height() int { return m.rows() + 1 }

// Update routes a message. KeyPressMsg drives the composer's grammar; everything
// else (bracketed paste, cursor blink) falls through to the textarea.
func (m *Model) Update(msg tea.Msg) (*Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		m.resize()
		return m, cmd
	}
	return m.handleKey(key)
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (*Model, tea.Cmd) {
	if m.state == StateDisabled {
		return m, nil
	}
	k := msg.String()
	prevKeyAt := m.lastKeyAt
	m.lastKeyAt = m.now()

	// A pending permission's answer keys are grace-gated: only a deliberate key
	// after a quiet gap answers it; keystrokes still in flight when the request
	// popped go to the draft (type-ahead protection).
	if m.permPending && (k == "a" || k == "d") && m.permissionAnswerable(prevKeyAt) {
		if k == "a" {
			if m.onApprove != nil {
				m.onApprove("once")
			}
		} else if m.onDeny != nil {
			m.onDeny()
		}
		return m, nil
	}

	switch k {
	case "enter":
		return m, m.onEnter()
	case "esc":
		return m, m.onEsc()
	case "shift+enter", "alt+enter":
		m.ta.SetValue(m.ta.Value() + "\n")
		m.ta.CursorEnd()
		m.resize()
		return m, nil
	case "up":
		return m, m.onUp(msg)
	case "down":
		return m, m.onDown(msg)
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	// Editing a recalled history entry exits recall.
	if m.histIdx >= 0 && m.ta.Value() != m.histShown {
		m.resetHistoryNav()
	}
	m.resize()
	return m, cmd
}

func (m *Model) onEnter() tea.Cmd {
	text := strings.TrimSpace(m.ta.Value())
	if text == "" {
		return nil
	}
	if m.state == StateBusy {
		// Arm an editable queued steer: the draft stays in the box for further
		// editing; it flushes as the next turn when the current one ends, or steers
		// now on Esc.
		m.queued = true
		return nil
	}
	m.recordPrompt(text)
	m.ta.Reset()
	m.resize()
	if m.onSubmit != nil {
		m.onSubmit(text)
	}
	return nil
}

// onEsc runs the input escape cascade: steer a queued prompt, else interrupt a
// running turn, else detach when idle.
func (m *Model) onEsc() tea.Cmd {
	switch {
	case m.queued:
		text := strings.TrimSpace(m.ta.Value())
		m.queued = false
		m.ta.Reset()
		m.resize()
		if text != "" && m.onSteer != nil {
			m.onSteer(text)
		}
		return nil
	case m.state == StateBusy:
		if m.onInterrupt != nil {
			m.onInterrupt()
		}
		return nil
	default:
		if m.onDetach != nil {
			m.onDetach()
		}
		return nil
	}
}

func (m *Model) flushQueued() {
	text := strings.TrimSpace(m.ta.Value())
	m.queued = false
	if text == "" {
		return
	}
	m.recordPrompt(text)
	m.ta.Reset()
	m.resize()
	if m.onSubmit != nil {
		m.onSubmit(text)
	}
}

// onUp / onDown: history recall with cursor-line awareness — ↑/↓ move the cursor
// within a multi-line draft, and navigate history only at the draft's first/last
// line. Always consumed (never scrolls the transcript).
func (m *Model) onUp(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case m.histIdx >= 0:
		m.histPrev()
	case m.ta.Line() > 0:
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		m.resize()
		return cmd
	default:
		m.histPrev()
	}
	return nil
}

func (m *Model) onDown(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case m.histIdx >= 0:
		m.histNext()
	case m.ta.Line() < m.ta.LineCount()-1:
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		m.resize()
		return cmd
	default:
		// last / single line: consumed no-op.
	}
	return nil
}

// ---- history ----------------------------------------------------------------

func (m *Model) recordPrompt(text string) {
	if n := len(m.history); n == 0 || m.history[n-1] != text {
		m.history = append(m.history, text)
	}
	m.resetHistoryNav()
}

func (m *Model) resetHistoryNav() {
	m.histIdx = -1
	m.histDraft = ""
	m.histShown = ""
}

func (m *Model) histPrev() {
	if len(m.history) == 0 {
		return
	}
	switch {
	case m.histIdx < 0:
		m.histDraft = m.ta.Value()
		m.histIdx = 0
	case m.histIdx < len(m.history)-1:
		m.histIdx++
	}
	m.showHistoryEntry()
}

func (m *Model) histNext() {
	if m.histIdx < 0 {
		return
	}
	if m.histIdx == 0 {
		draft := m.histDraft
		m.resetHistoryNav()
		m.ta.SetValue(draft)
		m.ta.CursorEnd()
		m.resize()
		return
	}
	m.histIdx--
	m.showHistoryEntry()
}

func (m *Model) showHistoryEntry() {
	entry := m.history[len(m.history)-1-m.histIdx]
	m.ta.SetValue(entry)
	m.ta.CursorEnd()
	m.histShown = entry
	m.resize()
}

// ---- permission grace gate --------------------------------------------------

// permissionAnswerable reports whether the pending permission may be resolved by
// the current keystroke. prevKeyAt is the time of the previous key event.
func (m *Model) permissionAnswerable(prevKeyAt time.Time) bool {
	if !m.permPending {
		return false
	}
	now := m.now()
	if now.Sub(m.permSince) >= permissionGraceCap {
		return true
	}
	quietSince := m.permSince
	if prevKeyAt.After(quietSince) {
		quietSince = prevKeyAt
	}
	return now.Sub(quietSince) >= permissionGraceQuiet
}

// ---- sizing / rendering -----------------------------------------------------

func (m *Model) innerWidth() int {
	// The prompt gutter ("❯ ") is 2 columns; reserve them so wrapping matches the
	// rendered content width.
	if w := m.width - 2; w > 1 {
		return w
	}
	return 1
}

// rows is the composer's current content height (1..maxRows), counting VISUAL
// (soft-wrapped) rows so a long paste grows the box until the cap.
func (m *Model) rows() int {
	w := m.ta.Width()
	var n int
	if w <= 0 {
		n = m.ta.LineCount()
	} else {
		for _, line := range strings.Split(m.ta.Value(), "\n") {
			wrapped := ansi.Hardwrap(ansi.Wordwrap(line, w, ""), w, true)
			n += strings.Count(wrapped, "\n") + 1
		}
	}
	if n < 1 {
		n = 1
	}
	if n > m.maxRows {
		n = m.maxRows
	}
	return n
}

func (m *Model) resize() { m.ta.SetHeight(m.rows()) }

// View renders the composer: the textarea box plus a dim state hint row.
func (m *Model) View() string {
	m.ta.SetWidth(m.innerWidth())
	m.ta.SetHeight(m.rows())
	return m.ta.View() + "\n" + m.hint()
}

func (m *Model) hint() string {
	dim := lipgloss.NewStyle().Foreground(theme.TextDim)
	var s string
	switch {
	case m.state == StateDisabled:
		s = "input disabled"
	case m.queued:
		s = "queued · esc to steer now · enter to re-queue"
	case m.state == StateBusy:
		s = "working… · enter to queue · esc to interrupt"
	default:
		s = "enter to send · shift+enter for newline"
	}
	if m.permPending {
		s = "permission pending · a approve · d deny"
	}
	return dim.Render(s)
}
