package chat

// frame.go — the transcript's shared visual grammar: the ⏺ head bullet, the ⎿
// result elbow, the hanging-indent message framing, and the focus gutter. These
// are the same glyph/indent conventions the production dashboard transcript uses
// (the "§2c message grammar"), lifted into the public package so an external host
// composes a transcript with identical chrome.

import (
	"strings"
	"time"
)

// Tool-card glyph vocabulary: the ⏺ status head bullet and the ⎿ result elbow.
// The bullet's color carries the running/ok/error signal; the elbow anchors the
// result column so a scan down the transcript reads results in one place.
const (
	toolHeadBullet = "⏺"
	toolElbow      = "⎿"
	// elbowChromeW is the display width of the "  ⎿  " elbow prefix (2-space
	// indent aligning the elbow under the tool name, the elbow glyph, two spaces).
	elbowChromeW = 5
	// msgIndent is the hanging-indent width a framed message occupies: a 2-cell
	// head prefix ("⏺ " / "> ") on the first line, a 2-space indent on the rest.
	msgIndent = 2
	// focusGutter is the width of the focus indicator ("▌ ") prepended to a
	// focused item's every line. A focused item renders its body focusGutter
	// columns narrower so the total stays within the caller's width.
	focusGutter = 2
)

// elapsedClockMin is the minimum running duration before a live elapsed clock is
// shown on a tool/subagent card, so fast tools stay on the bare word.
const elapsedClockMin = 2 * time.Second

// ExpandHint and CollapseHint are the affordance labels a collapsible tool card
// shows on its elbow ("… (ctrl+o to expand)"). They default to the ctrl+o keymap
// the reference host uses; a host that binds expansion to a different key rebinds
// these (they are read at render time). Package-global — set them once at
// startup, not concurrently with rendering.
var (
	ExpandHint   = "ctrl+o to expand"
	CollapseHint = "ctrl+o to collapse"
)

// Bullet heads a block with a single neutral "⏺ " bullet (the assistant action
// grammar) and hanging-indents continuation lines under it. Leading blank lines
// are dropped first so the bullet is never orphaned above the text. Exported so a
// host can chrome an AssistantItem body (whose own Render emits no bullet, by the
// item's locked contract) to match the production transcript.
func Bullet(s string) string {
	head := styAssistantBullet.Render(toolHeadBullet) + " "
	return hangingPrefix(trimLeadingBlankLines(s), head)
}

// Quote heads a block with a dim "> " quote and hanging-indents continuation
// lines under the text. Exported for hosts composing user prompts; UserItem
// applies it internally.
func Quote(s string) string {
	head := styUserQuote.Render("> ")
	return hangingPrefix(s, head)
}

// applyFocusBar prepends the focus gutter bar ("▌ ") to every line of body. The
// caller renders body at width-focusGutter first, so the result stays within the
// original width. Blank continuation lines still get the bar so the gutter reads
// as one column down the block.
func applyFocusBar(body string) string {
	bar := styFocusBar.Render("▌") + " "
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = bar + lines[i]
	}
	return strings.Join(lines, "\n")
}

// focusWidth is the body render width for an item given the caller's width and
// whether the item is focused (a focused item reserves the gutter columns).
func focusWidth(width int, focused bool) int {
	if focused {
		if w := width - focusGutter; w > 0 {
			return w
		}
		return 1
	}
	return width
}

// clampFocus applies the focus bar to an already-focus-width-rendered body when
// focused; otherwise returns it unchanged. Pairs with focusWidth.
func clampFocus(body string, focused bool) string {
	if focused {
		return applyFocusBar(body)
	}
	return body
}
