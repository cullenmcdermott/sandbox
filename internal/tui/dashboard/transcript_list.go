package dashboard

// transcript_list.go — the list-backed transcript body. The transcript is no
// longer one monolithic string rebuilt on every event (the old rebuild()).
// Instead each tblock is fronted by a blockItem implementing list.Item, and a
// *list.List virtualizes them: Render() is O(viewport) and an item only
// re-renders when its version bumps. A streaming assistant turn is a single
// ephemeral trailing item that grows via deltas, so a streamed chunk re-renders
// just that one item rather than re-running glamour over all history.

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
// fingerprint used as a render-cache key.
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

// blockItem fronts one transcript block (or the ephemeral streaming tail) as a
// list.Item. idx indexes into m.blocks; for the streaming tail idx is -1 and the
// body is rendered from m.assistantBuf. fp is the last-computed fingerprint of
// the render-affecting state, used to decide when to Bump the version.
type blockItem struct {
	*list.Versioned
	m         *TranscriptModel
	idx       int
	streaming bool
	// streamReasoning marks the ephemeral tail as the live THINKING tail (fed from
	// m.reasoningBuf) rather than the assistant-message tail (m.assistantBuf). Only
	// one is ever live at a time — a message's thinking block completes before its
	// text block starts — so the single streamItem serves both (§2b gap 3).
	streamReasoning bool
	unread          bool
	fp              uint64
	// fresh marks an item whose fingerprint has never been computed (just
	// created); dirty marks an immutable-kind block whose content was mutated in
	// place (the only such case is the RV9 dropped-partial text replacement).
	// Both force a fingerprint recompute on the next reconcile; otherwise an
	// immutable text block is never re-hashed (the live-append O(M·textlen) cost).
	fresh bool
	dirty bool
}

func (m *TranscriptModel) newBlockItem(idx int) *blockItem {
	return &blockItem{Versioned: list.NewVersioned(), m: m, idx: idx, fresh: true}
}

// blockKindMutable reports whether a block's render-affecting state can change
// after creation. Tool and subagent cards mutate in place (status/summary/arg,
// child cards, the running-spinner work-frame), so their fingerprint must be
// recomputed every reconcile — but they carry only short fields, so it is cheap.
// Every other kind is an immutable text block: hashed once, then reused.
func blockKindMutable(k tblockKind) bool {
	return k == blockToolCard || k == blockSubagent
}

// markBlockDirty forces item[idx]'s fingerprint to be recomputed on the next
// reconcile. Used for the rare in-place text mutation of an immutable block.
func (m *TranscriptModel) markBlockDirty(idx int) {
	if idx >= 0 && idx < len(m.items) {
		m.items[idx].dirty = true
	}
}

func (it *blockItem) Render(width int) string {
	var body string
	if it.streaming && it.streamReasoning {
		// Live THINKING tail (§2b gap 3): stream the reasoning text as it arrives,
		// muted+italic under a "∴ Thinking" header, instead of buffering silently
		// until reasoning.completed. It collapses to the compact "∴ Thought (N
		// lines): …" summary (renderBlock's blockReasoning) when the block commits.
		if it.unread {
			return it.m.renderUnreadDivider() + "\n" + it.m.renderLiveReasoning(it.m.reasoningBuf.String())
		}
		return it.m.renderLiveReasoning(it.m.reasoningBuf.String())
	}
	if it.streaming {
		// A2: use persistent AssistantItem + StreamingMarkdown for incremental
		// rendering of the live tail instead of creating a new item per delta.
		// The live tail wears the same Charple role gutter as the finalized
		// assistant block (renderBlock) so it doesn't shift left when the turn
		// completes. It MUST wrap at the same width as the finalized block
		// (assistantWrapWidth) — keyed off m.width, not the list-provided width —
		// or the block reflows at message.completed and the view lurches (T1).
		w := it.m.assistantWrapWidth()
		if it.m.streamAI != nil {
			it.m.streamAI.SetMessage(&chat.AssistantMessage{Content: it.m.assistantBuf.String(), Streaming: true})
			body = it.m.streamAI.RawRender(w)
		} else {
			body = it.m.renderBlockRaw(tblock{kind: blockAssistant, text: it.m.assistantBuf.String()})
		}
		// Match the finalized block's trailing-newline handling. renderBlockRaw
		// strips ALL trailing newlines (TrimRight), but the streaming renderer only
		// trims one (TrimSuffix), so glamour's trailing blank line survives as an
		// empty gutter row that disappears at message.completed — shifting the view
		// up a line (T1 drift). Trim it here so the tail and the finalized block are
		// the same height.
		body = strings.TrimRight(body, "\n")
		if body != "" {
			body = gutterPrefix(body, theme.Charple)
		}
	} else {
		body = it.m.renderBlock(it.m.blocks[it.idx])
		// A2.3 (Calm) turn gap: a blank line before each user turn after the
		// first, so turns read as distinct without a heavy divider.
		if it.m.blocks[it.idx].kind == blockUser && it.m.hasEarlierUser(it.idx) {
			body = "\n" + body
		}
	}
	if it.unread {
		return it.m.renderUnreadDivider() + "\n" + body
	}
	return body
}

