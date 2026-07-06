package dashboard

import (
	"encoding/json"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard/chat"
	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// modalContent renders the transcript sized for the command-center modal. The
// output is normalized to a solid w×h block (fitModal) so short rows don't leave
// the modal layer transparent on the right — otherwise the dark dashboard
// underneath shows through as a long dark line beside the status line (TODO.md).
func (m *TranscriptModel) modalContent(w, h int) string {
	if m.width != w || m.height != h {
		m.width, m.height = w, h
		m.layout()
	}
	return fitModal(m.renderTranscript(w, h), w, h)
}

// fitModal forces s to exactly h lines, each exactly w columns: short lines are
// padded with spaces (opaque cells, so the dashboard layer can't bleed through)
// and any over-wide line is ANSI-aware-truncated. Truncation is a backstop;
// renderInput already sizes itself to avoid overflow.
func fitModal(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		if lipgloss.Width(l) > w {
			l = ansi.Truncate(l, w, "")
		}
		lines[i] = padRight(l, w)
	}
	return strings.Join(lines, "\n")
}

// previewView renders the transcript's history (header + divider + scrollable
// body) with a connect banner where the composer would normally sit. It is used
// during ScreenConnecting (Fix A) so a session's conversation is visible while
// its pod resumes, instead of a blank splash. Read-only: no input box.
func (m *TranscriptModel) previewView(w, h int, banner string) string {
	m.width, m.height = w, h
	bannerH := lipgloss.Height(banner)
	// header(1) + divider(1) + body + blank(1) + banner.
	bodyH := h - 3 - bannerH
	if bodyH < 1 {
		bodyH = 1
	}
	m.body.SetSize(max(1, w-1), bodyH)
	m.syncItems()
	m.body.GotoBottom()
	parts := []string{
		m.renderHeader(),
		styleDivider.Render(strings.Repeat("─", w)),
		m.bodyView(),
		"",
		banner,
	}
	return strings.Join(parts, "\n")
}

// renderTranscript builds the actual transcript string for the current size.
func (m *TranscriptModel) renderTranscript(w, h int) string {
	body := m.bodyView()
	// A fresh session has no history yet; show a brief welcome instead of a blank
	// void (parity with the dashboard's firstRunView). Live attached view only —
	// previewView keeps its plain body under the connect banner.
	if m.transcriptEmpty() {
		body = m.emptyTranscriptView(max(1, m.width-1), m.body.Height())
	}
	parts := []string{m.renderHeader(), styleDivider.Render(strings.Repeat("─", w)), body}
	if m.pending != nil {
		// Rebuild the box at render time so the permission-appear border fade
		// (§C.3) reads the live elapsed time rather than the cached layout build.
		if m.pending.isPlan {
			parts = append(parts, m.permBox)
		} else {
			parts = append(parts, m.buildPermissionBox(m.width))
		}
	}
	if m.palette != "" {
		parts = append(parts, m.palette)
	}
	// The search bar (T3) sits just above the input when open; without this it
	// was dead code and `/`-search opened with no visible affordance.
	if m.search.open {
		parts = append(parts, m.renderSearchBar(w))
	}
	// A blank line sets the input apart from the transcript so the composer has
	// room to breathe instead of butting against the last message (roominess).
	parts = append(parts, "", m.renderInput(), m.renderStatusLine())
	return strings.Join(parts, "\n")
}

// --------------------------------------------------------------------------
// Layout
// --------------------------------------------------------------------------

