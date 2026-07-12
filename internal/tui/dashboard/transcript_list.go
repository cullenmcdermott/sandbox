package dashboard

// transcript_list.go — the list-backed transcript body. The transcript is a
// slice of *blockCard, each of which IS a list.Item: one unified representation
// that owns both the block's data AND its render + version + per-block display
// state. There is no parallel item slice and no fingerprint pass — a mutation
// bumps its card's version directly at the mutation site, and *list.List keys its
// render cache on (item, width, version) so only a changed card re-renders.
//
// A streaming assistant turn is a single ephemeral trailing card (m.streamItem)
// fed from m.assistantBuf, so a streamed chunk re-renders just that one card
// rather than re-running glamour over all history.

import (
	"fmt"
	"hash/fnv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard/chat"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// fnvStr hashes its parts (NUL-separated for unambiguous framing) into a 64-bit
// value used as the streaming tail's change key.
func fnvStr(parts ...string) uint64 {
	h := fnv.New64a()
	for i, p := range parts {
		if i > 0 {
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte(p))
	}
	return h.Sum64()
}

// blockCard is THE single representation of a transcript block: it holds the
// block's data (former tblock) AND is the list.Item that renders it (former
// blockItem), owning its own version and per-block display state. Mutating a
// card's data bumps its version directly — no external fingerprint pass, no
// parallel slice. The ephemeral streaming tail is a blockCard with streaming
// set, fed from the model's live buffers rather than its own text field.
type blockCard struct {
	*list.Versioned
	m *TranscriptModel

	kind tblockKind
	text string // raw (markdown for assistant blocks) so it re-wraps on resize
	tool *toolCard
	sub  *subagentCard // non-nil only for blockSubagent

	// Per-commit display state, recomputed by commitItems; a change bumps the
	// version so the "new since you left" divider / turn gap re-render.
	unread  bool
	turnGap bool // a blank line before a user block that begins a non-first turn

	// Streaming tail only (kind is unused then): streamReasoning selects the live
	// THINKING tail (m.reasoningBuf) over the assistant-message tail (m.assistantBuf).
	streaming       bool
	streamReasoning bool
	streamFP        uint64 // last-rendered buffer key; gates the tail's version bump
}

// newBlockCard builds a committed card owning a fresh version counter.
func (m *TranscriptModel) newBlockCard(kind tblockKind, text string) *blockCard {
	return &blockCard{Versioned: list.NewVersioned(), m: m, kind: kind, text: text}
}

// setDisplay updates the per-commit display flags, bumping the version only when
// they actually change so an immutable committed block is never needlessly
// re-rendered (the memoization the old fingerprint gate provided).
func (b *blockCard) setDisplay(unread, turnGap bool) {
	if b.unread == unread && b.turnGap == turnGap {
		return
	}
	b.unread = unread
	b.turnGap = turnGap
	b.Bump()
}

func (b *blockCard) Render(width int) string {
	if b.streaming {
		return b.renderStreamTail()
	}
	body := b.m.renderBlock(b)
	// A2.3 (Calm) turn gap: a blank line before each user turn after the first,
	// so turns read as distinct without a heavy divider.
	if b.kind == blockUser && b.turnGap {
		body = "\n" + body
	}
	if b.unread {
		return b.m.renderUnreadDivider() + "\n" + body
	}
	return body
}

// renderStreamTail renders the ephemeral live tail from the model's buffers.
func (b *blockCard) renderStreamTail() string {
	m := b.m
	if b.streamReasoning {
		// Live THINKING tail (§2b gap 3): stream the reasoning text as it arrives,
		// muted+italic under a "∴ Thinking" header, instead of buffering silently
		// until reasoning.completed. It collapses to the compact "∴ Thought (N
		// lines): …" summary (renderBlockBody's blockReasoning) when the block commits.
		if b.unread {
			return m.renderUnreadDivider() + "\n" + m.renderLiveReasoning(m.reasoningBuf.String())
		}
		return m.renderLiveReasoning(m.reasoningBuf.String())
	}
	// A2: use persistent AssistantItem + StreamingMarkdown for incremental rendering
	// of the live tail instead of creating a new item per delta. The live tail wears
	// the same ⏺ bullet + hanging indent as the finalized assistant block (renderBlock)
	// so it doesn't shift left when the turn completes. It MUST wrap at the same width as
	// the finalized block (assistantWrapWidth) — keyed off m.width, not the
	// list-provided width — or the block reflows at message.completed and the view
	// lurches (T1).
	w := m.assistantWrapWidth()
	var body string
	if m.streamAI != nil {
		m.streamAI.SetMessage(&chat.AssistantMessage{Content: m.assistantBuf.String(), Streaming: true})
		body = m.streamAI.RawRender(w)
	} else {
		body = m.renderBlockBody(&blockCard{m: m, kind: blockAssistant, text: m.assistantBuf.String()})
	}
	// Match the finalized block's trailing-newline handling. renderBlockBody strips
	// ALL trailing newlines (TrimRight), but the streaming renderer only trims one
	// (TrimSuffix), so glamour's trailing blank line survives as an empty gutter row
	// that disappears at message.completed — shifting the view up a line (T1 drift).
	// Trim it here so the tail and the finalized block are the same height.
	body = strings.TrimRight(body, "\n")
	if body != "" {
		body = bulletPrefix(body, theme.TextMuted)
	}
	if b.unread {
		return m.renderUnreadDivider() + "\n" + body
	}
	return body
}

// Finished reports whether the card's output is terminal (advisory for the
// list). A running tool/subagent or the live streaming tail is not finished.
func (b *blockCard) Finished() bool {
	if b.streaming {
		return false
	}
	switch b.kind {
	case blockToolCard:
		return b.tool == nil || b.tool.status != toolRunning
	case blockSubagent:
		if b.sub == nil {
			return true
		}
		if b.sub.status == toolRunning {
			return false
		}
		for _, c := range b.sub.children {
			if c.status == toolRunning {
				return false
			}
		}
		return true
	}
	return true
}

// syncItems rebuilds the list's item set from m.blocks (+ the ephemeral streaming
// tail) while preserving the bottom pin when the view was already at the bottom.
// It replaces the old syncBody + reconcileItems: there is no fingerprint pass and
// no parallel item slice — each card owns its version and bumps it at its mutation
// site, so SetItems only reshuffles pointers (the expensive render is lazy, per
// card, only on a version change). During a bulk cache replay it is a no-op; the
// caller commits once at the end.
func (m *TranscriptModel) syncItems() {
	if m.bulkReplay {
		return
	}
	wasBottom := m.body.AtBottom()
	m.commitItems()
	if wasBottom {
		m.body.GotoBottom()
	}
}

// commitItems recomputes each card's per-commit display flags (unread divider,
// turn gap) and hands the card set (+ the ephemeral streaming tail) to the list.
func (m *TranscriptModel) commitItems() {
	m.reconciles++
	// A /theme swap bumps the global epoch; a committed card otherwise never
	// re-renders (its version is stable), so its cached old-palette ANSI would
	// survive until an unrelated change. Force every card to re-render once on an
	// epoch change so the new palette takes immediately (§1c). The streaming tail
	// folds the epoch into its own key (ensureStreamTail).
	if e := theme.Epoch(); e != m.lastThemeEpoch {
		m.lastThemeEpoch = e
		for _, b := range m.blocks {
			b.Bump()
		}
	}

	sawUser := false
	for i, b := range m.blocks {
		unread := i == m.unreadIndex && m.unreadIndex > 0
		turnGap := b.kind == blockUser && sawUser
		if b.kind == blockUser {
			sawUser = true
		}
		b.setDisplay(unread, turnGap)
	}

	items := make([]list.Item, 0, len(m.blocks)+1)
	for _, b := range m.blocks {
		items = append(items, b)
	}
	// Append the ephemeral live tail: the streaming assistant message, or — when a
	// thinking block is in flight before any text — the live reasoning (§2b gap 3).
	// Assistant wins if both are somehow set (a text block supersedes an unfinished
	// think). Exactly one tail, reusing the single m.streamItem.
	switch {
	case m.streaming && m.assistantBuf.Len() > 0:
		m.ensureStreamTail(false)
		items = append(items, m.streamItem)
	case m.reasoning:
		m.ensureStreamTail(true)
		items = append(items, m.streamItem)
	default:
		m.streamItem = nil
	}
	m.body.SetItems(items...)
}

// ensureStreamTail creates (once) and refreshes the ephemeral streaming tail for
// the given mode, bumping its version only when the live buffer grew (or the theme
// epoch, or the mode changed). Cheap and O(1): the change key is the live buffer's
// LENGTH, not its content, so we never re-hash (or copy) the whole buffer per delta
// (§4 E7 — a 100 KB message used to hash+copy O(L) bytes on every one of its deltas).
//
// Keying on length is faithful because within a single streaming tail's life the
// active buffer is append-only: every message/reasoning boundary recreates the tail
// (commitItems nils m.streamItem whenever neither streaming nor reasoning is live),
// so a delta can only ever grow the buffer, never rewrite it at an equal length —
// a length change is therefore exactly a content change. The mode is folded in so a
// thinking→text handoff can't collide keys; the epoch is folded in so a /theme swap
// mid-stall re-renders the tail with the new palette (§1c).
func (m *TranscriptModel) ensureStreamTail(reasoning bool) {
	if m.streamItem == nil {
		m.streamItem = &blockCard{Versioned: list.NewVersioned(), m: m, streaming: true}
	}
	m.streamItem.streamReasoning = reasoning
	mode, n := "stream", m.assistantBuf.Len()
	if reasoning {
		mode, n = "think", m.reasoningBuf.Len()
	}
	fp := fnvStr(fmt.Sprintf("%s\x00e%d\x00n%d", mode, theme.Epoch(), n))
	if fp != m.streamItem.streamFP {
		m.streamItem.streamFP = fp
		m.streamItem.Bump()
	}
}

// streamDelta is the hot path for a streamed chunk: it refreshes only the
// streaming tail (or, on the first chunk, commits to create it), avoiding any walk
// over prior blocks. This is what makes a streamed turn O(deltas), not O(deltas×M).
func (m *TranscriptModel) streamDelta() {
	wasBottom := m.body.AtBottom()
	if m.streamItem == nil {
		m.commitItems() // first chunk: the tail enters the item set
	} else {
		// The tail is already in the list; refresh it in place (O(1)).
		m.ensureStreamTail(m.streamItem.streamReasoning)
	}
	if wasBottom {
		m.body.GotoBottom()
	}
}

// bumpRunningSubagents re-renders every in-flight subagent card (its header/child
// spinner animates on the work tick). Flat running tool cards use a static marker,
// so they are deliberately excluded — they must not force a re-render each tick.
// Returns whether any card was bumped.
func (m *TranscriptModel) bumpRunningSubagents() bool {
	bumped := false
	for _, b := range m.blocks {
		if b.kind != blockSubagent || b.sub == nil {
			continue
		}
		running := b.sub.status == toolRunning
		for _, c := range b.sub.children {
			if c.status == toolRunning {
				running = true
			}
		}
		if running {
			b.Bump()
			bumped = true
		}
	}
	return bumped
}

// transcriptEmpty reports whether there is nothing to show in the body yet — a
// freshly opened session with no committed blocks, no streaming turn, and no
// pending permission. Drives the first-hint welcome (renderTranscript).
func (m *TranscriptModel) transcriptEmpty() bool {
	return len(m.blocks) == 0 && !m.streaming && !m.reasoning && m.pending == nil
}

// emptyTranscriptView is the centered first-hint shown in a fresh session so a
// new Claude session isn't a blank void. Padded with plain whitespace to match
// bodyView's fitModal fill (no forced background). Sized to the body rect.
func (m *TranscriptModel) emptyTranscriptView(width, height int) string {
	title := theme.GradientText("new session", true, theme.Charple, theme.Dolly)
	tagline := lipgloss.NewStyle().Foreground(theme.TextBody).Render("ready when you are")
	hint := lipgloss.NewStyle().Foreground(theme.TextMuted).Render("type a message below to begin · ") +
		kit.Kbd("ctrl+]", "detach")
	body := strings.Join([]string{title, "", tagline, "", hint}, "\n")
	placed := lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, body)
	// Enforce the exact body rect the same way bodyView does, so a narrow terminal
	// (where a hint line would overflow) can't push the surrounding chrome around.
	return fitModal(placed, width, height)
}

// bodyView renders the list and pads it to the body height so the surrounding
// chrome (input + status line) stays anchored, matching the old viewport which
// always filled its height.
func (m *TranscriptModel) bodyView() string {
	out := m.body.Render()
	h := m.body.Height()
	// A stateless scrollbar on the right edge when the content overflows the
	// viewport (§D). The body width was reserved one column short in layout, so
	// the bar (or a blank filler column) sits flush without shifting content.
	total, offset := m.body.Metrics()
	bar := kit.Scrollbar(h, total, h, offset)
	// Normalize to an exact (m.width-1)×h rectangle so the scrollbar attaches
	// flush and short content fills the height. fitModal is the cheap ANSI-aware
	// pad/truncate; the equivalent lipgloss Style.Width().Height().Render() costs
	// ~830µs / 33k allocs per frame on a long transcript (the scroll-lag hot
	// spot), versus a few µs here, with byte-identical output for in-width lines.
	body := fitModal(out, max(1, m.width-1), h)
	if bar == "" {
		return body
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, body, bar)
}
