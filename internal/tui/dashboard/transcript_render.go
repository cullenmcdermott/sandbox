package dashboard

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/chat"
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

// --------------------------------------------------------------------------
// Vertical layout regions
// --------------------------------------------------------------------------
//
// The transcript is a vertical stack of bands (header, divider, body, optional
// permission/palette/search, composer, status line — or, in the connect
// preview, a banner). Historically each consumer hand-counted the stack:
// layout() reserved the body height with a bare `-3`, renderTranscript()
// re-assembled the same bands into a parts slice, previewView() used its own
// `h-3-bannerH`, and the scrollbar hit-test hard-coded `bodyTop=2`. Any layout
// change had to be mirrored in every copy, and a missed copy silently broke
// mouse mapping. These types make the stack declarative: one builder produces an
// ordered []region with the body as the flex band, and ALL consumers walk it —
// rendering, body sizing, and hit-testing read the same numbers. Adding,
// removing, or resizing a band is a one-line change to the builder.

// region is one horizontal band in the transcript's vertical stack: a name, its
// height in rows for the current frame, and a renderer that produces exactly
// that many rows.
type region struct {
	name   string
	height int
	render func() string
}

// Region names. The body is the single flex band (its height absorbs whatever
// the fixed bands leave); every other band has a fixed, self-measured height.
const (
	regionHeader     = "header"
	regionDivider    = "divider"
	regionBody       = "body"
	regionPerm       = "perm"
	regionPalette    = "palette"
	regionSearch     = "search"
	regionGap        = "gap"
	regionWorking    = "working"
	regionComposer   = "composer"
	regionStatusLine = "statusline"
	regionBanner     = "banner"
)

// vlayout is the transcript's per-frame vertical layout: the ordered region
// stack and the frame's total height. It is the single source of the stack
// arithmetic, so render and hit-test can never drift.
type vlayout struct {
	regions []region
	total   int
}

// top returns the 0-based row of the named band's first line within the frame
// (the summed height of every band above it), or -1 if the band is absent.
func (v vlayout) top(name string) int {
	y := 0
	for _, r := range v.regions {
		if r.name == name {
			return y
		}
		y += r.height
	}
	return -1
}

// heightOf returns the named band's row height, or 0 if it is absent this frame.
func (v vlayout) heightOf(name string) int {
	for _, r := range v.regions {
		if r.name == name {
			return r.height
		}
	}
	return 0
}

// view composites the stack top-to-bottom. Bands are joined with newlines
// exactly as the former hand-assembled parts slice was; each band's render must
// emit its declared height so the frame sums to total.
func (v vlayout) view() string {
	parts := make([]string, len(v.regions))
	for i, r := range v.regions {
		parts[i] = r.render()
	}
	return strings.Join(parts, "\n")
}

// headerBands are the fixed bands stacked above the body. §2c dropped the
// persistent title bar: normally there are NONE and the body starts at row 0 —
// the statusline owns identity and the terminal tab owns the title (see
// App.windowTitle), so a permanent top band only duplicated them. The two-band
// alert (title header + divider) returns ONLY in the exceptional connect states
// (reconnecting / gave up), where an in-frame alert is still warranted because a
// live error doesn't belong solely in a possibly-hidden tab title. bodyTop, the
// scrollbar hit-test, and the live/preview layouts all walk this, so they follow
// automatically.
func (m *TranscriptModel) headerBands() []region {
	if !m.reconnecting && !m.reconnectGaveUp {
		return nil
	}
	return []region{
		{regionHeader, 1, m.renderHeader},
		{regionDivider, 1, func() string { return styleDivider.Render(strings.Repeat("─", m.width)) }},
	}
}

// bodyTop is the 0-based row of the transcript body's first line — the combined
// height of the bands above it. The scrollbar hit-test uses it so mouse mapping
// follows the same band definitions the renderer stacks.
func (m *TranscriptModel) bodyTop() int {
	y := 0
	for _, r := range m.headerBands() {
		y += r.height
	}
	return y
}

// stack assembles a vlayout from the fixed bands above and below a single flex
// body band. The body height is the frame total minus every fixed band (floored
// at 1); its renderer is supplied by the caller (the live view shows the
// empty-session welcome when there is nothing yet; the preview shows the plain
// body under its banner). Sizing the list widget to heightOf(regionBody) stays
// the caller's job — this only computes the geometry.
func (m *TranscriptModel) stack(above, below []region, bodyView func() string) vlayout {
	fixed := 0
	for _, r := range above {
		fixed += r.height
	}
	for _, r := range below {
		fixed += r.height
	}
	bodyH := m.height - fixed
	if bodyH < 1 {
		bodyH = 1
	}
	regions := make([]region, 0, len(above)+1+len(below))
	regions = append(regions, above...)
	regions = append(regions, region{regionBody, bodyH, bodyView})
	regions = append(regions, below...)
	return vlayout{regions: regions, total: m.height}
}