// layout (re)sizes the list body and input and reconciles items. It is called
// on resize and whenever the permission box appears/disappears or the diff view
// toggles, since those change the available body height.
func (m *TranscriptModel) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	permH := 0
	m.permBox = ""
	if m.pending != nil {
		if m.pending.isPlan {
			m.permBox = m.renderPlanCard(m.width)
		} else {
			m.permBox = m.buildPermissionBox(m.width)
		}
		permH = strings.Count(m.permBox, "\n") + 1
	}

	palH := 0
	m.palette = ""
	if m.paletteOpen() {
		m.palette = m.renderPalette(m.width)
		palH = strings.Count(m.palette, "\n") + 1
	}

	// The search bar consumes one row above the input when open (T3).
	searchH := 0
	if m.search.open {
		searchH = 1
	}

	// Size the composer first so inputRows() (which wraps on this width) is
	// accurate, then reserve the body height around the boxed input. Must match
	// renderInput() exactly, or the reserved height drifts from what renders.
	m.input.SetWidth(m.composerInnerWidth())
	// header(1) + divider(1) + input gap(1) + box(border 2 + rows) + hint row(1).
	inputH := m.inputRows() + 3
	vpH := m.height - 3 - inputH - statusLineRows - permH - palH - searchH
	if vpH < 1 {
		vpH = 1
	}
	// Reserve one column on the right for the transcript scrollbar (§D).
	m.body.SetSize(max(1, m.width-1), vpH)
	m.syncItems()
}

// maxInputRows caps how tall the composer grows before it scrolls internally.
const maxInputRows = 6

// inputRows is the composer's current display height (1..maxInputRows), driving
// both the box render and the body-height reservation in layout.
// composerBoxWidth is the outer width of the rounded composer box: the full
// width minus one column reserved for the scrollbar gutter, floored so the box
// never collapses.
func (m *TranscriptModel) composerBoxWidth() int {
	return max(20, m.width-1)
}

// composerInnerWidth is the textarea's content/wrap width inside the box:
// box width minus border(2) + padding(2). layout() (which reserves body height
// from inputRows()) and renderInput() (which renders) MUST size the textarea
// with this same value, or the reserved height drifts from the rendered height
// at narrow widths.
func (m *TranscriptModel) composerInnerWidth() int {
	return m.composerBoxWidth() - 4
}

func (m *TranscriptModel) inputRows() int {
	n := m.input.LineCount()
	if n < 1 {
		n = 1
	}
	if n > maxInputRows {
		n = maxInputRows
	}
	return n
}

// renderUnreadDivider draws a subtle "new since you left" line.
func (m *TranscriptModel) renderUnreadDivider() string {
	w := m.width - 2
	if w < 10 {
		w = 10
	}
	left := "─ new ─"
	line := left + strings.Repeat("─", w-lipgloss.Width(left))
	return lipgloss.NewStyle().Foreground(theme.TextMuted).Render(line)
}

// A2.1 (Calm) role gutter. gutterInset is the left inset a guttered message (or
// a place-indented subordinate block) occupies: 1 pad column + the 2-cell role
// bar "▌ ". Wrapping blocks render that much narrower so the bar + text still fit
// the body width.
const gutterInset = 3

// gutterPrefix puts a slim role-colored bar (Charple for the assistant, Guac for
// the user) down the left of every line of a message block — replacing the old
// "❯ " prefix. The bar is its own styled span so it never bleeds into the line.
func gutterPrefix(s string, bar color.Color) string {
	b := lipgloss.NewStyle().Foreground(bar).Render("▌ ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = " " + b + l
	}
	return strings.Join(lines, "\n")
}

