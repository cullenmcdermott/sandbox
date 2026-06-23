package chat

import (
	"hash/fnv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/cullenmcdermott/sandbox/tui/list"
)

// AssistantMessage is the plain data struct the TranscriptModel mutates.
type AssistantMessage struct {
	ID        string
	Content   string // markdown body; grows via deltas
	Thinking  string // optional reasoning text ("" when none)
	Errored   bool
	ErrText   string
	Streaming bool // a turn is actively producing this message
	Finished  bool // the turn reached a terminal state
}

func fnv64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// fnvFields hashes fields with 8-byte little-endian length framing so distinct
// field tuples can never collide via concatenation (e.g. {"a","bc"} != {"ab","c"}).
func fnvFields(fields ...[]byte) uint64 {
	h := fnv.New64a()
	var lb [8]byte
	for _, f := range fields {
		for i := 0; i < 8; i++ {
			lb[i] = byte(uint64(len(f)) >> (8 * i))
		}
		_, _ = h.Write(lb[:])
		_, _ = h.Write(f)
	}
	return h.Sum64()
}

type section struct {
	width   int
	srcHash uint64
	extra   uint64
	out     string
	valid   bool
}

func (s *section) hit(width int, srcHash, extra uint64) bool {
	return s.valid && s.width == width && s.srcHash == srcHash && s.extra == extra
}

func (s *section) store(width int, srcHash, extra uint64, out string) {
	*s = section{width: width, srcHash: srcHash, extra: extra, out: out, valid: true}
}

type AssistantItem struct {
	*list.Versioned

	msg     *AssistantMessage
	focused bool

	contentSec  section
	thinkingSec section
	errorSec    section

	streaming StreamingMarkdown

	renderContentMD                                     func(text string, width int) string
	renderThinkingMD                                    func(text string, width int) string
	getRenderer                                         func(width int) Renderer // for StreamingMarkdown; nil disables incremental
	styUser, styErr, styPrefixFocused, styPrefixBlurred lipgloss.Style
}

func NewAssistantItem(msg *AssistantMessage) *AssistantItem {
	return &AssistantItem{
		Versioned: list.NewVersioned(),
		msg:       msg,
		renderContentMD: func(text string, width int) string {
			return text
		},
		renderThinkingMD: func(text string, width int) string {
			return text
		},
		styUser:          lipgloss.NewStyle(),
		styErr:           lipgloss.NewStyle(),
		styPrefixFocused: lipgloss.NewStyle().PaddingLeft(2),
		styPrefixBlurred: lipgloss.NewStyle().PaddingLeft(1),
	}
}

func (a *AssistantItem) ID() string { return a.msg.ID }

func (a *AssistantItem) SetFocused(b bool) {
	if a.focused != b {
		a.focused = b
		a.Bump()
	}
}

func (a *AssistantItem) SetMessage(m *AssistantMessage) {
	a.msg = m
	a.Bump()
}

// SetRenderContentMD injects the content-section markdown renderer. Production
// wires this to the pooled glamour renderer (chat.MarkdownRenderer); tests
// inject a counting stub. Must be called before the first RawRender/Render.
func (a *AssistantItem) SetRenderContentMD(fn func(text string, width int) string) {
	a.renderContentMD = fn
}

// SetRenderThinkingMD injects the thinking-section markdown renderer.
func (a *AssistantItem) SetRenderThinkingMD(fn func(text string, width int) string) {
	a.renderThinkingMD = fn
}

// SetRendererFactory injects a width→Renderer lookup for StreamingMarkdown.
// When non-nil and msg.Streaming is true, cachedContent uses StreamingMarkdown
// instead of full re-renders.
func (a *AssistantItem) SetRendererFactory(fn func(width int) Renderer) {
	a.getRenderer = fn
}

func (a *AssistantItem) Finished() bool { return a.msg.Finished && !a.msg.Streaming }

func (a *AssistantItem) contentKey() (uint64, uint64)  { return fnv64(a.msg.Content), 0 }
func (a *AssistantItem) thinkingKey() (uint64, uint64) { return fnv64(a.msg.Thinking), 0 }
func (a *AssistantItem) errorKey() (uint64, uint64) {
	if !a.msg.Errored {
		return 0, 0
	}
	return fnvFields([]byte(a.msg.ErrText)), 0
}

func (a *AssistantItem) cachedContent(width int) string {
	// Live tail: use StreamingMarkdown stable-prefix caching (A2).
	if a.msg.Streaming && a.getRenderer != nil {
		if r := a.getRenderer(width); r != nil {
			return a.streaming.Render(a.msg.Content, width, r)
		}
	}
	sh, ex := a.contentKey()
	if a.contentSec.hit(width, sh, ex) {
		return a.contentSec.out
	}
	out := a.renderContentMD(a.msg.Content, width)
	a.contentSec.store(width, sh, ex, out)
	return out
}

func (a *AssistantItem) cachedThinking(width int) string {
	sh, ex := a.thinkingKey()
	if a.thinkingSec.hit(width, sh, ex) {
		return a.thinkingSec.out
	}
	out := a.renderThinkingMD(a.msg.Thinking, width)
	a.thinkingSec.store(width, sh, ex, out)
	return out
}

func (a *AssistantItem) cachedError(width int) string {
	sh, ex := a.errorKey()
	if a.errorSec.hit(width, sh, ex) {
		return a.errorSec.out
	}
	out := a.styErr.Render(a.msg.ErrText)
	a.errorSec.store(width, sh, ex, out)
	return out
}

func (a *AssistantItem) RawRender(width int) string {
	var parts []string
	if strings.TrimSpace(a.msg.Thinking) != "" {
		parts = append(parts, a.cachedThinking(width))
	}
	if strings.TrimSpace(a.msg.Content) != "" {
		parts = append(parts, a.cachedContent(width))
	}
	if a.msg.Errored {
		parts = append(parts, a.cachedError(width))
	}
	return strings.Join(parts, "\n")
}

func (a *AssistantItem) Render(width int) string {
	raw := a.RawRender(width)
	prefix := a.styPrefixBlurred.Render("")
	if a.focused {
		prefix = a.styPrefixFocused.Render("")
	}
	if prefix == "" {
		return raw
	}
	lines := strings.Split(raw, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
