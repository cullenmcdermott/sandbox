// Package transcript is a public, event-sourced transcript component: feed it
// normalized session events (client.Event) via Apply and render the polished
// Sandbox chat transcript with Render, composing tui/chat items in a tui/list
// virtual list. It owns transcript state, event coalescing, tool pairing (by
// tool_use id / parent id), subagent routing, streaming, scrolling, follow mode,
// focus, expansion, responsive sizing, theme invalidation, and markdown-renderer
// caching. It deliberately keeps networking, connection lifecycle, and session
// ownership OUTSIDE the package: the host wires side effects (prompt submission,
// approval decisions, interruption, steering, detach) through callbacks and
// resolves the actual transport itself.
//
// The reducer is derived, behavior-for-behavior, from the dashboard's
// transcript_reduce.go + transcript_render.go, but imports nothing under
// internal/: it consumes the public re-exported event vocabulary from the
// `client` package and renders through the public tui/chat items.
package transcript

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/tui/chat"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

const (
	// bulletIndent is the width the "⏺ " assistant head bullet occupies; an
	// assistant body is rendered this many columns narrower so chat.Bullet's
	// hanging indent fits within the caller's width.
	bulletIndent = 2
	// diffCapLines bounds an edit-tool card's rendered diff.
	diffCapLines = 40
)

// entryKind classifies a committed block for the "calm" entry-gap spacing (a
// single blank line before each new top-level entry, while consecutive tool
// cards stay tight). It mirrors the dashboard's tblockKind spacing rules.
type entryKind int

const (
	entryNone entryKind = iota
	entryUser
	entryAssistant
	entryReasoning
	entrySubagent
	entryTodos
	entryTool
	entryAttachment // notices, elbows, footers — fold into the entry above
)

// startsEntry reports whether a block of kind cur following one of kind prev
// opens a new top-level entry (earning a leading blank line).
func startsEntry(prev, cur entryKind) bool {
	switch cur {
	case entryUser, entryAssistant, entryReasoning, entrySubagent, entryTodos:
		return true
	case entryTool:
		return prev != entryTool
	default:
		return false
	}
}

// committedItem is one settled transcript block plus its spacing classification.
type committedItem struct {
	item list.Item
	kind entryKind
}

// subEntry tracks a dispatched Task subagent: its rendered card plus a per-child
// index so a child tool.completed can find and mutate the right *chat.ToolCall.
type subEntry struct {
	item     *chat.SubagentItem
	children map[string]*chat.ToolCall // child toolUseId -> child call
}

// Option configures a Model at construction.
type Option func(*Model)

// WithNow injects the clock (for deterministic tests). Defaults to time.Now.
func WithNow(fn func() time.Time) Option { return func(m *Model) { m.now = fn } }

// WithBackend sets the display-ready backend label ("anthropic", "opencode", …)
// shown in the per-turn footer. Session ownership lives outside this package, so
// the host supplies it.
func WithBackend(name string) Option { return func(m *Model) { m.backend = name } }

// WithMarkdown toggles glamour markdown rendering of assistant bodies. Defaults
// to true; a host that wants plain text (or a deterministic test) passes false.
func WithMarkdown(on bool) Option { return func(m *Model) { m.markdown = on } }

// WithSubmit registers the host callback invoked when Submit sends a prompt. The
// package appends the optimistic user block; the host performs the transport.
func WithSubmit(fn func(text string)) Option { return func(m *Model) { m.onSubmit = fn } }

// WithApprove / WithDeny register the host callbacks for a permission decision.
func WithApprove(fn func(id, scope string)) Option { return func(m *Model) { m.onApprove = fn } }
func WithDeny(fn func(id string)) Option           { return func(m *Model) { m.onDeny = fn } }

// WithInterrupt / WithSteer / WithDetach register the remaining host actions.
func WithInterrupt(fn func()) Option        { return func(m *Model) { m.onInterrupt = fn } }
func WithSteer(fn func(text string)) Option { return func(m *Model) { m.onSteer = fn } }
func WithDetach(fn func()) Option           { return func(m *Model) { m.onDetach = fn } }

// Model is an event-sourced transcript. The zero value is not usable; build one
// with New.
type Model struct {
	list      *list.List
	committed []committedItem

	// event dedup / replay boundary
	lastSeq   uint64
	attachSeq uint64
	replaying bool

	// streaming assistant
	streaming         bool
	assistantBuf      strings.Builder
	streamAI          *chat.AssistantItem
	streamBlock       *assistantBlock // ephemeral trailing tail
	droppedPartialIdx int

	// live reasoning
	reasoning    bool
	reasoningBuf strings.Builder
	liveReason   *chat.ReasoningItem

	// flat tool pairing
	flatTools    map[string]int             // toolUseId -> committed index
	toolInputs   map[string]json.RawMessage // toolUseId -> retained input (for diff)
	pendingTools []int                      // committed indices of running flat cards (FIFO fallback)

	// subagents
	subs map[string]*subEntry // Task toolUseId -> subagent

	// pinned todos
	todoIdx int // committed index of the single todo block, or -1

	// pending permission (ephemeral trailing overlay, never in committed)
	pendingPerm   *chat.PermissionItem
	pendingPermID string

	// per-turn footer accounting
	turnActive    bool
	turnStart     time.Time
	inTok, outTok int
	costUSD       float64
	modelID       string
	modelDisplay  string
	backend       string
	modelNames    map[string]string // model id -> display name (models.available)

	// focus
	focusIdx int // committed index of the focused item, or -1

	// options
	now         func() time.Time
	markdown    bool
	onSubmit    func(string)
	onApprove   func(id, scope string)
	onDeny      func(id string)
	onInterrupt func()
	onSteer     func(string)
	onDetach    func()

	themeUnsub func()
}