// placeIndent indents a subordinate block (tool card, footer, notice, reasoning)
// by gutterInset spaces so it aligns under the message column rather than under
// the role bar.
func placeIndent(s string) string {
	pad := strings.Repeat(" ", gutterInset)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// renderBlock renders a transcript block with its Calm chrome: user/assistant
// blocks get the role gutter bar; every other (subordinate) kind is indented to
// the message column. The bare content is produced by renderBlockBody; an empty
// body stays empty (no stray bar/indent on a blank line).
func (m *TranscriptModel) renderBlock(b *blockCard) string {
	raw := m.renderBlockBody(b)
	if raw == "" {
		return ""
	}
	switch b.kind {
	case blockUser:
		return gutterPrefix(raw, theme.Guac)
	case blockAssistant:
		return gutterPrefix(raw, theme.Charple)
	default:
		return placeIndent(raw)
	}
}

// assistantWrapWidth is the markdown word-wrap width for an assistant message
// body. It MUST be identical for the live streaming tail and the finalized block
// (T1): the tail wraps at this width while streaming, and if the finalized block
// wrapped even one column narrower, the extra wrapped lines would push the
// content up and the view would lurch off the bottom at message.completed. It
// reserves the gutter chrome (gutterInset) plus one column for the scrollbar.
func (m *TranscriptModel) assistantWrapWidth() int {
	w := m.width - 2 - gutterInset
	if w < 20 {
		w = 20
	}
	return w
}

// renderBlockBody renders a block's bare content (no gutter/indent). Wrapping
// kinds reserve gutterInset columns so the chrome added by renderBlock fits.
// renderAssistantMD renders assistant markdown through the pooled glamour
// renderer, falling back to a plainly-styled render when the renderer is
// unavailable or errors. The finalized-block path (renderBlockBody) and the live
// streaming path (streamAI) MUST share this so their output can't drift — a
// difference here reflows the block at message.completed and lurches the view
// (T1). Matches AssistantItem.SetRenderContentMD's signature.
func renderAssistantMD(text string, width int) string {
	r := chat.MarkdownRenderer(width)
	if r == nil {
		return styleTAssistant.Render(text)
	}
	out, err := r.Render(text)
	if err != nil {
		return styleTAssistant.Render(text)
	}
	return strings.TrimRight(out, "\n")
}

func (m *TranscriptModel) renderBlockBody(b *blockCard) string {
	switch b.kind {
	case blockUser:
		return styleTUser.Render(b.text)
	case blockAssistant:
		wrap := m.assistantWrapWidth()
		// Route assistant blocks through chat.AssistantItem + the pooled
		// glamour renderer (chat.MarkdownRenderer), replacing the per-layout
		// m.md allocation. RawRender emits no focus prefix, preserving
		// byte-for-byte parity with the former m.md.Render + TrimRight path.
		ai := chat.NewAssistantItem(&chat.AssistantMessage{Content: b.text, Finished: true})
		ai.SetRenderContentMD(renderAssistantMD)
		return ai.RawRender(wrap)
	case blockToolCard:
		if b.tool != nil {
			w := m.width - 2 - gutterInset
			if w < 10 {
				w = 10
			}
			return m.renderToolCard(b.tool, w)
		}
		return b.text
	case blockTool:
		return styleTTool.Render(b.text)
	case blockToolErr, blockError:
		return styleTError.Render(b.text)
	case blockInfo:
		return styleTInfo.Render(b.text)
	case blockWarn:
		return lipgloss.NewStyle().Foreground(theme.Warning).Render(b.text)
	case blockShell, blockFooter:
		// Pre-styled block; render verbatim.
		return b.text
	case blockSubagent:
		if b.sub != nil {
			w := m.width - 2 - gutterInset
			if w < 10 {
				w = 10
			}
			return m.renderSubagentCard(b.sub, w)
		}
	case blockReasoning:
		// Render reasoning/thinking text as a muted "Thought:" prefix box
		// (chat-rendering §4.4). Shown in a compact single-line summary when
		// short, or multi-line for longer reasoning.
		lines := strings.Count(b.text, "\n") + 1
		// "∴" keeps the geometric glyph vocabulary (◐◆❯○▌◇…) — emoji here broke
		// the set and renders double-width in some terminals.
		label := lipgloss.NewStyle().Foreground(theme.TextMuted).Bold(true).Render("∴ Thought")
		if lines <= 1 {
			return label + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(": "+b.text)
		}
		summary := firstLine(b.text)
		return label + lipgloss.NewStyle().Foreground(theme.TextMuted).
			Render(fmt.Sprintf(" (%d lines): %s…", lines, truncate(summary, 40)))
	}
	return b.text
}

// renderLiveReasoning renders the in-flight thinking text as a muted, italic,
// word-wrapped block under a "∴ Thinking" header — the live counterpart of the
// compact finalized blockReasoning summary (§2b gap 3). It shares placeIndent
// placement with the finalized block, so when the think collapses to its
// one-line summary at reasoning.completed the content stays in the same column.
// Empty text (thinking just started) shows the header alone as a live indicator.
func (m *TranscriptModel) renderLiveReasoning(text string) string {
	label := lipgloss.NewStyle().Foreground(theme.TextMuted).Bold(true).Render("∴ Thinking")
	text = strings.TrimSpace(text)
	if text == "" {
		return placeIndent(label)
	}
	body := lipgloss.NewStyle().Foreground(theme.TextMuted).Italic(true).
		Width(m.assistantWrapWidth()).Render(text)
	return placeIndent(label + "\n" + body)
}

// renderTodos formats a todo.updated checklist as one line per item with a
// status glyph (completed ✓, in_progress ▸, pending ○). For in-progress items
// the present-tense ActiveForm is preferred when set.
func renderTodos(todos []session.TodoItem) string {
	if len(todos) == 0 {
		return "▤ todo list cleared"
	}
	var b strings.Builder
	b.WriteString("▤ todo list")
	for _, t := range todos {
		var glyph string
		switch t.Status {
		case "completed":
			glyph = "✓"
		case "in_progress":
			glyph = "▸"
		default: // pending and any unknown status
			glyph = "○"
		}
		text := t.Content
		if t.Status == "in_progress" && t.ActiveForm != "" {
			text = t.ActiveForm
		}
		b.WriteString("\n  " + glyph + " " + text)
	}
	return b.String()
}

// Tool-card glyph vocabulary: the ⏺ status head bullet and the ⎿ result elbow.
// The bullet's color carries the running/ok/error signal; the elbow anchors the
// result column so a scan down the transcript reads results in one place.
const (
	toolHeadBullet = "⏺"
	toolElbow      = "⎿"
	// elbowChromeW is the display width of the "  ⎿  " elbow prefix (2-space
	// indent aligning the elbow under the tool name, elbow glyph, two spaces).
	elbowChromeW = 5
)

// toolBulletColor maps a tool card's status to its ⏺ head-bullet color via theme
// tokens (running=Malibu, ok=Guac, error=Coral), so a /theme swap re-skins it.
func toolBulletColor(s toolStatus) color.Color {
	switch s {
	case toolOK:
		return theme.Guac
	case toolErr:
		return theme.Coral
	default: // toolRunning
		return theme.Malibu
	}
}

// renderToolCard formats a tool call as the two-line ⏺-head + ⎿-elbow idiom:
//
//	⏺ Bash(npm test)
//	  ⎿  exit 0 · 42 lines (ctrl+o to expand)
//
// The head bullet is colored by status; the elbow shows the result summary (plus
// a dim ctrl+o hint when collapsed content exists), and when expanded the card
// reveals its available content (arg / edit diff / captured output). Every line
// is budgeted from the measured remaining width (ANSI-aware) and truncated as a
// backstop, so the card never overflows even at very narrow widths (§1c).
func (m *TranscriptModel) renderToolCard(c *toolCard, width int) string {
	if width < 4 {
		width = 4
	}

	// Line 1 — head: "⏺ Name(arg)". Bullet colored by status (A2.4: name muted,
	// arg dim; only the bullet keeps its color). The name takes what it needs and
	// the arg gets whatever remains, ellipsized.
	bullet := lipgloss.NewStyle().Foreground(toolBulletColor(c.status)).Render(toolHeadBullet)
	nameStr := c.tool
	if nameStr == "" {
		nameStr = "tool"
	}
	avail := width - 2 // "⏺ "
	name := truncate(nameStr, max(1, avail))
	head := lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(name)
	// argTruncated records whether the head could not show the argument in full,
	// so expansion only offers to reveal it when there is actually more to see.
	argTruncated := false
	if c.arg != "" {
		fullArg := collapseSpaces(c.arg)
		if argBudget := avail - lipgloss.Width(name) - 2; argBudget >= 3 {
			shown := truncate(fullArg, argBudget)
			head += lipgloss.NewStyle().Foreground(theme.TextMuted).Render("(" + shown + ")")
			argTruncated = shown != fullArg
		} else {
			argTruncated = true // no room to show the arg at all
		}
	}
	headLine := bullet + " " + head
	if lipgloss.Width(headLine) > width {
		headLine = truncate(headLine, width)
	}

	// Line 2 — elbow: "  ⎿  <result> (ctrl+o hint)".
	elbowText := c.summary
	if elbowText == "" {
		switch c.status {
		case toolRunning:
			elbowText = "running…"
		case toolErr:
			elbowText = "failed"
		default:
			elbowText = "done"
		}
	}
	elbowColor := theme.TextMuted
	if c.status == toolErr {
		elbowColor = theme.Coral
	}
	body := m.toolExpandBody(c, width-elbowChromeW, argTruncated)
	hint := ""
	if len(body) > 0 {
		if c.expanded {
			hint = "  (ctrl+o to collapse)"
		} else {
			hint = "  (ctrl+o to expand)"
		}
	}
	elbowAvail := width - elbowChromeW
	// Fit "<result> + hint" into elbowAvail: prefer the result, drop the hint if
	// it won't fit, then ellipsize the result as a last resort.
	if lipgloss.Width(elbowText)+lipgloss.Width(hint) > elbowAvail {
		hint = ""
	}
	if lipgloss.Width(elbowText) > elbowAvail {
		elbowText = truncate(elbowText, elbowAvail)
	}
	elbowLine := lipgloss.NewStyle().Foreground(theme.TextDim).Render("  "+toolElbow+"  ") +
		lipgloss.NewStyle().Foreground(elbowColor).Render(elbowText) +
		lipgloss.NewStyle().Foreground(theme.TextDim).Render(hint)
	if lipgloss.Width(elbowLine) > width {
		elbowLine = truncate(elbowLine, width)
	}

	lines := []string{headLine, elbowLine}
	if c.expanded {
		lines = append(lines, body...)
	}
	return strings.Join(lines, "\n")
}

// tool-card expansion caps: the condensed edit-diff line cap, and the head/tail
// line window for captured output so a huge dump can't blow up a single card.
const (
	toolExpandDiffMax   = 16
	toolExpandHeadLines = 20
	toolExpandTailLines = 6
)

// toolExpandBody builds the expanded content lines for a tool card, aligned under
// the elbow's result column. It shows the edit diff for edit-like tools (reusing
// the permission_diff machinery so a post-approval diff stays viewable), the
// captured output for output-producing tools (display-capped head+tail), or the
// full argument as a fallback. width is the content width available after the
// elbow chrome; every line is truncated to it so a narrow terminal never
// overflows. Returns nil when there is nothing to expand.
func (m *TranscriptModel) toolExpandBody(c *toolCard, width int, argTruncated bool) []string {
	if width < 4 {
		width = 4
	}
	var content []string
	// Edit-like tools (Edit/Write/MultiEdit): reuse permissionDiffStat for a
	// colored +/− diff, so the diff survives past the permission box that showed it.
	if _, _, dl := permissionDiffStat(c.tool, c.input); len(dl) > 0 {
		for _, l := range condenseDiff(dl, toolExpandDiffMax) {
			content = append(content, styleDiffLine(l))
		}
	}
	// Captured output (Bash/Read/…): capped head+tail, ANSI remapped to the palette.
	if c.output != "" {
		for _, l := range clampOutputLines(c.output) {
			content = append(content, kit.RemapANSI(l))
		}
	}
	// Nothing structured to show, but the head truncated the argument — reveal it
	// in full so expansion isn't a no-op.
	if len(content) == 0 && argTruncated && c.arg != "" {
		content = append(content, collapseSpaces(c.arg))
	}
	if len(content) == 0 {
		return nil
	}
	out := make([]string, len(content))
	for i, l := range content {
		out[i] = strings.Repeat(" ", elbowChromeW) + truncate(l, width)
	}
	return out
}

// clampOutputLines splits captured tool output into display lines, keeping the
// first toolExpandHeadLines and last toolExpandTailLines with a "… N lines
// hidden …" marker between them when longer. Trailing blank lines are trimmed.
func clampOutputLines(out string) []string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	if len(lines) <= toolExpandHeadLines+toolExpandTailLines+1 {
		return lines
	}
	hidden := len(lines) - toolExpandHeadLines - toolExpandTailLines
	res := make([]string, 0, toolExpandHeadLines+toolExpandTailLines+1)
	res = append(res, lines[:toolExpandHeadLines]...)
	res = append(res, "… "+formatInt(hidden)+" lines hidden …")
	res = append(res, lines[len(lines)-toolExpandTailLines:]...)
	return res
}