// liveLayout builds the attached (composer) transcript's region stack at the
// current size. Fixed bands measure themselves exactly as the former
// layout()/renderTranscript() pair did; the body flexes to fill the rest. It
// builds m.palette as a side effect so the render closure reuses the same
// string the height was measured from. The permission region is owned by the
// §2a permPrompt component (permprompt.go): its static body is cached and its
// border fade assembles live in Render, so Height/Render share one discipline
// and the closure needs no plan-vs-tool branch.
func (m *TranscriptModel) liveLayout() vlayout {
	// Size the composer's textarea first so inputRows() (which wraps on this
	// width) is accurate before its height is reserved. Must match renderInput()
	// exactly, or the reserved height drifts from what renders.
	m.input.SetWidth(m.composerInnerWidth())

	var below []region

	// Inline permission / plan-approval box, when one is pending — one component
	// for both variants. Height is fade-stable (color-only), so the reserved band
	// matches what Render draws every frame.
	if m.pending != nil {
		p := m.permComp()
		permH := p.Height(m.width)
		below = append(below, region{regionPerm, permH, func() string { return p.Render(m.width) }})
	}

	// Slash-command palette, when the composer starts with '/'.
	m.palette = ""
	if m.paletteOpen() {
		m.palette = m.renderPalette(m.width)
		palH := strings.Count(m.palette, "\n") + 1
		below = append(below, region{regionPalette, palH, func() string { return m.palette }})
	}

	// The search bar (T3) consumes one row just above the composer when open.
	if m.search.open {
		below = append(below, region{regionSearch, 1, func() string { return m.renderSearchBar(m.width) }})
	}

	// A blank line sets the composer apart from the transcript (roominess). While a
	// turn runs (or history replays) a single left-aligned working-indicator line
	// sits directly above the composer — "✳ Thinking… (12s · ↓820 tokens · esc to
	// interrupt)" — so the live state and the esc affordance are one glance above
	// the input. It is absent when idle, so the idle layout is byte-identical.
	below = append(below, region{regionGap, 1, func() string { return "" }})
	if m.workingLine() != "" {
		below = append(below, region{regionWorking, 1, func() string {
			return truncate(m.workingLine(), max(1, m.width))
		}})
	}
	// Then the boxed composer (border 2 + rows + hint row 1 = inputRows()+3) and
	// the status line (1 quiet row, +1 while the transient rate-limit row shows).
	below = append(below,
		region{regionComposer, m.inputRows() + 3, m.renderInput},
		region{regionStatusLine, m.statusLineHeight(), m.renderStatusLine},
	)

	return m.stack(m.headerBands(), below, m.liveBodyView)
}

// liveBodyView renders the body band for the attached view: the scrollable list,
// or a brief welcome for a fresh session so it isn't a blank void (parity with
// the dashboard's firstRunView). It sizes to the list's current height, which
// layout() set from heightOf(regionBody).
func (m *TranscriptModel) liveBodyView() string {
	if m.transcriptEmpty() {
		return m.emptyTranscriptView(max(1, m.width-1), m.body.Height())
	}
	return m.bodyView()
}

// previewLayout builds the connect-preview region stack: the same header/divider
// and body, but a connect banner where the composer would be (read-only, no
// input). The body stays plain — no empty-session welcome under the banner.
func (m *TranscriptModel) previewLayout(banner string) vlayout {
	below := []region{
		{regionGap, 1, func() string { return "" }},
		{regionBanner, lipgloss.Height(banner), func() string { return banner }},
	}
	return m.stack(m.headerBands(), below, m.bodyView)
}

// renderTranscript builds the attached transcript string for the current size.
func (m *TranscriptModel) renderTranscript(w, h int) string {
	return m.liveLayout().view()
}

// previewView renders the transcript's history (header + divider + scrollable
// body) with a connect banner where the composer would normally sit. It is used
// during ScreenConnecting (Fix A) so a session's conversation is visible while
// its pod resumes, instead of a blank splash. Read-only: no input box.
func (m *TranscriptModel) previewView(w, h int, banner string) string {
	m.width, m.height = w, h
	v := m.previewLayout(banner)
	m.body.SetSize(max(1, w-1), v.heightOf(regionBody))
	m.syncItems()
	m.body.GotoBottom()
	return v.view()
}

// --------------------------------------------------------------------------
// Layout
// --------------------------------------------------------------------------