// New builds an empty transcript Model. Call SetSize before Render.
func New(opts ...Option) *Model {
	m := &Model{
		list:              list.New(),
		flatTools:         map[string]int{},
		toolInputs:        map[string]json.RawMessage{},
		subs:              map[string]*subEntry{},
		modelNames:        map[string]string{},
		todoIdx:           -1,
		droppedPartialIdx: -1,
		focusIdx:          -1,
		now:               time.Now,
		markdown:          true,
	}
	for _, o := range opts {
		o(m)
	}
	// A transcript follows the live tail by default until the user scrolls up.
	m.list.SetFollow(true)
	// Re-skin the committed transcript and drop stale markdown renderers on a
	// /theme swap (the THEME-SWAP CONTRACT: without dropping the list cache a swap
	// re-skins only newly-rendered items).
	m.themeUnsub = theme.OnChange(func() {
		chat.InvalidateRenderers()
		m.list.InvalidateAll()
	})
	return m
}

// Close unsubscribes the theme hook. Call when discarding the Model.
func (m *Model) Close() {
	if m.themeUnsub != nil {
		m.themeUnsub()
		m.themeUnsub = nil
	}
}

// ---- sizing / rendering -----------------------------------------------------

// SetSize sets the transcript viewport (columns × rows).
func (m *Model) SetSize(w, h int) { m.list.SetSize(w, h) }

// Width / Height report the viewport size.
func (m *Model) Width() int  { return m.list.Width() }
func (m *Model) Height() int { return m.list.Height() }

// Render draws the transcript body at the current size.
func (m *Model) Render() string { return m.list.Render() }

// ---- scrolling / follow -----------------------------------------------------

func (m *Model) ScrollBy(lines int) { m.list.ScrollBy(lines) }
func (m *Model) GotoTop()           { m.list.GotoTop() }
func (m *Model) GotoBottom()        { m.list.GotoBottom() }
func (m *Model) AtBottom() bool     { return m.list.AtBottom() }
func (m *Model) Following() bool    { return m.list.Following() }

// Len reports the number of committed blocks (excludes ephemeral tails).
func (m *Model) Len() int { return len(m.committed) }

// ---- host actions -----------------------------------------------------------

// Submit optimistically appends a user block and invokes the WithSubmit
// callback so the host performs the actual turn start. A blank prompt is ignored.
func (m *Model) Submit(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.append(chat.NewUserItem(text), entryUser)
	if m.onSubmit != nil {
		m.onSubmit(text)
	}
}

// Approve resolves the pending permission (scope "session" grants for the whole
// session; anything else is one-shot) and invokes the WithApprove callback.
func (m *Model) Approve(scope string) {
	id := m.pendingPermID
	if m.pendingPerm == nil {
		return
	}
	if scope != "session" {
		scope = "once"
	}
	m.clearPending("  [permission approved]")
	if m.onApprove != nil {
		m.onApprove(id, scope)
	}
}

// Deny rejects the pending permission and invokes the WithDeny callback.
func (m *Model) Deny() {
	id := m.pendingPermID
	if m.pendingPerm == nil {
		return
	}
	m.clearPending("  [permission denied]")
	if m.onDeny != nil {
		m.onDeny(id)
	}
}

// Interrupt invokes the WithInterrupt callback (the turn-terminal transcript
// artifact arrives via the runner's turn.interrupted event).
func (m *Model) Interrupt() {
	if m.onInterrupt != nil {
		m.onInterrupt()
	}
}

// Steer invokes the WithSteer callback with a queued steering message. A blank
// message is ignored.
func (m *Model) Steer(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if m.onSteer != nil {
		m.onSteer(text)
	}
}

// Detach invokes the WithDetach callback.
func (m *Model) Detach() {
	if m.onDetach != nil {
		m.onDetach()
	}
}

// PendingPermission returns the pending permission descriptor, or nil when none
// is awaiting a decision.
func (m *Model) PendingPermission() *chat.Permission {
	if m.pendingPerm == nil {
		return nil
	}
	return m.pendingPerm.Permission()
}

func (m *Model) clearPending(notice string) {
	m.pendingPerm = nil
	m.pendingPermID = ""
	m.append(chat.NewNoticeItem(chat.NoticeInfo, notice), entryAttachment)
}

// ---- focus / expansion ------------------------------------------------------

type focusable interface {
	SetFocused(bool)
	Focused() bool
}

type expandable interface {
	Expandable(width int) bool
	SetExpanded(bool)
	Expanded() bool
}

// FocusNext moves focus to the next focusable committed block (wrapping), or the
// first one when nothing is focused.
func (m *Model) FocusNext() { m.moveFocus(+1) }

