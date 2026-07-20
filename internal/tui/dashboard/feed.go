package dashboard

// feed.go — the detached activity feed (claude-pane-first, detached-activity-feed
// spec). For external-pane sessions (opencode, claude-pane) the real agent TUI
// lives in the pane; when the user is NOT attached, the feed is a read-only
// MONITOR built from the same normalized events the read-model consumes:
// prompts, streaming assistant text, one-line tool entries, and calm system
// notices. It deliberately does NOT reproduce the chat transcript — no tool
// cards, no subagent trees, no permission modals, no todo widget, no input.
// Fidelity is the pane's job (attach with enter/a); the feed just answers
// "what's it doing right now?" without holding the terminal.
//
// It is self-contained (no dependency on the transcript renderer, which
// claude-pane-first deletes): its own minimal reducer + one-line formatting,
// rendering through the shared tui/list + tui/kit + theme building blocks.

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// feedKind is the small vocabulary of activity-feed entries. Each renders as a
// single top-level entry (a prompt, an assistant reply, a one-line tool note,
// or a calm dim sentence) — deliberately far fewer shapes than the transcript's
// block grammar.
type feedKind int

const (
	feedUser      feedKind = iota // a user/autopilot prompt
	feedAssistant                 // assistant reply text (may stream)
	feedTool                      // one-line tool activity
	feedNotice                    // calm dim system sentence (aborts, lifecycle)
)

// feedItem is one activity-feed entry and its own list.Item. It owns a version
// so a streamed assistant entry (or a tool line updated with its result)
// re-renders only itself.
type feedItem struct {
	*list.Versioned
	kind feedKind
	text string
	// streaming marks an assistant entry still accumulating deltas (rendered
	// with a soft trailing cursor); cleared at message/turn completion.
	streaming bool
	// toolUseID links a feedTool entry to its tool.started so tool.completed can
	// fold a result summary into the same line rather than adding a second.
	toolUseID string
	// toolPrefix is the "Tool — arg" head captured at tool.started, so a
	// completion (whose payload often omits the input) can append its result
	// without losing the argument.
	toolPrefix string
}

func newFeedItem(kind feedKind, text string) *feedItem {
	return &feedItem{Versioned: list.NewVersioned(), kind: kind, text: text}
}

func (it *feedItem) set(text string) {
	if it.text == text {
		return
	}
	it.text = text
	it.Bump()
}

// Render draws one entry. Entries are separated by a leading blank (one calm
// blank line between top-level entries), applied by the model at commit time —
// here we render just the styled body.
func (it *feedItem) Render(width int) string {
	switch it.kind {
	case feedUser:
		head := lipgloss.NewStyle().Foreground(theme.Charple).Bold(true).Render("▸ ")
		body := lipgloss.NewStyle().Foreground(theme.TextBright).Render(feedWrap(it.text, width-2))
		return head + indentRest(body, 2)
	case feedAssistant:
		body := it.text
		if it.streaming {
			body += "▍"
		}
		styled := lipgloss.NewStyle().Foreground(theme.TextBody).Render(feedWrap(body, width-2))
		return lipgloss.NewStyle().Foreground(theme.Guac).Render("⏺ ") + indentRest(styled, 2)
	case feedTool:
		return lipgloss.NewStyle().Foreground(theme.TextMuted).Render("  " + feedTruncate(it.text, width-2))
	default: // feedNotice
		return lipgloss.NewStyle().Foreground(theme.TextDim).Italic(true).Render(feedTruncate(it.text, width))
	}
}

// feedModel is the read-only activity-feed view for one external-pane session.
type feedModel struct {
	ref   session.Ref
	title string
	label string // backend client label for the status row ("claude"/"opencode")

	width, height int
	body          *list.List
	items         []*feedItem

	// Reduce state.
	lastSeq      uint64    // seq dedup vs cache seed + passive-stream replay
	stream       *feedItem // the in-flight assistant entry accumulating deltas
	streamBuf    strings.Builder
	toolByID     map[string]*feedItem
	reconnecting bool // header band while the SSE stream re-establishes
	gone         bool // header band when the session is gone
}