// layout (re)sizes the list body and reconciles items. It is called on resize
// and whenever a band appears/disappears (permission box, palette, search) or
// the composer grows, since those change the flex body height. The band
// geometry lives in liveLayout; layout only applies the body height it computes.
func (m *TranscriptModel) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	v := m.liveLayout()
	// Reserve one column on the right for the transcript scrollbar (§D).
	m.body.SetSize(max(1, m.width-1), v.heightOf(regionBody))
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
	// bubbles v2.1.1 textarea.LineCount() returns len(m.value) — the count of
	// LOGICAL lines only — so soft-wrapped text scrolls inside one row and the box
	// never grows until a hard newline. Count VISUAL rows instead: sum, over each
	// logical line, the soft-wrapped row count at the textarea's own content width
	// (m.input.Width(), which already subtracts the 2-col prompt). We mirror the
	// textarea's placeholderView wrapping (word-wrap, then hard-wrap long words) at
	// that same width; an off-by-one vs the textarea's internal wrap is acceptable.
	w := m.input.Width()
	var n int
	if w <= 0 {
		n = m.input.LineCount() // width not yet set (pre-layout) — logical fallback
	} else {
		for _, line := range strings.Split(m.input.Value(), "\n") {
			wrapped := ansi.Hardwrap(ansi.Wordwrap(line, w, ""), w, true)
			n += strings.Count(wrapped, "\n") + 1
		}
	}
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

// §2c message grammar. msgIndent is the hanging-indent width a user or assistant
// message occupies: the 2-cell head prefix ("⏺ " for the assistant bullet, "> "
// for the quiet user prompt) on the first line, matched by a 2-space indent on
// every wrapped/continuation line so the block reads as one column. Wrapping
// blocks render that much narrower so the prefix + text still fit the body width.
//
// This replaced the full-height role gutter bars (a Charple/Guac "▌ " down every
// line): those colored bars were the largest departure from Claude Code's look.
// The ⏺ bullet is a neutral marker (theme.TextMuted) so the transcript's only
// colored signals are the tool-card status hues (running/ok/err) — role hue no
// longer competes with status.
const msgIndent = 2

// hangingPrefix applies a 2-cell head prefix to the first line of a message
// block and a 2-space indent to every continuation line, so a wrapped message
// aligns under its bullet/quote rather than back at column 0. An empty input
// stays empty (no stray prefix on a blank line — handled by renderBlock).
func hangingPrefix(s, firstPrefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = firstPrefix + lines[i]
		} else {
			lines[i] = "  " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// bulletPrefix heads an assistant message with a single "⏺ " bullet (CC's
// per-turn action grammar), colored bar for status but neutral for plain text.
// Leading blank lines are dropped first: glamour emits a blank document-margin
// line before the first paragraph, which would otherwise orphan the ⏺ on its own
// line above the text. finalizeStreaming and renderStreamTail both feed
// trimLeadingBlankLines-cleaned bodies, so the tail and the finalized block stay
// the same height (T1).
func bulletPrefix(s string, bullet color.Color) string {
	head := lipgloss.NewStyle().Foreground(bullet).Render(toolHeadBullet) + " "
	return hangingPrefix(trimLeadingBlankLines(s), head)
}

// trimLeadingBlankLines drops leading whitespace-only lines from s.
func trimLeadingBlankLines(s string) string {
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 || strings.TrimSpace(s[:i]) != "" {
			return s
		}
		s = s[i+1:]
	}
}

// quotePrefix heads a user message with a dim "> " — the user's own words are the
// quietest element in the transcript, not the loudest (they used to be Bold+Guac
// behind a colored bar).
func quotePrefix(s string) string {
	head := lipgloss.NewStyle().Foreground(theme.TextDim).Render("> ")
	return hangingPrefix(s, head)
}