// styleDiffLine colors a unified-diff line by its prefix ("+" add, "−" del, "…"
// elision, " " context). Shared by the permission box and the expanded tool card.
func styleDiffLine(l string) string {
	var c color.Color
	switch {
	case strings.HasPrefix(l, "+"):
		c = theme.Guac
	case strings.HasPrefix(l, "−"):
		c = theme.Coral
	case strings.HasPrefix(l, "…"):
		c = theme.TextDim
	default: // context (" " prefix)
		c = theme.TextMuted
	}
	return lipgloss.NewStyle().Foreground(c).Render(l)
}

// toolArg extracts the most informative single argument from a tool's input
// for the card label: a file path, command, pattern, or url.
func toolArg(tool string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	get := func(keys ...string) string {
		var raw map[string]any
		if json.Unmarshal(input, &raw) != nil {
			return ""
		}
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
			return shortenPath(p)
		}
	case "Bash":
		return collapseSpaces(get("command"))
	case "Grep":
		return get("pattern")
	case "Glob":
		return get("pattern")
	case "WebFetch":
		return get("url")
	case "WebSearch":
		return get("query")
	}
	// Fall back to a path-ish or query-ish field if present.
	if p := get("file_path", "path", "command", "pattern", "url", "query"); p != "" {
		return collapseSpaces(p)
	}
	return ""
}

