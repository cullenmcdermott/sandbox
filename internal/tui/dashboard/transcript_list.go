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
	unread    bool
	fp        uint64
}

func (m *TranscriptModel) newBlockItem(idx int) *blockItem {
	return &blockItem{Versioned: list.NewVersioned(), m: m, idx: idx}
}

func (it *blockItem) Render(width int) string {
	var body string
	if it.streaming {
		// A2: use persistent AssistantItem + StreamingMarkdown for incremental
		// rendering of the live tail instead of creating a new item per delta.
		if it.m.streamAI != nil {
			it.m.streamAI.SetMessage(&chat.AssistantMessage{Content: it.m.assistantBuf.String(), Streaming: true})
			body = it.m.streamAI.RawRender(width)
		} else {
			body = it.m.renderBlock(tblock{kind: blockAssistant, text: it.m.assistantBuf.String()})
		}
	} else {
		body = it.m.renderBlock(it.m.blocks[it.idx])
	}
	if it.unread {
		return it.m.renderUnreadDivider() + "\n" + body
	}
	return body
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
	if len(m.items) > len(m.blocks) {
		m.items = m.items[:len(m.blocks)]
	}
	for i := len(m.items); i < len(m.blocks); i++ {
		m.items = append(m.items, m.newBlockItem(i))
	}

	for i, it := range m.items {
		unread := i == m.unreadIndex && m.unreadIndex > 0
		it.unread = unread
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
	if m.streaming && m.assistantBuf.Len() > 0 {
		if m.streamItem == nil {
			m.streamItem = &blockItem{Versioned: list.NewVersioned(), m: m, idx: -1, streaming: true}
		}
		m.bumpStreamItem()
		items = append(items, m.streamItem)
	} else {
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
	fp := fnvStr("stream", m.assistantBuf.String())
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
	var sb strings.Builder
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

// bodyView renders the list and pads it to the body height so the surrounding
// chrome (input + status line) stays anchored, matching the old viewport which
// always filled its height.
func (m *TranscriptModel) bodyView() string {
	out := m.body.Render()
	h := m.body.Height()
	// A stateless scrollbar on the right edge when the content overflows the
	// viewport (§D). The body width was reserved one column short in layout, so
	// the bar (or a blank filler column) sits flush without shifting content.
	bar := kit.Scrollbar(h, m.body.TotalHeight(), h, m.body.Offset())
	body := lipgloss.NewStyle().Width(max(1, m.width-1)).Height(h).Render(out)
	if bar == "" {
		return body
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, body, bar)
}