// FocusPrev moves focus to the previous focusable committed block (wrapping).
func (m *Model) FocusPrev() { m.moveFocus(-1) }

// ClearFocus blurs any focused block.
func (m *Model) ClearFocus() {
	if m.focusIdx >= 0 && m.focusIdx < len(m.committed) {
		if f, ok := m.committed[m.focusIdx].item.(focusable); ok {
			f.SetFocused(false)
		}
	}
	m.focusIdx = -1
	m.sync()
}

func (m *Model) moveFocus(dir int) {
	n := len(m.committed)
	if n == 0 {
		return
	}
	start := m.focusIdx
	for i := 1; i <= n; i++ {
		idx := ((start + dir*i) % n)
		if idx < 0 {
			idx += n
		}
		if start < 0 { // no current focus: scan from an edge
			if dir > 0 {
				idx = (i - 1) % n
			} else {
				idx = (n - i) % n
			}
		}
		if f, ok := m.committed[idx].item.(focusable); ok {
			if start >= 0 && start < n {
				if pf, ok := m.committed[start].item.(focusable); ok {
					pf.SetFocused(false)
				}
			}
			f.SetFocused(true)
			m.focusIdx = idx
			m.sync()
			return
		}
	}
}

// ToggleExpand flips the expansion of the focused expandable block, or — when
// nothing focusable is focused — the most recent expandable block (a tool card's
// diff/output, or a capped reasoning block). Returns whether a block toggled.
func (m *Model) ToggleExpand() bool {
	w := m.list.Width()
	if m.focusIdx >= 0 && m.focusIdx < len(m.committed) {
		if ex, ok := m.committed[m.focusIdx].item.(expandable); ok {
			if ex.Expanded() || ex.Expandable(w) {
				ex.SetExpanded(!ex.Expanded())
				m.sync()
				return true
			}
		}
	}
	for i := len(m.committed) - 1; i >= 0; i-- {
		if ex, ok := m.committed[i].item.(expandable); ok {
			if ex.Expanded() || ex.Expandable(w) {
				ex.SetExpanded(!ex.Expanded())
				m.sync()
				return true
			}
		}
	}
	return false
}

// ---- reduce -----------------------------------------------------------------