// placeIndent indents a subordinate block (footer, notice, reasoning) by
// msgIndent spaces so it aligns under the message content column rather than at
// column 0. Tool/subagent cards are NOT place-indented: they carry their own ⏺
// head at column 0 (aligned with the assistant bullets), so renderBlock passes
// them through verbatim.
func placeIndent(s string) string {
	pad := strings.Repeat(" ", msgIndent)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// renderBlock renders a transcript block with its §2c chrome: an assistant block
// gets the neutral ⏺ bullet + hanging indent; a user block gets the dim "> "
// quote; tool/subagent cards keep their own ⏺ head at column 0; every other
// (subordinate) kind is indented to the message content column. The bare content
// is produced by renderBlockBody; an empty body stays empty (no stray prefix on a
// blank line).
func (m *TranscriptModel) renderBlock(b *blockCard) string {
	raw := m.renderBlockBody(b)
	if raw == "" {
		return ""
	}
	switch b.kind {
	case blockUser:
		return quotePrefix(raw)
	case blockAssistant:
		return bulletPrefix(raw, theme.TextMuted)
	case blockToolCard, blockSubagent:
		// The card owns its ⏺ head at column 0, aligned with assistant bullets.
		return raw
	default:
		return placeIndent(raw)
	}
}

// assistantWrapWidth is the markdown word-wrap width for an assistant message
// body. It MUST be identical for the live streaming tail and the finalized block
// (T1): the tail wraps at this width while streaming, and if the finalized block
// wrapped even one column narrower, the extra wrapped lines would push the
// content up and the view would lurch off the bottom at message.completed. It
// reserves the ⏺ hanging indent (msgIndent) plus a scrollbar + margin column.
func (m *TranscriptModel) assistantWrapWidth() int {
	w := m.width - 2 - msgIndent
	if w < 20 {
		w = 20
	}
	return w
}

// cardWidth is the render width for a tool or subagent card. The card owns its ⏺
// head at column 0 (aligned with the assistant bullets), so — unlike a wrapped
// message body — it reserves only the scrollbar column + one right margin, not a
// hanging indent. renderBlockBody and toolCardExpandable share it so the elbow
// hint, the toggle gate (H7), and the card's own render can never disagree.
func (m *TranscriptModel) cardWidth() int {
	w := m.width - 2
	if w < 10 {
		w = 10
	}
	return w
}

// renderBlockBody renders a block's bare content (no prefix/indent). Message
// bodies reserve msgIndent columns (assistantWrapWidth); cards use cardWidth so
// their ⏺ head sits at column 0 where renderBlock passes them through verbatim.
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
		// Word-wrap the prompt at the same width discipline as assistant bodies
		// (assistantWrapWidth reserves msgIndent for the "> " hanging indent plus
		// the scrollbar + margin column), so a long prompt wraps into the frame
		// instead of rendering as one clipped line at the edge. renderBlock then
		// adds the dim "> " quote via quotePrefix (continuation lines indent under
		// the text, not the ">").
		return styleTUser.Width(m.assistantWrapWidth()).Render(b.text)
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
			return m.renderToolCard(b.tool, m.cardWidth())
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
	case blockTodos:
		// The single pinned todo checklist (§2b). The payload lives on the model
		// (m.todoItems), not the card, so a mutate-in-place update re-renders the
		// current list; the card only bumps its version to invalidate the cache.
		return renderTodos(m.todoItems)
	case blockSubagent:
		if b.sub != nil {
			return m.renderSubagentCard(b.sub, m.cardWidth())
		}
	case blockReasoning:
		// Render reasoning/thinking text as a muted "∴ Thought" block. A single
		// raw line stays a compact inline summary; a multi-line think shows an
		// italic muted body — the SAME shape the live "∴ Thinking" tail shows
		// (§2c), so the block doesn't change form when it commits. The collapsed
		// body is capped at reasoningCapLines wrapped lines with a dim
		// "… +N lines (ctrl+o)" trailer; ctrl+o (toggleLatestExpandable) flips
		// b.expanded to reveal the full body.
		// "∴" keeps the geometric glyph vocabulary (◐◆❯○▌◇…) — emoji here broke
		// the set and renders double-width in some terminals.
		label := lipgloss.NewStyle().Foreground(theme.TextMuted).Bold(true).Render("∴ Thought")
		if strings.Count(b.text, "\n") == 0 {
			return label + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(": "+b.text)
		}
		lines := m.reasoningWrappedLines(b.text)
		if b.expanded || len(lines) <= reasoningCapLines {
			return label + "\n" + strings.Join(lines, "\n")
		}
		hidden := len(lines) - reasoningCapLines
		trailer := lipgloss.NewStyle().Foreground(theme.TextDim).
			Render(fmt.Sprintf("… +%d lines (ctrl+o)", hidden))
		return label + "\n" + strings.Join(lines[:reasoningCapLines], "\n") + "\n" + trailer
	}
	return b.text
}

// reasoningCapLines is how many wrapped body lines a thinking block shows before
// it caps — the same window for the committed "∴ Thought" block and the live
// "∴ Thinking" tail (§2c, Claude-Code parity), so the block keeps one shape from
// streaming through commit. Committed collapses cap the FIRST lines + a
// "… +N lines (ctrl+o)" trailer; the live tail follows the LAST lines (a
// terminal-style tail) + a "… +N earlier lines" marker.
const reasoningCapLines = 6