func newFeedModel(ref session.Ref, title, label string) *feedModel {
	return &feedModel{
		ref:      ref,
		title:    title,
		label:    label,
		body:     list.New(),
		toolByID: map[string]*feedItem{},
	}
}

// SetSize lays the feed out, reserving the last row for the status line (and a
// header band row when reconnecting/gone).
func (m *feedModel) SetSize(w, h int) {
	m.width, m.height = w, h
	m.body.SetSize(max(1, w-1), m.bodyHeight())
}

func (m *feedModel) bodyHeight() int {
	h := m.height - 1 // status row
	if m.reconnecting || m.gone {
		h-- // header band
	}
	if h < 1 {
		h = 1
	}
	return h
}

// seed bulk-ingests a session's cached history so the feed opens populated
// rather than blank; it follows the bottom afterward. Delta events are not
// cached, so seeded assistant text arrives already-complete.
func (m *feedModel) seed(events []session.Event) {
	for _, ev := range events {
		m.reduce(ev)
	}
	m.commit()
	m.body.SetFollow(true)
}

// ingest applies one live event and re-commits, keeping the bottom pinned when
// following. Deltas re-render only the streaming tail.
func (m *feedModel) ingest(ev session.Event) {
	if m.reduce(ev) {
		m.commit()
	}
}

// reduce folds one normalized event into the entry list. It returns whether the
// entry set changed (so ingest can skip a needless commit). Seq dedup mirrors
// the read-model's: an event at or below the resume cursor is a replay.
func (m *feedModel) reduce(ev session.Event) bool {
	if ev.Seq != 0 && ev.Seq <= m.lastSeq {
		return false
	}
	if ev.Seq > m.lastSeq {
		m.lastSeq = ev.Seq
	}
	switch ev.Type {
	case session.EventTurnStarted:
		var p session.TurnStartedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.finishStream()
		if p.Prompt != "" {
			m.items = append(m.items, newFeedItem(feedUser, collapseFeedSpaces(p.Prompt)))
			return true
		}
		return false

	case session.EventMessageDelta:
		var p session.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		// Monitor only the main thread — a subagent's stream would interleave.
		if p.Role != "assistant" || p.ParentToolUseID != "" || p.Content == "" {
			return false
		}
		if m.stream == nil {
			m.streamBuf.Reset()
			m.stream = newFeedItem(feedAssistant, "")
			m.stream.streaming = true
			m.items = append(m.items, m.stream)
		}
		m.streamBuf.WriteString(p.Content)
		m.stream.streaming = true
		m.stream.set(strings.TrimSpace(m.streamBuf.String()))
		return true

	case session.EventMessageCompleted:
		var p session.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Role != "assistant" || p.ParentToolUseID != "" {
			return false
		}
		text := strings.TrimSpace(p.Content)
		if m.stream != nil {
			// Finalize the streamed entry to the authoritative text (dedup).
			m.stream.streaming = false
			if text != "" {
				m.stream.set(text)
			}
			m.stream = nil
			m.streamBuf.Reset()
			return true
		}
		if text != "" {
			m.items = append(m.items, newFeedItem(feedAssistant, text))
			return true
		}
		return false

	case session.EventToolStarted:
		var p session.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.ParentToolUseID != "" { // subagent child tools stay out of the monitor
			return false
		}
		prefix := feedToolLine(p.Tool, p.Input, "")
		it := newFeedItem(feedTool, prefix)
		it.toolUseID = p.ToolUseID
		it.toolPrefix = prefix
		if p.ToolUseID != "" {
			m.toolByID[p.ToolUseID] = it
		}
		m.items = append(m.items, it)
		return true

	case session.EventToolCompleted, session.EventToolFailed:
		var p session.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.ParentToolUseID != "" {
			return false
		}
		result := feedToolResult(ev.Type, p)
		if it, ok := m.toolByID[p.ToolUseID]; ok {
			// Keep the started line's "Tool — arg" head (the completion payload
			// usually omits the input) and append the result.
			line := it.toolPrefix
			if result != "" {
				line += " · " + result
			}
			it.set(line)
			return true
		}
		// No matching started (replayed mid-stream): a fresh line still informs.
		m.items = append(m.items, newFeedItem(feedTool, feedToolLine(p.Tool, p.Input, result)))
		return true

	case session.EventTurnCompleted:
		return m.finishStream()

	case session.EventTurnInterrupted:
		m.finishStream()
		var p session.TurnInterruptedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		reason := strings.TrimSpace(p.Reason)
		notice := "Turn interrupted."
		if reason != "" {
			notice = "Turn interrupted — " + reason + "."
		}
		m.items = append(m.items, newFeedItem(feedNotice, notice))
		return true

	case session.EventTurnFailed:
		m.finishStream()
		m.items = append(m.items, newFeedItem(feedNotice, "Turn failed."))
		return true

	case session.EventError:
		var p session.ErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if msg := strings.TrimSpace(p.Message); msg != "" {
			m.items = append(m.items, newFeedItem(feedNotice, feedTruncate(collapseFeedSpaces(msg), 200)))
			return true
		}
		return false

	case session.EventSessionTitle:
		var p session.SessionTitlePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Title != "" {
			m.title = p.Title
		}
		return false
	}
	return false
}

