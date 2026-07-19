package chat

// permission.go — a pending permission request rendered as a compact gold
// approval card: a "◆ Tool(arg)" head, an optional edit diff, and the approve/
// deny key hints. A plan-approval variant heads with "◆ Plan" and shows the plan
// text. This is the transcript-visible form of the dashboard's permission prompt;
// the interactive decision flow (which key resolves it, focus routing, the
// numbered options panel) is a host concern — the item renders the request so a
// host can drive it. Keeping the decision out of the item is deliberate: a host
// owns transport and must guarantee an asynchronously-arriving request cannot
// steal keystrokes the user was already typing.

import (
	"strings"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// Permission is the protocol-neutral data behind a PermissionItem.
type Permission struct {
	// ID is the request id a host uses to resolve the decision.
	ID string
	// Tool is the tool awaiting approval ("Bash", "Edit", …).
	Tool string
	// Arg is the headline argument (command / path / url) so approval isn't blind.
	Arg string
	// Diff is an optional edit diff ("+"/"−" prefixed) shown under the head.
	Diff []string
	// IsPlan marks an ExitPlanMode plan-approval card.
	IsPlan bool
	// Plan is the plan text shown for a plan-approval card.
	Plan string
}

// PermissionItem renders a Permission as a list.Item.
type PermissionItem struct {
	*list.Versioned

	perm    *Permission
	focused bool

	cache section
}

// NewPermissionItem builds a permission card.
func NewPermissionItem(perm *Permission) *PermissionItem {
	return &PermissionItem{Versioned: list.NewVersioned(), perm: perm}
}

// Permission returns the underlying data.
func (p *PermissionItem) Permission() *Permission { return p.perm }

// SetFocused marks the card focused (a left gutter bar).
func (p *PermissionItem) SetFocused(b bool) {
	if p.focused == b {
		return
	}
	p.focused = b
	p.cache.valid = false
	p.Bump()
}

// Focused reports the focus state.
func (p *PermissionItem) Focused() bool { return p.focused }

// Render draws the gold approval card within width columns.
func (p *PermissionItem) Render(width int) string {
	if p.perm == nil {
		return ""
	}
	if width < 1 {
		width = 1
	}
	fields := [][]byte{[]byte(p.perm.ID), []byte(p.perm.Tool), []byte(p.perm.Arg), []byte(p.perm.Plan), u64b(flagBits(p.perm.IsPlan))}
	for _, d := range p.perm.Diff {
		fields = append(fields, []byte(d))
	}
	srcHash := fnvFields(fields...)
	extra := extraKey(theme.Epoch(), flagBits(p.focused))
	if p.cache.hit(width, srcHash, extra) {
		return p.cache.out
	}
	out := fitWidth(clampFocus(p.render(focusWidth(width, p.focused)), p.focused), width)
	p.cache.store(width, srcHash, extra, out)
	return out
}

func (p *PermissionItem) render(width int) string {
	perm := p.perm
	var lines []string
	if perm.IsPlan {
		lines = append(lines, truncate(styPermLabel.Render(theme.GlyphWaiting+" Plan"), width))
		if body := strings.TrimSpace(perm.Plan); body != "" {
			lines = append(lines, strings.Split(styPermArg.Render(wrapPlain(body, width)), "\n")...)
		}
		lines = append(lines, truncate("  "+kit.KbdRow([2]string{"a", "approve"}, [2]string{"e", "edit"}, [2]string{"esc", "keep planning"}), width))
		return strings.Join(lines, "\n")
	}

	tool := perm.Tool
	if tool == "" {
		tool = "tool"
	}
	head := styPermLabel.Render(theme.GlyphWaiting + " " + tool)
	if arg := collapseSpaces(perm.Arg); arg != "" {
		head += styPermArg.Render("(" + truncate(arg, max(4, width-len(tool)-4)) + ")")
	}
	lines = append(lines, truncate(head, width))
	for _, l := range condenseDiff(perm.Diff, toolExpandDiffMax) {
		lines = append(lines, truncate("  "+styleDiffLine(l), width))
	}
	lines = append(lines, truncate("  "+kit.KbdRow([2]string{"a", "approve"}, [2]string{"d", "deny"}), width))
	return strings.Join(lines, "\n")
}