// reasoningWrappedLines word-wraps a committed think body at the assistant wrap
// width and returns the styled (italic + muted) wrapped lines. Committed blocks
// render once per version, so wrapping the whole buffer here is fine — this is
// deliberately NOT the E6 live hot path (that stays in wrapLiveReasoning, whose
// prefix cache we never touch). Both the render and toggleLatestExpandable's
// "is anything hidden?" gate call this, so their line counts can't disagree.
func (m *TranscriptModel) reasoningWrappedLines(text string) []string {
	w := m.assistantWrapWidth()
	style := lipgloss.NewStyle().Foreground(theme.TextMuted).Italic(true).Width(w)
	return strings.Split(style.Render(strings.TrimSpace(text)), "\n")
}

// renderLiveReasoning renders the in-flight thinking text as a muted, italic,
// word-wrapped block under a "∴ Thinking" header — the live counterpart of the
// finalized blockReasoning block (§2b gap 3). It shares placeIndent placement
// with the finalized block, so when the think commits at reasoning.completed the
// content stays in the same column. Empty text (thinking just started) shows the
// header alone as a live indicator. The body is capped to the LAST
// reasoningCapLines wrapped lines — the window follows the newest text like a
// terminal tail, giving the live think the same 6-line shape as the committed
// block (§2c). When lines are hidden above the window, a dim "… +N earlier
// lines" marker is prepended (mirrors the committed trailer, reversed because
// the window tails the newest text).
func (m *TranscriptModel) renderLiveReasoning(text string) string {
	label := lipgloss.NewStyle().Foreground(theme.TextMuted).Bold(true).Render("∴ Thinking")
	text = strings.TrimSpace(text)
	if text == "" {
		return placeIndent(label)
	}
	// Slice the OUTPUT of wrapLiveReasoning (keep the last reasoningCapLines) —
	// the E6 prefix cache inside it is untouched.
	lines := strings.Split(m.wrapLiveReasoning(text), "\n")
	body := strings.Join(lines, "\n")
	if len(lines) > reasoningCapLines {
		hidden := len(lines) - reasoningCapLines
		marker := lipgloss.NewStyle().Foreground(theme.TextDim).
			Render(fmt.Sprintf("… +%d earlier lines", hidden))
		body = marker + "\n" + strings.Join(lines[len(lines)-reasoningCapLines:], "\n")
	}
	return placeIndent(label + "\n" + body)
}

// wrapLiveReasoning word-wraps the live think body incrementally (§4 E6, Option
// A: prefix cache). Wrapping the whole buffer every delta is O(buffer²) over a
// long think; instead we wrap each COMPLETE line (everything up to the last '\n')
// exactly once — lipgloss wraps hard lines independently, so a cached line's wrap
// never changes as later text arrives — and re-wrap only the trailing partial
// line each frame. Output is byte-identical to a single .Width(w).Render(text)
// because text is already TrimSpace'd (so it never ends in '\n', the one case
// where per-line and whole-text rendering diverge). The cache self-invalidates on
// width/epoch change or when the buffer shrank below what we cached (a reset /
// new think), and resetReasoningWrapCache clears it at every reasoningBuf.Reset.
func (m *TranscriptModel) wrapLiveReasoning(text string) string {
	w := m.assistantWrapWidth()
	epoch := theme.Epoch()
	style := lipgloss.NewStyle().Foreground(theme.TextMuted).Italic(true).Width(w)

	// prefixLen is the byte offset just past the last newline: text[:prefixLen] is
	// the complete-lines region (ends in '\n'), text[prefixLen:] the partial tail.
	prefixLen := strings.LastIndexByte(text, '\n') + 1

	// Invalidate the cache if the wrap key changed or the buffer shrank below the
	// cached region (D4 reset, or a fresh think reusing the same model).
	if w != m.reasoningWrapWidth || epoch != m.reasoningWrapEpoch || prefixLen < m.reasoningWrapLen {
		m.reasoningWrapCache = ""
		m.reasoningWrapLen = 0
		m.reasoningWrapWidth = w
		m.reasoningWrapEpoch = epoch
	}

	// Extend the cache with any newly-completed lines (the O(delta) part).
	if prefixLen > m.reasoningWrapLen {
		seg := text[m.reasoningWrapLen : prefixLen-1] // drop the trailing '\n'
		for _, ln := range strings.Split(seg, "\n") {
			if m.reasoningWrapCache != "" {
				m.reasoningWrapCache += "\n"
			}
			m.reasoningWrapCache += style.Render(ln)
		}
		m.reasoningWrapLen = prefixLen
	}

	// Wrap only the trailing partial line each frame and append it to the cache.
	partial := text[prefixLen:]
	if partial == "" {
		return m.reasoningWrapCache
	}
	rendered := style.Render(partial)
	if m.reasoningWrapCache == "" {
		return rendered
	}
	return m.reasoningWrapCache + "\n" + rendered
}