// finishStream closes any in-flight streaming assistant entry (turn boundary /
// interruption). Returns whether anything changed.
func (m *feedModel) finishStream() bool {
	if m.stream == nil {
		return false
	}
	m.stream.streaming = false
	m.stream = nil
	m.streamBuf.Reset()
	return true
}

// notice appends a calm dim system sentence (connection lifecycle). Public to
// the App so it can narrate stream drops/reconnects like the transcript's calm
// notices, without brackets.
func (m *feedModel) notice(text string) {
	m.items = append(m.items, newFeedItem(feedNotice, text))
	m.commit()
}

// commit hands the entries (with a calm one-blank-line gap between top-level
// entries) to the list, preserving the bottom pin when following.
func (m *feedModel) commit() {
	wasBottom := m.body.AtBottom()
	items := make([]list.Item, 0, len(m.items)*2)
	for i, it := range m.items {
		if i > 0 {
			items = append(items, feedGap{})
		}
		items = append(items, it)
	}
	m.body.SetItems(items...)
	if wasBottom {
		m.body.GotoBottom()
	}
}

// feedGap is a blank spacer entry between top-level feed entries (the calm
// one-blank-line rhythm). A stable zero version → always a cache hit.
type feedGap struct{}

func (feedGap) Render(int) string { return "" }
func (feedGap) Version() uint64   { return 0 }

// setConnection updates the header-band state (reconnecting/gone) and re-lays
// out (the band steals a body row). Returns whether the layout changed.
func (m *feedModel) setConnection(reconnecting, gone bool) bool {
	if m.reconnecting == reconnecting && m.gone == gone {
		return false
	}
	m.reconnecting, m.gone = reconnecting, gone
	m.SetSize(m.width, m.height)
	return true
}

// scroll moves the viewport (read-only navigation).
func (m *feedModel) scroll(lines int) { m.body.ScrollBy(lines) }
func (m *feedModel) top()             { m.body.GotoTop() }
func (m *feedModel) bottom()          { m.body.GotoBottom() }

// View renders the optional header band, the feed body with a transient
// scrollbar, and the status row.
func (m *feedModel) View() string {
	var rows []string
	if band := m.headerBand(); band != "" {
		rows = append(rows, band)
	}
	rows = append(rows, m.bodyView(), m.statusRow())
	return strings.Join(rows, "\n")
}

// headerBand renders only in the exceptional states (reconnecting / gone) —
// otherwise the body starts at row 0 (transcript-calm-chrome: no persistent
// header on a session screen; identity lives in the window title).
func (m *feedModel) headerBand() string {
	switch {
	case m.gone:
		return lipgloss.NewStyle().Width(m.width).Background(theme.Surface).
			Foreground(theme.Coral).Render(" session ended")
	case m.reconnecting:
		return lipgloss.NewStyle().Width(m.width).Background(theme.Surface).
			Foreground(theme.Gold).Render(" reconnecting…")
	}
	return ""
}