// Apply reduces one event into the transcript. Unknown or unhandled event types
// degrade gracefully (no-op). Persisted events at or below the seq cursor are
// dropped (replay dedup).
func (m *Model) Apply(ev client.Event) {
	if ev.Type == client.EventStreamLive {
		m.replaying = false
		return
	}
	if ev.Seq != 0 {
		if ev.Seq <= m.lastSeq {
			return
		}
		m.lastSeq = ev.Seq
	}
	if m.replaying && m.attachSeq > 0 && m.lastSeq >= m.attachSeq {
		m.replaying = false
	}

	switch ev.Type {
	case client.EventSessionStarted:
		var p client.SessionStartedPayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			m.modelID = p.Model
			if d := m.modelNames[p.Model]; d != "" {
				m.modelDisplay = d
			} else if m.modelDisplay == "" {
				m.modelDisplay = p.Model
			}
		}

	case client.EventModelsAvailable:
		var p client.ModelsAvailablePayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			for _, mi := range p.Models {
				if mi.DisplayName != "" {
					m.modelNames[mi.Value] = mi.DisplayName
				}
			}
			if d := m.modelNames[m.modelID]; d != "" {
				m.modelDisplay = d
			}
		}

	case client.EventTurnStarted:
		if !m.turnActive {
			m.beginTurn()
		}

	case client.EventTurnCompleted:
		m.finalizeStreaming()
		m.drainPendingTools("no result")
		var p client.TurnCompletedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.DurationMs > 0 && m.turnStart.IsZero() {
			// A replay/attach turn with no local start: use the reported duration.
			m.turnStart = m.now().Add(-time.Duration(p.DurationMs) * time.Millisecond)
		}
		if f := m.buildFooter(); f != nil {
			m.append(chat.NewFooterItem(f), entryAttachment)
		}
		m.turnActive = false

	case client.EventTurnInterrupted:
		m.finalizeStreaming()
		m.drainPendingTools("interrupted")
		m.turnActive = false
		m.append(chat.NewElbowNotice("Interrupted by user"), entryAttachment)

	case client.EventTurnFailed:
		m.finalizeStreaming()
		m.drainPendingTools("interrupted")
		m.turnActive = false
		var p client.TurnFailedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		msg := strings.TrimSpace(p.Message)
		if msg == "" {
			msg = "turn failed"
		}
		m.append(chat.NewNoticeItem(chat.NoticeError, "✗ "+msg), entryAttachment)

	case client.EventMessageStarted:
		var p client.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.ParentToolUseID != "" {
			m.applySubagentMessage(ev.Type, p)
			return
		}
		m.droppedPartialIdx = -1
		m.streaming = true
		m.assistantBuf.Reset()
		m.newStreamTail()

	case client.EventMessageDelta:
		var p client.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.ParentToolUseID != "" {
			m.applySubagentMessage(ev.Type, p)
			return
		}
		m.streaming = true
		m.assistantBuf.WriteString(p.Content)
		m.streamDelta()

	case client.EventMessageCompleted:
		var p client.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.ParentToolUseID != "" {
			m.applySubagentMessage(ev.Type, p)
			return
		}
		m.completeMessage(p)

	case client.EventReasoningStarted:
		var p client.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.ParentToolUseID != "" { // a subagent's own thinking is internal to its Task
			return
		}
		m.reasoning = true
		m.reasoningBuf.Reset()
		m.newReasonTail()

	case client.EventReasoningDelta:
		if !m.reasoning {
			return
		}
		var p client.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.ParentToolUseID != "" {
			return
		}
		m.reasoningBuf.WriteString(p.Content)
		m.reasonDelta()

	case client.EventReasoningCompleted:
		var p client.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.ParentToolUseID != "" {
			return
		}
		text := strings.TrimSpace(p.Content)
		if text == "" {
			text = strings.TrimSpace(m.reasoningBuf.String())
		}
		m.reasoning = false
		m.reasoningBuf.Reset()
		m.liveReason = nil
		if text != "" {
			m.append(chat.NewReasoningItem(text, false), entryReasoning)
		} else {
			m.sync()
		}

	case client.EventToolStarted:
		var p client.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		switch {
		case p.Tool == "Task" || p.AgentName != "":
			m.startSubagent(p)
		case p.ParentToolUseID != "" && m.subs[p.ParentToolUseID] != nil:
			m.startSubagentChild(p)
		default:
			m.startOrUpdateTool(p)
		}

	case client.EventToolDelta:
		var p client.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.applyToolDelta(p)

	case client.EventToolProgress:
		var p client.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.applyToolProgress(p, ev.Time)

	case client.EventToolCompleted:
		var p client.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !m.finishNested(p, chat.ToolOK, chat.ToolSummary(p.Output)) {
			m.finishTool(chat.ToolOK, chat.ToolSummary(p.Output), p)
		}

	case client.EventToolFailed:
		var p client.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		summary := p.Error
		if summary == "" {
			summary = chat.ToolSummary(p.Output)
		}
		if !m.finishNested(p, chat.ToolError, summary) {
			m.finishTool(chat.ToolError, summary, p)
		}

	case client.EventPermissionRequested:
		var p client.PermissionPayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			m.requestPermission(p)
		}

	case client.EventPermissionResolved:
		if m.pendingPerm != nil {
			var p client.PermissionPayload
			_ = json.Unmarshal(ev.Payload, &p)
			label := "resolved"
			switch p.Decision {
			case "deny":
				label = "denied"
			case "allow-once", "allow-session":
				label = "approved"
			}
			m.pendingPerm = nil
			m.pendingPermID = ""
			m.append(chat.NewNoticeItem(chat.NoticeInfo, "  [permission "+label+"]"), entryAttachment)
		} else {
			m.sync()
		}

	case client.EventTodoUpdated:
		var p client.TodoUpdatedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.applyTodos(p.Todos)

	case client.EventContextCompacted:
		var p client.ContextCompactedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.append(chat.NewNoticeItem(chat.NoticeInfo, compactionMarker(p)), entryAttachment)

	case client.EventUsageUpdated:
		var p client.UsagePayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			m.inTok = p.InputTokens
			m.outTok = p.OutputTokens
			m.costUSD = p.TotalCostUSD
		}

	case client.EventSessionStatusChanged:
		var p client.SessionStatusPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Status == "error" && strings.TrimSpace(p.Reason) != "" {
			m.append(chat.NewNoticeItem(chat.NoticeError, "session error: "+p.Reason), entryAttachment)
		}

	case client.EventSessionTerminating:
		var p client.TerminatingPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.turnActive = false
		warn := "⚠ pod is being rescheduled — saving state, will reconnect"
		if strings.TrimSpace(p.Reason) != "" {
			warn = "⚠ " + p.Reason + " — saving state, will reconnect"
		}
		m.append(chat.NewNoticeItem(chat.NoticeWarn, warn), entryAttachment)

	case client.EventError:
		var p client.ErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.append(chat.NewNoticeItem(chat.NoticeError, "error: "+p.Message), entryAttachment)

	default:
		// Unknown / chrome-only event (rate_limit, workspace, title, autopilot, …):
		// nothing to render in the transcript body. Degrade gracefully.
	}
}

// BeginReplay puts the reducer into replay mode with the seq the host last knew
// about. Historical events feed in; the reducer flips to live once it crosses
// attachSeq or receives EventStreamLive. Replay only affects the live/replay
// boundary flag — seq dedup guards double-application regardless.
func (m *Model) BeginReplay(attachSeq uint64) {
	m.replaying = true
	m.attachSeq = attachSeq
}

// Replaying reports whether the reducer is still catching up on history.
func (m *Model) Replaying() bool { return m.replaying }

// ---- turn lifecycle ---------------------------------------------------------

func (m *Model) beginTurn() {
	m.dropTrailingFooter()
	m.turnActive = true
	m.turnStart = m.now()
	m.inTok, m.outTok, m.costUSD = 0, 0, 0
}