// resetReasoningWrapCache drops the live-reasoning wrap cache (§4 E6) so the next
// think starts clean. Called wherever reasoningBuf is reset (finalizeStreaming
// D4, reasoning start/complete) — without it a new think could concatenate onto
// the previous think's cached lines.
func (m *TranscriptModel) resetReasoningWrapCache() {
	m.reasoningWrapCache = ""
	m.reasoningWrapLen = 0
}

// renderTodos formats a todo.updated checklist as one styled line per item — a
// calm checkbox progression (§2c) rendered by the pinned blockTodos card (§2b).
// There is no header line: completed items strike through in dim green, the
// in-progress item is bright (its present-tense ActiveForm preferred when set),
// and pending items are dim. An empty list collapses to a single dim
// "todos cleared" line. renderBlock place-indents the whole block by msgIndent,
// so lines here carry no leading indent of their own.
func renderTodos(todos []session.TodoItem) string {
	if len(todos) == 0 {
		return lipgloss.NewStyle().Foreground(theme.TextDim).Render("todos cleared")
	}
	doneStyle := lipgloss.NewStyle().Foreground(theme.Success).Strikethrough(true).Faint(true)
	activeStyle := lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true)
	pendingStyle := lipgloss.NewStyle().Foreground(theme.TextDim)
	lines := make([]string, 0, len(todos))
	for _, t := range todos {
		switch t.Status {
		case "completed":
			lines = append(lines, doneStyle.Render("✓ "+t.Content))
		case "in_progress":
			text := t.Content
			if t.ActiveForm != "" {
				text = t.ActiveForm
			}
			lines = append(lines, activeStyle.Render("▸ "+text))
		default: // pending and any unknown status
			lines = append(lines, pendingStyle.Render("○ "+t.Content))
		}
	}
	return strings.Join(lines, "\n")
}