// hasEarlierUser reports whether any block before idx is a user block — i.e.
// the block at idx begins a turn other than the first. Drives the turn-gap.
func (m *TranscriptModel) hasEarlierUser(idx int) bool {
	for i := 0; i < idx && i < len(m.blocks); i++ {
		if m.blocks[i].kind == blockUser {
			return true
		}
	}
	return false
}

// Finished reports whether the block's output is terminal (advisory for the
// list). A running tool/subagent or the live streaming tail is not finished.
func (it *blockItem) Finished() bool {
	if it.streaming {
		return false
	}
	b := it.m.blocks[it.idx]
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

// syncBody reconciles the list items with m.blocks and the streaming buffer,
// preserving the bottom pin when the view was already at the bottom. It replaces
// the old rebuild(): no monolithic string, no SetContent — only changed items
// re-render.
func (m *TranscriptModel) syncBody() {
	// During a bulk cache replay the caller appends every block first and
	// reconciles once at the end, so skip the per-event reconcile here — it would
	// otherwise re-fingerprint and rebuild the whole list on every replayed event
	// (the O(N^2) cold-load path).
	if m.bulkReplay {
		return
	}
	wasBottom := m.body.AtBottom()
	m.reconcileItems()
	if wasBottom {
		m.body.GotoBottom()
	}
}

// reconcileItems makes m.items mirror m.blocks (growing or, after /clear,
// shrinking), bumps the version of any item whose render-affecting state
// changed, appends the ephemeral streaming tail when a turn is streaming, and
// hands the resulting item set to the list.
func (m *TranscriptModel) reconcileItems() {
	m.reconciles++
	if len(m.items) > len(m.blocks) {
		m.items = m.items[:len(m.blocks)]
	}
	for i := len(m.items); i < len(m.blocks); i++ {
		m.items = append(m.items, m.newBlockItem(i))
	}

	for i, it := range m.items {
		unread := i == m.unreadIndex && m.unreadIndex > 0
		// Recompute the fingerprint only when it could have changed. Immutable
		// text blocks (the bulk of a long transcript, and the expensive ones —
		// full assistant message text) are hashed once and reused, so a live turn
		// appending block M+1 no longer re-hashes blocks 1..M every event.
		needFP := it.fresh || it.dirty || unread != it.unread || blockKindMutable(m.blocks[i].kind)
		it.unread = unread
		if !needFP {
			continue
		}
		it.fresh = false
		it.dirty = false
		fp := m.blockFP(m.blocks[i], unread)
		if fp != it.fp {
			it.fp = fp
			it.Bump()
		}
	}

	items := make([]list.Item, 0, len(m.items)+1)
	for _, it := range m.items {
		items = append(items, it)
	}
	// Append the ephemeral live tail: the streaming assistant message, or — when a
	// thinking block is in flight before any text — the live reasoning (§2b gap 3).
	// Assistant wins if both flags are somehow set (a text block supersedes an
	// unfinished think). Exactly one tail, reusing the single streamItem.
	switch {
	case m.streaming && m.assistantBuf.Len() > 0:
		if m.streamItem == nil {
			m.streamItem = &blockItem{Versioned: list.NewVersioned(), m: m, idx: -1, streaming: true}
		}
		m.streamItem.streamReasoning = false
		m.bumpStreamItem()
		items = append(items, m.streamItem)
	case m.reasoning:
		if m.streamItem == nil {
			m.streamItem = &blockItem{Versioned: list.NewVersioned(), m: m, idx: -1, streaming: true}
		}
		m.streamItem.streamReasoning = true
		m.bumpStreamItem()
		items = append(items, m.streamItem)
	default:
		m.streamItem = nil
	}
	m.body.SetItems(items...)
}

// bumpStreamItem refingerprints the streaming tail and bumps it if its text
// changed. Cheap: it hashes only the live buffer, never history.
func (m *TranscriptModel) bumpStreamItem() {
	if m.streamItem == nil {
		return
	}
	// Fingerprint the live buffer for the tail's current mode. The mode is folded
	// in so a thinking→text handoff (reasoning tail replaced by the assistant tail)
	// can't collide fingerprints even at identical text.
	var fp uint64
	if m.streamItem.streamReasoning {
		fp = fnvStr("think", m.reasoningBuf.String())
	} else {
		fp = fnvStr("stream", m.assistantBuf.String())
	}
	if fp != m.streamItem.fp {
		m.streamItem.fp = fp
		m.streamItem.Bump()
	}
}

// streamDelta is the hot path for a streamed chunk: it bumps only the streaming
// tail (or, on the first chunk, reconciles to create it), avoiding any walk over
// prior blocks. This is what makes a streamed turn O(deltas), not O(deltas×M).
func (m *TranscriptModel) streamDelta() {
	wasBottom := m.body.AtBottom()
	if m.streamItem == nil {
		m.reconcileItems()
	} else {
		m.bumpStreamItem()
	}
	if wasBottom {
		m.body.GotoBottom()
	}
}

// blockFP fingerprints every render-affecting field of a block so reconcile can
// bump exactly the items that changed. The work-frame is folded in only for a
// running subagent (whose header/child spinner animates), matching the old
// rebuild's "re-render only when a subagent is in flight" rule; flat running
// tool cards use a static marker and so omit it.
func (m *TranscriptModel) blockFP(b tblock, unread bool) uint64 {
	m.fpComputes++
	var sb strings.Builder
	// Fold the theme epoch in so a /theme swap invalidates every block's cached
	// render (the palette baked into the ANSI would otherwise persist).
	fmt.Fprintf(&sb, "e%d\x00", theme.Epoch())
	fmt.Fprintf(&sb, "k%d\x00", b.kind)
	sb.WriteString(b.text)
	sb.WriteByte(0)
	if b.tool != nil {
		fmt.Fprintf(&sb, "t\x00%s\x00%s\x00%d\x00%s\x00", b.tool.tool, b.tool.arg, b.tool.status, b.tool.summary)
	}
	if b.sub != nil {
		s := b.sub
		running := s.status == toolRunning
		fmt.Fprintf(&sb, "s\x00%s\x00%s\x00%d\x00%t\x00", s.agentName, s.prompt, s.status, s.collapsed)
		for _, c := range s.children {
			fmt.Fprintf(&sb, "c\x00%s\x00%s\x00%d\x00%s\x00", c.tool, c.arg, c.status, c.summary)
			if c.status == toolRunning {
				running = true
			}
		}
		if running {
			fmt.Fprintf(&sb, "wf%d\x00", m.workFrame)
		}
	}
	if unread {
		sb.WriteString("U")
	}
	return fnvStr(sb.String())
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