// buildFooter assembles the per-turn outcome footer, or nil when there is
// nothing to summarize.
func (m *Model) buildFooter() *chat.TurnFooter {
	f := &chat.TurnFooter{
		Model:        m.modelDisplay,
		Backend:      m.backend,
		InputTokens:  m.inTok,
		OutputTokens: m.outTok,
		CostUSD:      m.costUSD,
	}
	if !m.turnStart.IsZero() {
		if d := m.now().Sub(m.turnStart); d > 0 {
			f.Elapsed = d
		}
	}
	if f.Model == "" && f.Backend == "" && f.Elapsed == 0 &&
		f.InputTokens == 0 && f.OutputTokens == 0 && f.CostUSD == 0 {
		return nil
	}
	return f
}

// dropTrailingFooter removes the previous turn's footer when it is the trailing
// block, so only the latest turn carries one.
func (m *Model) dropTrailingFooter() {
	n := len(m.committed)
	if n == 0 {
		return
	}
	if _, ok := m.committed[n-1].item.(*chat.FooterItem); ok {
		m.committed = m.committed[:n-1]
		m.sync()
	}
}

// finalizeStreaming commits any in-flight assistant/reasoning tail as a settled
// block at a turn boundary.
func (m *Model) finalizeStreaming() {
	if m.streaming {
		text := m.assistantBuf.String()
		m.streaming = false
		m.assistantBuf.Reset()
		m.streamAI = nil
		m.streamBlock = nil
		if strings.TrimSpace(text) != "" {
			m.append(m.newAssistant(&chat.AssistantMessage{Content: text, Finished: true}, nil), entryAssistant)
			m.droppedPartialIdx = len(m.committed) - 1
			return
		}
	}
	if m.reasoning {
		text := strings.TrimSpace(m.reasoningBuf.String())
		m.reasoning = false
		m.reasoningBuf.Reset()
		m.liveReason = nil
		if text != "" {
			m.append(chat.NewReasoningItem(text, false), entryReasoning)
		} else {
			m.sync()
		}
	}
}

// ---- streaming assistant ----------------------------------------------------

func (m *Model) newStreamTail() {
	ai := chat.NewAssistantItem(&chat.AssistantMessage{Streaming: true})
	if m.markdown {
		ai.SetRenderContentMD(renderAssistantMD)
		ai.SetRendererFactory(func(w int) chat.Renderer { return chat.MarkdownRenderer(w) })
	}
	m.streamAI = ai
	m.streamBlock = &assistantBlock{Versioned: list.NewVersioned(), ai: ai, streaming: true}
}

func (m *Model) streamDelta() {
	if m.streamAI == nil {
		m.newStreamTail()
	}
	m.streamAI.SetMessage(&chat.AssistantMessage{Content: m.assistantBuf.String(), Streaming: true})
	// The list caches on (item, version) keyed by the WRAPPER (streamBlock), so
	// bump the wrapper — SetMessage bumped only the inner AssistantItem.
	m.streamBlock.Bump()
	m.sync()
}

func (m *Model) completeMessage(p client.MessagePayload) {
	text := p.Content
	if text == "" {
		text = m.assistantBuf.String()
	}
	m.streaming = false
	m.assistantBuf.Reset()
	m.streamAI = nil
	m.streamBlock = nil

	if p.Role == "user" {
		// A user-role echo of an injected message: render with the user's styling
		// and dedup against the optimistic block appended at submit.
		if t := strings.TrimSpace(p.Content); t != "" {
			if n := len(m.committed); n > 0 {
				if u, ok := m.committed[n-1].item.(*chat.UserItem); ok && u != nil {
					if strings.TrimSpace(u.Text) == t {
						m.sync()
						return
					}
				}
			}
			m.append(chat.NewUserItem(p.Content), entryUser)
		} else {
			m.sync()
		}
		return
	}

	cites := toChatCitations(p.Citations)
	switch {
	case m.droppedPartialIdx >= 0 && m.droppedPartialIdx < len(m.committed):
		// The replayed full version of a partial committed on a mid-message drop:
		// replace it in place instead of appending a duplicate.
		if ab, ok := m.committed[m.droppedPartialIdx].item.(*assistantBlock); ok && strings.TrimSpace(text) != "" {
			ab.setContent(text, cites)
			m.droppedPartialIdx = -1
			m.sync()
			return
		}
		fallthrough
	default:
		if strings.TrimSpace(text) != "" {
			m.append(m.newAssistant(&chat.AssistantMessage{Content: text, Finished: true}, cites), entryAssistant)
		} else {
			m.sync()
		}
	}
}

// ---- live reasoning ---------------------------------------------------------

func (m *Model) newReasonTail() {
	m.liveReason = chat.NewReasoningItem("", true)
	m.sync()
}

func (m *Model) reasonDelta() {
	if m.liveReason == nil {
		m.newReasonTail()
	}
	m.liveReason.SetText(m.reasoningBuf.String())
	m.sync()
}

// ---- flat tools -------------------------------------------------------------