// appendElbowNotice appends a Coral elbow line ("⎿  <text>") that reads as
// attached under the block above it — the §2c calm-chrome replacement for the
// old bracketed system notices ("[interrupted]", "[interrupt failed: …]") that
// scanned like debug logs. It is pre-styled and appended as a verbatim blockShell
// because blockInfo would re-style it dim (styleTInfo), overriding the Coral tone
// that marks it as an interruption. renderBlock place-indents it by msgIndent, so
// the "⎿" aligns with tool-card elbows.
func (m *TranscriptModel) appendElbowNotice(text string) {
	styled := lipgloss.NewStyle().Foreground(theme.Coral).Render(toolElbow + "  " + text)
	m.appendBlock(blockShell, styled)
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

// elapsedClockMin is the minimum running duration before a live elapsed clock is
// shown on a tool/subagent card. The threshold keeps fast tools (and the
// fixed-clock goldens, which see 0s) on the bare word rather than "(0s)".
const elapsedClockMin = 2 * time.Second

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
// A completed Bash card prefixes the elbow with its exit code ("exit N · …");
// a running card shows a live elapsed clock once it has run ~2s
// ("running… (12s)"). The head bullet is colored by status; the elbow shows the
// result summary (plus
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
	argShown, argTruncated := headArg(c.arg, avail-lipgloss.Width(name)-2)
	if argShown != "" {
		head += lipgloss.NewStyle().Foreground(theme.TextMuted).Render("(" + argShown + ")")
	}
	headLine := bullet + " " + head
	if lipgloss.Width(headLine) > width {
		headLine = truncate(headLine, width)
	}

	// Line 2 — elbow: "  ⎿  <result> (ctrl+o hint)".
	elbowText := c.summary
	switch {
	case c.status == toolRunning && elbowText == "":
		// Live elapsed on a running card, but only once it has been running
		// elapsedClockMin. The clock ticks off each tool.progress heartbeat
		// (applyToolProgress re-anchors startedAt) and every workTick
		// (bumpRunningCards re-renders the card).
		elbowText = "running…"
		if !c.startedAt.IsZero() {
			if d := nowFunc().Sub(c.startedAt); d >= elapsedClockMin {
				elbowText = "running… (" + fmtElapsed(d) + ")"
			}
		}
	case c.exitCode != nil:
		// Bash exit code on a completed/failed card: "exit N · <summary>" (or just
		// "exit N" when there is no summary). Status coloring below is unchanged.
		ec := fmt.Sprintf("exit %d", *c.exitCode)
		if elbowText != "" {
			elbowText = ec + " · " + elbowText
		} else {
			elbowText = ec
		}
	case elbowText == "":
		switch c.status {
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

// headArg budgets a tool card's argument for the "Name(arg)" head line: the
// collapsed arg ellipsized to budget columns, or "" with truncated=true when
// budget < 3 leaves no room to show it at all. truncated is the signal that
// expansion has more of the argument to reveal — shared by renderToolCard and
// toolCardExpandable so the ctrl+o hint, the toggle gate (H7), and the
// expanded arg-reveal can never disagree.
func headArg(arg string, budget int) (shown string, truncated bool) {
	if arg == "" {
		return "", false
	}
	if budget < 3 {
		return "", true
	}
	full := collapseSpaces(arg)
	shown = truncate(full, budget)
	return shown, shown != full
}

// toolCardExpandable reports whether ctrl+o would reveal anything for c at the
// transcript's current render width — the same width math and toolExpandBody
// call the renderer makes (renderItem's blockToolCard case), so a card whose
// elbow shows no expand hint is also not toggleable (H7).
func (m *TranscriptModel) toolCardExpandable(c *toolCard) bool {
	w := m.cardWidth()
	nameStr := c.tool
	if nameStr == "" {
		nameStr = "tool"
	}
	name := truncate(nameStr, max(1, w-2))
	_, argTruncated := headArg(c.arg, w-2-lipgloss.Width(name)-2)
	return len(m.toolExpandBody(c, w-elbowChromeW, argTruncated)) > 0
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
// Output is sanitized for in-frame display (H4) and tabs are expanded (H5) so
// the truncation backstop downstream sees the real display width.
func clampOutputLines(out string) []string {
	out = strings.TrimRight(sanitizeToolOutput(out), "\n")
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	if len(lines) > toolExpandHeadLines+toolExpandTailLines+1 {
		hidden := len(lines) - toolExpandHeadLines - toolExpandTailLines
		res := make([]string, 0, toolExpandHeadLines+toolExpandTailLines+1)
		res = append(res, lines[:toolExpandHeadLines]...)
		res = append(res, "… "+formatInt(hidden)+" lines hidden …")
		res = append(res, lines[len(lines)-toolExpandTailLines:]...)
		lines = res
	}
	for i, l := range lines {
		lines[i] = expandTabs(l)
	}
	return lines
}

// sanitizeToolOutput normalizes captured terminal output for in-frame display
// (H4): CRLF becomes LF, a lone CR keeps only the text after the last one on
// its line (the final state a terminal shows for progress-bar rewrites), and
// every escape sequence except SGR color runs is dropped — cursor movement and
// erase-line controls would otherwise execute inside the composited frame and
// smear the transcript. SGR survives for kit.RemapANSI to map onto the palette.
func sanitizeToolOutput(s string) string {
	if !strings.ContainsAny(s, "\r\x1b\b") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = stripNonSGR(l)
	}
	return strings.Join(lines, "\n")
}

// stripNonSGR removes every ESC-introduced sequence except SGR (ESC[…m) from a
// single line, plus any stray C0 control bytes other than tab (tabs are
// expanded later by expandTabs).
func stripNonSGR(s string) string {
	if !strings.ContainsAny(s, "\x1b\a\b\v\f") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\x1b' {
			j := ansiSeqEnd(s, i)
			if j-i >= 3 && s[i+1] == '[' && s[j-1] == 'm' {
				b.WriteString(s[i:j]) // SGR survives
			}
			i = j
			continue
		}
		if c < 0x20 && c != '\t' {
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// ansiSeqEnd returns the index just past the escape sequence starting at
// s[i] == ESC: CSI runs to their final byte (0x40–0x7e), string-introducer
// sequences (OSC/DCS/APC/PM/SOS) to BEL or ST, anything else as a 2-byte pair.
func ansiSeqEnd(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return j
	}
	switch s[j] {
	case '[':
		j++
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			j++
		}
		return j
	case ']', 'P', '_', '^', 'X':
		j++
		for j < len(s) {
			if s[j] == '\a' {
				return j + 1
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	default:
		return j + 1
	}
}

// expandTabs replaces tabs with spaces to the next 8-column stop (H5).
// lipgloss.Width measures "\t" as 0 but terminals expand it, so a tab that
// survives into the frame renders up to 8 columns wider than every width
// budget downstream believes. Columns are counted ANSI-aware but per-rune
// (a wide rune drifts a stop by one column — cosmetic).
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	const tabStop = 8
	var b strings.Builder
	b.Grow(len(s) + (tabStop-1)*strings.Count(s, "\t"))
	col := 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '\x1b': // zero-width escape: copy through
			j := ansiSeqEnd(s, i)
			b.WriteString(s[i:j])
			i = j
		case '\t':
			n := tabStop - col%tabStop
			for k := 0; k < n; k++ {
				b.WriteByte(' ')
			}
			col += n
			i++
		default:
			_, size := utf8.DecodeRuneInString(s[i:])
			b.WriteString(s[i : i+size])
			col++
			i += size
		}
	}
	return b.String()
}

// styleDiffLine colors a unified-diff line by its prefix ("+" add, "−" del, "…"
// elision, " " context). Shared by the permission box and the expanded tool card.
// Tabs are expanded first (H5) so callers' truncation sees the real width —
// tab-indented diff hunks (every Go file) otherwise overflow the budget.
func styleDiffLine(l string) string {
	l = expandTabs(l)
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

// renderHeader renders the exceptional alert band shown above the transcript ONLY
// while reconnecting or after giving up — headerBands includes it solely in those
// states. §2c removed the persistent title header (the statusline owns identity;
// the terminal tab owns the title — see App.windowTitle), so the non-exceptional
// title+status branch is gone: this now always pairs the session title with the
// live connect/gone alert, so a stalled resume reads as real progress even when
// the tab title isn't visible.
func (m *TranscriptModel) renderHeader() string {
	left := styleDetailTitle.Render(m.title)

	var right string
	if m.reconnectGaveUp {
		right = styleTError.Render("session gone")
	} else {
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
	}

	// spread truncates a long title rather than letting it overflow and clip the
	// reconnect state off the right edge (§1c spot 1).
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
	// editing is on), and the key hints right-aligned. The live working/loading
	// indicator no longer lives here — it is a dedicated left-aligned line ABOVE
	// the composer (workingLine / regionWorking), where it can spell out "esc to
	// interrupt" instead of the box's static "esc detach".
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

// workingLine renders the §2c live working indicator: a single left-aligned line
// ABOVE the composer while a turn runs (or history replays), e.g.
//
//	✳ Thinking… (12s · ↓820 tokens · esc to interrupt)
//
// The verb is context-aware (reasoning→Thinking, a running tool→Running <Tool>,
// streaming text→Writing, else Working). The trailing hint says "esc to steer"
// when a prompt is queued (esc sends it now) and "esc to interrupt" otherwise —
// esc genuinely does both, which the old right-aligned "working…" cell never
// spelled out. Empty when idle and not replaying so the band is absent.
func (m *TranscriptModel) workingLine() string {
	if m.replaying {
		return m.loadingStatus()
	}
	if !m.turnActive {
		return ""
	}
	// Animated "…" at a slower sub-rate than a spinner (§C.3), static under
	// reduce-motion (§E) so goldens are byte-stable.
	ell := anim.Ellipsis(m.workFrame / spinnerSubRate)
	if anim.ReduceMotion() {
		ell = "…"
	}
	glyph := lipgloss.NewStyle().Foreground(theme.Busy).Render(theme.MarkClaude)
	verb := styleSLBusy.Render(m.workingVerb() + ell)

	parts := []string{fmtElapsed(nowFunc().Sub(m.turnStart))}
	if m.OutputTokens > 0 {
		parts = append(parts, "↓"+kit.FormatTokens(m.OutputTokens)+" tokens")
	}
	if m.queuedPrompt != "" {
		parts = append(parts, "esc to steer")
	} else {
		parts = append(parts, "esc to interrupt")
	}
	meta := styleSLMuted.Render(" (" + strings.Join(parts, " · ") + ")")
	return glyph + " " + verb + meta
}

// workingVerb picks the present-tense verb for the working indicator from the
// live turn phase: thinking (reasoning), running a specific tool, writing the
// reply (streaming text), or a generic "Working" between phases.
func (m *TranscriptModel) workingVerb() string {
	switch {
	case m.reasoning:
		return "Thinking"
	case m.runningToolName() != "":
		return "Running " + m.runningToolName()
	case m.streaming:
		return "Writing"
	default:
		return "Working"
	}
}

// runningToolName returns the display name of the most recently started tool
// still awaiting its result, or "" when none is running. It reads the pendingTools
// FIFO (block indices of unresolved tool cards) so it stays O(pending), not O(all
// blocks).
func (m *TranscriptModel) runningToolName() string {
	for i := len(m.pendingTools) - 1; i >= 0; i-- {
		idx := m.pendingTools[i]
		if idx < 0 || idx >= len(m.blocks) {
			continue
		}
		if b := m.blocks[idx]; b.tool != nil && b.tool.status == toolRunning {
			if b.tool.tool != "" {
				return b.tool.tool
			}
			return "tool"
		}
	}
	return ""
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