// toolSummary condenses a tool's output into a short result note.
func toolSummary(output string) string {
	if output == "" {
		return ""
	}
	n := strings.Count(output, "\n")
	if strings.TrimRight(output, "\n") != output {
		n--
	}
	if n >= 1 {
		return formatInt(n+1) + " lines"
	}
	return collapseSpaces(firstLine(output))
}

// shortenPath trims a long absolute path to its last two segments.
func shortenPath(p string) string {
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// collapseSpaces flattens runs of whitespace (incl. newlines) into single
// spaces so a multi-line command renders on one card line.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// --------------------------------------------------------------------------
// Rendering helpers
// --------------------------------------------------------------------------

// chatStatusLabel maps a session status to a single-session, action-oriented
// label for the chat header (T12). The dashboard's SessionStatus.String() names
// the internal state ("needs-input" for a finished turn, "waiting" for a pending
// permission), which a user reads backwards — "needs-input" looks like the agent
// is blocked on you when it actually means done/ready, and "waiting" is the one
// that truly needs you. These labels say what's true from your seat.
func chatStatusLabel(s SessionStatus) string {
	switch s {
	case StatusBusy:
		return "working"
	case StatusWaiting:
		return "awaiting approval"
	case StatusNeedsInput:
		return "ready for input"
	case StatusIdle:
		return "idle"
	case StatusSuspended:
		return "suspended"
	case StatusFailed:
		return "failed"
	default:
		return s.String()
	}
}

func (m *TranscriptModel) renderHeader() string {
	left := styleDetailTitle.Render(m.title)

	var right string
	if m.reconnectGaveUp {
		right = styleTError.Render("session gone")
	} else if m.reconnecting {
		// Show the live connect stage (FU1) — "reconnecting — Starting pod" — so a
		// slow cold-pod resume reads as real progress, falling back to a plain
		// label until the first stage arrives. Elapsed time is appended (Fix D).
		label := "reconnecting…"
		if m.reconnectStageKnown {
			label = "reconnecting — " + connectStageLabel(m.reconnectStage)
			if m.reconnectDetail != "" {
				label += " " + m.reconnectDetail
			}
		}
		if !m.reconnectStartedAt.IsZero() {
			if el := nowFunc().Sub(m.reconnectStartedAt); el >= time.Second {
				label += fmt.Sprintf(" (%s)", roundDur(el))
			}
		}
		right = styleTError.Render(label)
	} else {
		glyph := glyphStyle(m.DashStatus).Render(m.DashStatus.Glyph() + " " + chatStatusLabel(m.DashStatus))
		meta := styleTInfo.Render(m.agent + " · " + filepath.Base(m.projectPath))
		right = meta + "  " + glyph
	}

	// spread truncates a long title rather than letting it overflow and clip the
	// status glyph / reconnect state off the right edge (§1c spot 1).
	return spread(left, right, m.width)
}

func (m *TranscriptModel) renderInput() string {
	// The transcript opens in NORMAL (vim) mode with the prompt blurred, which
	// isn't discoverable — a new user doesn't know to press i to type (T13). Spell
	// it out in the placeholder and the hint, in plain language (not "insert").
	if m.imode == modeInsert {
		m.input.Placeholder = "type a message…"
	} else {
		m.input.Placeholder = "press i to type a message…"
	}

	// The composer sits in a rounded box that spans the body width (one column
	// reserved for the scrollbar gutter). Its border brightens to Charple when
	// you're typing (INSERT) and stays quiet otherwise, so the box itself signals
	// focus instead of a separate badge.
	boxW := m.composerBoxWidth()
	m.input.SetWidth(m.composerInnerWidth())
	m.input.SetHeight(m.inputRows())
	borderColor := theme.BorderMedium
	if m.imode == modeInsert {
		borderColor = theme.Charple
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(boxW - 2). // content width; the border adds the other 2 columns
		Render(m.input.View())

	// A thin row under the box: the vim-mode badge on the left (only when modal
	// editing is on), and the live working/loading indicator (or key hints)
	// right-aligned.
	var right string
	switch {
	case m.vimEnabled && m.imode == modeNormal:
		right = kit.KbdRow([2]string{"i", "type"}, [2]string{"q", "detach"})
	case m.vimEnabled:
		right = kit.KbdRow([2]string{"↵", "send"}, [2]string{"⇧↵", "newline"})
	default:
		// Vim off: the prompt is always focused, so surface how to leave (esc when
		// idle, or ctrl+]) instead of the modal "i to type" hint.
		right = kit.KbdRow([2]string{"↵", "send"}, [2]string{"esc", "detach"})
	}
	if m.replaying {
		right = m.loadingStatus()
	} else if m.turnActive {
		right = m.workingStatus()
	}
	badge := ""
	if m.vimEnabled {
		badge = m.modeBadge()
	}
	// A queued prompt was previously invisible: submit-during-turn made the text
	// vanish with no cue that it would send later (and silently changed what esc
	// does). Surface it as a chip, with the steer affordance spelled out.
	if m.queuedPrompt != "" {
		chip := lipgloss.NewStyle().Foreground(theme.Gold).
			Render("↳ queued: "+truncate(m.queuedPrompt, max(8, m.width/3))) +
			styleSLMuted.Render(" · esc sends now")
		if badge != "" {
			badge += " "
		}
		badge += chip
	}
	// A running /loop or /goal shows a persistent chip so it's clear a driver is
	// firing turns and how to stop it. Empty when off — the idle hint row is
	// unchanged.
	if chip := m.autopilotChip(); chip != "" {
		if badge != "" {
			badge += " "
		}
		badge += chip
	}
	// spread truncates the (already internally-clipped) chips rather than letting
	// them overflow and clip the send/esc affordance off the right edge (§1c spot 1).
	hint := spread(badge, right, m.width)

	return box + "\n" + hint
}

// workTickMsg drives the working-indicator clock/spinner while a turn runs.
type workTickMsg struct{}

// workTickInterval is the refresh cadence of the working indicator.
const workTickInterval = 150 * time.Millisecond

func workTickCmd() tea.Cmd {
	return tea.Tick(workTickInterval, func(time.Time) tea.Msg { return workTickMsg{} })
}

// maybeStartWorking schedules the work-tick loop if a turn is active and the
// loop is not already running. Returns nil otherwise so no timer runs idle.
func (m *TranscriptModel) maybeStartWorking() tea.Cmd {
	// Don't animate "working" while replaying history (Workstream C): a replayed
	// turn.started must not drive the live spinner. Once the boundary flips
	// replaying false, the next call starts the loop if the turn is still active.
	if !m.working && m.turnActive && !m.replaying {
		m.working = true
		return workTickCmd()
	}
	return nil
}

// loadingStatus renders the prompt-line indicator shown while catching up
// historical events after an attach/reconnect (Workstream C): an honest "loading
// transcript…" with the count caught up so far, instead of the live "working…"
// spinner that made replay feel like the model was running (#1).
func (m *TranscriptModel) loadingStatus() string {
	ell := anim.Ellipsis(m.workFrame / spinnerSubRate)
	if anim.ReduceMotion() {
		ell = "…"
	}
	msg := "loading transcript" + ell
	if m.replayedCount > 0 {
		msg += fmt.Sprintf(" %d", m.replayedCount)
	}
	return lipgloss.NewStyle().Foreground(theme.Malibu).Render("⟳ " + msg)
}

// workingStatus renders the live indicator shown on the prompt line during a
// turn: spinner · elapsed · token counts · cost.
func (m *TranscriptModel) workingStatus() string {
	spin := theme.SpinnerFrame(m.workFrame)
	// Animated "working" ellipsis at a slower sub-rate than the spinner (§C.3),
	// collapsed to a static "…" under reduce-motion (§E).
	ell := anim.Ellipsis(m.workFrame / spinnerSubRate)
	if anim.ReduceMotion() {
		ell = "…"
	}
	working := styleSLBusy.Render("working" + ell)
	out := spin + " " + working + "  " +
		styleSLLabel.Render(fmtElapsed(nowFunc().Sub(m.turnStart)))
	if m.InputTokens > 0 || m.OutputTokens > 0 {
		out += styleSLMuted.
			Render(fmt.Sprintf("  ↑%s ↓%s", kit.FormatTokens(m.InputTokens), kit.FormatTokens(m.OutputTokens)))
	}
	if m.TotalCostUSD > 0 {
		out += styleSLCost.Render("  " + kit.FormatCost(m.TotalCostUSD))
	}
	return out
}

// turnFooter renders the dim per-turn footer (§D): a diamond, the model, the
// client, elapsed, token in/out, and cost — e.g.
// "◇ Opus 4.8 · via anthropic · 12s · ↑3.1k ↓820 · $0.04". Empty when there is
// nothing meaningful to summarize.
func (m *TranscriptModel) turnFooter() string {
	var parts []string
	// Skip the model segment entirely before session.started delivers it — a
	// literal "◇ —" placeholder reads as a glitch.
	if m.Model != "" {
		parts = append(parts, shortModelName(m.Model))
	}
	if m.agent != "" {
		parts = append(parts, "via "+MarkedClientLabel(m.agent))
	}
	if !m.turnStart.IsZero() {
		parts = append(parts, fmtElapsed(nowFunc().Sub(m.turnStart)))
	}
	if m.InputTokens > 0 || m.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("↑%s ↓%s", kit.FormatTokens(m.InputTokens), kit.FormatTokens(m.OutputTokens)))
	}
	if m.TotalCostUSD > 0 {
		parts = append(parts, kit.FormatCost(m.TotalCostUSD))
	}
	if len(parts) == 0 {
		return ""
	}
	return lipgloss.NewStyle().Foreground(theme.TextMuted).Render("◇ " + strings.Join(parts, " · "))
}

// fmtElapsed renders a duration as a compact clock (e.g. "12s", "1m03s").
func fmtElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return formatInt(s) + "s"
	}
	mn := s / 60
	sec := s % 60
	pad := ""
	if sec < 10 {
		pad = "0"
	}
	return formatInt(mn) + "m" + pad + formatInt(sec) + "s"
}

// compactTokens renders a token count as e.g. "340" or "1.2k".
func compactTokens(n int) string {
	if n < 1000 {
		return formatInt(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// tailLines returns up to n plain text lines from the end of the transcript
// body, for the dashboard detail-pane preview of a warm session. It seeds the
// model's size for the requested width first (it may have been built in the
// background without a layout).
func (m *TranscriptModel) tailLines(n, width int) []string {
	m.seedSize(width, max(n+4, 8))
	body := m.bodyView()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	// Drop trailing blank padding lines so the preview hugs real content.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// seedSize applies a terminal size to a model that was built in the background
// (and so never received a WindowSizeMsg). It mirrors the WindowSizeMsg handler
// so the model lays out correctly before its first foreground View.
func (m *TranscriptModel) seedSize(w, h int) {
	m.width, m.height = w, h
	m.layout()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