func (m *Model) startOrUpdateTool(p client.ToolPayload) {
	arg := chat.ToolArg(p.Tool, p.Input)
	if p.ToolUseID != "" {
		if idx, ok := m.flatTools[p.ToolUseID]; ok && idx >= 0 && idx < len(m.committed) {
			if ti, ok := m.committed[idx].item.(*chat.ToolItem); ok {
				if arg != "" {
					ti.SetArg(arg)
				}
				if len(p.Input) > 0 {
					m.toolInputs[p.ToolUseID] = p.Input
					if d := m.buildToolDiff(p.Tool, p.Input); len(d) > 0 {
						ti.SetDiff(d)
					}
				}
				m.sync()
				return
			}
		}
	}
	call := &chat.ToolCall{ID: p.ToolUseID, Name: p.Tool, Arg: arg, Status: chat.ToolRunning}
	if d := m.buildToolDiff(p.Tool, p.Input); len(d) > 0 {
		call.Diff = d
	}
	ti := chat.NewToolItem(call)
	m.append(ti, entryTool)
	idx := len(m.committed) - 1
	m.pendingTools = append(m.pendingTools, idx)
	if p.ToolUseID != "" {
		m.flatTools[p.ToolUseID] = idx
		if len(p.Input) > 0 {
			m.toolInputs[p.ToolUseID] = p.Input
		}
	}
}

func (m *Model) buildToolDiff(tool string, input json.RawMessage) []string {
	_, _, lines := editDiff(tool, input)
	if len(lines) == 0 {
		return nil
	}
	return condenseDiff(lines, diffCapLines)
}

func (m *Model) applyToolDelta(p client.ToolPayload) {
	if p.PartialJSON == "" {
		return
	}
	idx := -1
	switch {
	case p.ToolUseID != "":
		if i, ok := m.flatTools[p.ToolUseID]; ok {
			idx = i
		}
	case p.ParentToolUseID != "":
		// Parented but id-less: never guess a main-thread card.
	case len(m.pendingTools) > 0:
		idx = m.pendingTools[len(m.pendingTools)-1]
	}
	if idx < 0 || idx >= len(m.committed) {
		return
	}
	ti, ok := m.committed[idx].item.(*chat.ToolItem)
	if !ok {
		return
	}
	// Accumulate the streamed input JSON and preview the parsed argument. Frames
	// that don't parse keep the last good preview.
	prev := m.toolInputs[p.ToolUseID]
	raw := string(prev) + p.PartialJSON
	m.toolInputs[p.ToolUseID] = json.RawMessage(raw)
	name := ti.Call().Name
	if arg := chat.ToolArg(name, json.RawMessage(raw)); arg != "" {
		ti.SetArg(collapse(arg))
	} else if arg := chat.ToolArg(name, json.RawMessage(raw+`"}`)); arg != "" {
		ti.SetArg(collapse(arg))
	}
	m.sync()
}

func (m *Model) applyToolProgress(p client.ToolPayload, evTime string) {
	if p.ElapsedSeconds == nil || p.ToolUseID == "" {
		return
	}
	elapsed := time.Duration(*p.ElapsedSeconds * float64(time.Second))
	if idx, ok := m.flatTools[p.ToolUseID]; ok && idx >= 0 && idx < len(m.committed) {
		if ti, ok := m.committed[idx].item.(*chat.ToolItem); ok && ti.Call().Status == chat.ToolRunning {
			ti.SetElapsed(elapsed)
			m.sync()
		}
		return
	}
	if sub := m.subs[p.ToolUseID]; sub != nil {
		if sub.item.Subagent().Status == chat.ToolRunning {
			sub.item.SetElapsed(elapsed)
			m.sync()
		}
	}
}

func (m *Model) finishTool(status chat.ToolStatus, summary string, p client.ToolPayload) {
	summary = kit.RemapANSI(summary)
	idx := -1
	if p.ToolUseID != "" {
		if i, ok := m.flatTools[p.ToolUseID]; ok {
			idx = i
		}
	}
	if idx < 0 && len(m.pendingTools) > 0 {
		idx = m.pendingTools[0]
		m.pendingTools = m.pendingTools[1:]
	} else if idx >= 0 {
		m.removePending(idx)
	}
	if idx >= 0 && idx < len(m.committed) {
		if ti, ok := m.committed[idx].item.(*chat.ToolItem); ok {
			m.finishToolItem(ti, status, summary, p)
			m.sync()
			return
		}
	}
	// Orphan result (no matching start): a standalone finished card.
	call := &chat.ToolCall{Name: p.Tool, Status: status, Summary: summary, Output: p.Output}
	if p.ExitCode != nil {
		call.ExitCode = p.ExitCode
	}
	m.append(chat.NewToolItem(call), entryTool)
}

func (m *Model) finishToolItem(ti *chat.ToolItem, status chat.ToolStatus, summary string, p client.ToolPayload) {
	ti.SetStatus(status, summary)
	if p.Output != "" {
		ti.SetOutput(p.Output)
	}
	if p.ExitCode != nil {
		ti.SetExitCode(*p.ExitCode)
	}
	if in, ok := m.toolInputs[p.ToolUseID]; ok {
		if d := m.buildToolDiff(ti.Call().Name, in); len(d) > 0 {
			ti.SetDiff(d)
		}
	}
}

func (m *Model) removePending(idx int) {
	for i, v := range m.pendingTools {
		if v == idx {
			m.pendingTools = append(m.pendingTools[:i:i], m.pendingTools[i+1:]...)
			return
		}
	}
}