// bodyView renders the list padded to the body height, with a transient
// scrollbar thumb shown only while scrolled off the bottom.
func (m *feedModel) bodyView() string {
	h := m.bodyHeight()
	if len(m.items) == 0 {
		hint := lipgloss.NewStyle().Foreground(theme.TextMuted).Render(
			"no activity yet · " + kit.Kbd("enter", "attach") + " to work in the pane")
		return fitModal(lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Center, hint), m.width, h)
	}
	out := m.body.Render()
	total, offset := m.body.Metrics()
	var bar string
	if offset < total-h { // transient thumb: only while reading scrollback
		bar = kit.Scrollbar(h, total, h, offset)
	}
	body := fitModal(out, max(1, m.width-1), h)
	if bar == "" {
		return body
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, body, bar)
}

// statusRow mirrors the external pane's reserved status line so a detached feed
// and an attached pane read the same: title · client, plus the attach/back
// hints. Live metrics ride the pane; the feed keeps it to identity + hints.
func (m *feedModel) statusRow() string {
	muted := lipgloss.NewStyle().Foreground(theme.TextMuted)
	left := lipgloss.NewStyle().Foreground(theme.Charple).Bold(true).Render(m.title) +
		muted.Render(" · "+m.label+" · watching")
	right := kit.Kbd("enter", "attach") + muted.Render("  ") + kit.Kbd("esc", "back")
	w := m.width
	if w < 1 {
		w = extDefaultW
	}
	bar := spread(left, right, w)
	return lipgloss.NewStyle().Width(w).Background(theme.Surface).Render(bar)
}

// --- self-contained one-line formatting (independent of the transcript stack) ---

// feedToolLine formats a one-line tool entry: "Tool — arg" (+ " · result" when
// present). The argument is the tool's most identifying field.
func feedToolLine(tool string, input json.RawMessage, result string) string {
	line := tool
	if arg := feedToolArg(tool, input); arg != "" {
		line += " — " + arg
	}
	if result != "" {
		line += " · " + result
	}
	return line
}

// feedToolArg extracts the identifying argument for a tool call, mirroring the
// fields Claude Code tools use. Kept local so the feed survives the transcript
// renderer's deletion.
func feedToolArg(tool string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var raw map[string]any
	if json.Unmarshal(input, &raw) != nil {
		return ""
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := raw[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch tool {
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		if p := get("file_path", "notebook_path", "path"); p != "" {
			return feedShortenPath(p)
		}
	case "Bash":
		return collapseFeedSpaces(get("command"))
	case "Grep", "Glob":
		return get("pattern")
	case "WebFetch":
		return get("url")
	case "WebSearch":
		return get("query")
	}
	if p := get("file_path", "path", "command", "pattern", "url", "query"); p != "" {
		return collapseFeedSpaces(p)
	}
	return ""
}

// feedToolResult condenses a completed tool's outcome into a short note.
func feedToolResult(t session.EventType, p session.ToolPayload) string {
	if t == session.EventToolFailed {
		if e := strings.TrimSpace(p.Error); e != "" {
			return "failed: " + collapseFeedSpaces(firstLineOf(e))
		}
		return "failed"
	}
	if p.ExitCode != nil && *p.ExitCode != 0 {
		return fmt.Sprintf("exit %d", *p.ExitCode)
	}
	out := strings.TrimRight(p.Output, "\n")
	if out == "" {
		return ""
	}
	if n := strings.Count(out, "\n"); n >= 1 {
		return fmt.Sprintf("%d lines", n+1)
	}
	return collapseFeedSpaces(out)
}

func feedShortenPath(p string) string {
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func collapseFeedSpaces(s string) string { return strings.Join(strings.Fields(s), " ") }

// feedTruncate hard-caps a single-line string to width columns with an ellipsis.
func feedTruncate(s string, width int) string {
	if width < 1 {
		width = 1
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// feedWrap wraps text to width columns for multi-line entries (prompts, replies).
func feedWrap(s string, width int) string {
	if width < 1 {
		width = 1
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

// indentRest hangs continuation lines under a leading glyph by n spaces, so a
// wrapped prompt/reply aligns under its first character rather than the bullet.
func indentRest(s string, n int) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.Repeat(" ", n) + lines[i]
	}
	return strings.Join(lines, "\n")
}