func (m *Model) drainPendingTools(reason string) {
	if len(m.pendingTools) == 0 {
		return
	}
	for _, idx := range m.pendingTools {
		if idx >= 0 && idx < len(m.committed) {
			if ti, ok := m.committed[idx].item.(*chat.ToolItem); ok && ti.Call().Status == chat.ToolRunning {
				sum := ti.Call().Summary
				if sum == "" {
					sum = reason
				}
				ti.SetStatus(chat.ToolError, sum)
			}
		}
	}
	m.pendingTools = nil
	m.sync()
}

// ---- subagents --------------------------------------------------------------

func (m *Model) startSubagent(p client.ToolPayload) {
	if p.ToolUseID == "" || m.subs[p.ToolUseID] != nil {
		return
	}
	item := chat.NewSubagentItem(&chat.Subagent{
		ID:        p.ToolUseID,
		AgentName: p.AgentName,
		Prompt:    taskPrompt(p.Input),
		Status:    chat.ToolRunning,
	})
	m.subs[p.ToolUseID] = &subEntry{item: item, children: map[string]*chat.ToolCall{}}
	m.append(item, entrySubagent)
}

func (m *Model) startSubagentChild(p client.ToolPayload) {
	sub := m.subs[p.ParentToolUseID]
	if sub == nil {
		return
	}
	if p.ToolUseID != "" && sub.children[p.ToolUseID] != nil {
		return
	}
	child := &chat.ToolCall{ID: p.ToolUseID, Name: p.Tool, Arg: chat.ToolArg(p.Tool, p.Input), Status: chat.ToolRunning}
	sub.item.AddChild(child)
	if p.ToolUseID != "" {
		sub.children[p.ToolUseID] = child
	}
	m.sync()
}

func (m *Model) applySubagentMessage(t client.EventType, p client.MessagePayload) {
	sub := m.subs[p.ParentToolUseID]
	if sub == nil || p.Role == "user" {
		return
	}
	switch t {
	case client.EventMessageStarted:
		sub.item.SetNarration("")
		m.sync()
	case client.EventMessageDelta:
		cur := sub.item.Subagent().Narration
		sub.item.SetNarration(cur + p.Content)
		m.sync()
	case client.EventMessageCompleted:
		text := strings.TrimSpace(p.Content)
		if text != "" {
			sub.item.SetNarration(text)
		}
		m.sync()
	}
}

func (m *Model) finishNested(p client.ToolPayload, status chat.ToolStatus, summary string) bool {
	if p.ToolUseID != "" {
		if sub := m.subs[p.ToolUseID]; sub != nil {
			sub.item.SetStatus(status)
			m.sync()
			return true
		}
		// A child result: mutate the retained child call and bump the card.
		for _, sub := range m.subs {
			if child := sub.children[p.ToolUseID]; child != nil {
				child.Status = status
				child.Summary = kit.RemapANSI(summary)
				sub.item.Bump()
				m.sync()
				return true
			}
		}
	}
	if p.ParentToolUseID != "" {
		if sub := m.subs[p.ParentToolUseID]; sub != nil {
			for _, c := range sub.item.Subagent().Children {
				if c != nil && c.Status == chat.ToolRunning {
					c.Status = status
					c.Summary = kit.RemapANSI(summary)
					break
				}
			}
			sub.item.Bump()
			m.sync()
			return true
		}
	}
	return false
}

// ---- permissions ------------------------------------------------------------

func (m *Model) requestPermission(p client.PermissionPayload) {
	perm := &chat.Permission{ID: p.PermissionID, Tool: p.Tool}
	if p.Tool == "ExitPlanMode" {
		var pl struct {
			Plan string `json:"plan"`
		}
		_ = json.Unmarshal(p.Input, &pl)
		perm.IsPlan = true
		perm.Plan = pl.Plan
	} else {
		perm.Arg = chat.ToolArg(p.Tool, p.Input)
		if _, _, lines := editDiff(p.Tool, p.Input); len(lines) > 0 {
			perm.Diff = condenseDiff(lines, diffCapLines)
		}
	}
	m.pendingPerm = chat.NewPermissionItem(perm)
	m.pendingPermID = p.PermissionID
	m.sync()
}

// ---- todos ------------------------------------------------------------------

func (m *Model) applyTodos(items []client.TodoItem) {
	todos := make([]chat.Todo, 0, len(items))
	for _, it := range items {
		todos = append(todos, chat.Todo{
			Content:    it.Content,
			ActiveForm: it.ActiveForm,
			Status:     todoStatus(it.Status),
		})
	}
	if m.todoIdx >= 0 && m.todoIdx < len(m.committed) {
		if ti, ok := m.committed[m.todoIdx].item.(*chat.TodosItem); ok {
			ti.SetTodos(todos)
			m.sync()
			return
		}
	}
	item := chat.NewTodosItem(todos)
	m.append(item, entryTodos)
	m.todoIdx = len(m.committed) - 1
}

// ---- list composition -------------------------------------------------------

// append settles a block and re-syncs the list.
func (m *Model) append(item list.Item, kind entryKind) {
	m.committed = append(m.committed, committedItem{item: item, kind: kind})
	m.sync()
}

// sync rebuilds the list's item set from the committed blocks (interleaving
// entry-gap spacers), then any ephemeral trailing tail (streaming assistant or
// live reasoning) and the pending-permission overlay. The list's follow flag
// preserves the reader's position (bottom-pin when following, offset when
// scrolled back).
func (m *Model) sync() {
	items := make([]list.Item, 0, len(m.committed)*2+2)
	prev := entryNone
	for _, e := range m.committed {
		if len(items) > 0 && startsEntry(prev, e.kind) {
			items = append(items, newGap())
		}
		items = append(items, e.item)
		prev = e.kind
	}
	// Ephemeral live tail: the streaming assistant, or the live reasoning tail.
	// (prev is the last committed kind; a live tail always opens its own entry, so
	// it earns the leading gap like the committed block it will become.)
	switch {
	case m.streaming && m.streamBlock != nil:
		if startsEntry(prev, entryAssistant) {
			items = append(items, newGap())
		}
		items = append(items, m.streamBlock)
	case m.reasoning && m.liveReason != nil:
		if startsEntry(prev, entryReasoning) {
			items = append(items, newGap())
		}
		items = append(items, m.liveReason)
	}
	if m.pendingPerm != nil {
		items = append(items, newGap())
		items = append(items, m.pendingPerm)
	}
	m.list.SetItems(items...)
}

// blankSpacer is what a gapItem renders: the empty string, which tui/list turns
// into exactly one blank line. Named so this reads as the deliberate "calm"
// entry-gap spacer it is, not an unfinished renderer.
const blankSpacer = ""

// gapItem renders a single blank line between top-level entries (the "calm"
// spacing). It is static (its rendered output never changes).
type gapItem struct{ *list.Versioned }

func (gapItem) Render(int) string { return blankSpacer }

func newGap() list.Item { return gapItem{list.NewVersioned()} }

// ---- assistant block --------------------------------------------------------

// assistantBlock renders a (streaming or finished) assistant message through the
// public chat.AssistantItem body + chat.Bullet chrome, with an optional dim
// citation footnote list under the body — the composition a host uses to match
// the production transcript grammar.
type assistantBlock struct {
	*list.Versioned
	ai        *chat.AssistantItem
	cites     []chat.Citation
	streaming bool
}

func (m *Model) newAssistant(msg *chat.AssistantMessage, cites []chat.Citation) *assistantBlock {
	ai := chat.NewAssistantItem(msg)
	if m.markdown {
		ai.SetRenderContentMD(renderAssistantMD)
		ai.SetRendererFactory(func(w int) chat.Renderer { return chat.MarkdownRenderer(w) })
	}
	return &assistantBlock{Versioned: list.NewVersioned(), ai: ai, cites: cites}
}

func (b *assistantBlock) setContent(text string, cites []chat.Citation) {
	b.ai.SetMessage(&chat.AssistantMessage{Content: text, Finished: true})
	b.cites = cites
	b.Bump()
}

func (b *assistantBlock) Render(width int) string {
	if width < bulletIndent+1 {
		width = bulletIndent + 1
	}
	body := b.ai.RawRender(width - bulletIndent)
	if fn := chat.RenderCitations(b.cites, width-bulletIndent); fn != "" {
		body += "\n" + fn
	}
	body = strings.TrimRight(body, "\n")
	return chat.Bullet(body)
}

// ---- helpers ----------------------------------------------------------------

func renderAssistantMD(text string, width int) string {
	r := chat.MarkdownRenderer(width)
	if r == nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}

func todoStatus(s string) chat.TodoStatus {
	switch s {
	case "in_progress":
		return chat.TodoInProgress
	case "completed":
		return chat.TodoCompleted
	default:
		return chat.TodoPending
	}
}

func compactionMarker(p client.ContextCompactedPayload) string {
	switch {
	case p.PreTokens > 0 && p.PostTokens > 0:
		return fmt.Sprintf("context compacted · %s→%s tokens", kit.FormatTokens(p.PreTokens), kit.FormatTokens(p.PostTokens))
	case p.PreTokens > 0:
		return fmt.Sprintf("context compacted · %s tokens", kit.FormatTokens(p.PreTokens))
	case p.PostTokens > 0:
		return fmt.Sprintf("context compacted · %s tokens", kit.FormatTokens(p.PostTokens))
	default:
		return "context compacted"
	}
}

func taskPrompt(input json.RawMessage) string {
	var t struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	_ = json.Unmarshal(input, &t)
	if t.Description != "" {
		return t.Description
	}
	if i := strings.IndexByte(t.Prompt, '\n'); i >= 0 {
		return t.Prompt[:i]
	}
	return t.Prompt
}

func toChatCitations(cs []client.Citation) []chat.Citation {
	if len(cs) == 0 {
		return nil
	}
	out := make([]chat.Citation, 0, len(cs))
	for _, c := range cs {
		out = append(out, chat.Citation{Title: c.Title, URL: c.URL, CitedText: c.CitedText})
	}
	return out
}

// collapse flattens whitespace runs to single spaces for a one-line preview.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
