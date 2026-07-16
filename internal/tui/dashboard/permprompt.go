package dashboard

// permprompt.go — the §2a permission-decision component: the single owner of
// BOTH permission surfaces, the numbered tool-question panel and the plan
// approval card, behind one Render/Height/HandleKey contract with one refresh
// discipline (the static body is cached; the appear-fade border assembles live
// in Render — see permPrompt below). CC-signature "Do you want to…?" question +
// numbered, ↑/↓-navigable options with a ❯ on the selection; a/d stay as hidden
// accelerators. The session option issues the runner's session-scope grant
// (§2b gap 2) — tool-NAME-level ("allow Bash for the rest of this session",
// runner/src/grants.ts), so the label names the tool, never the argument. The
// dashboard's cross-session perm queue (permqueue.go) stays a separate
// allow-once list; it reuses only the "wants:" summary vocabulary here.

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// permOption is one numbered row of the permission question panel.
type permOption struct {
	label string
	allow bool
	scope string // "once" | "session" (meaningful only when allow)
}

// permOptions builds the panel's option rows for a tool: Yes / Yes-for-session
// / No. Deny carries no scope — the runner's grant store records allows only.
func permOptions(tool string) []permOption {
	return []permOption{
		{label: "Yes", allow: true, scope: "once"},
		{label: fmt.Sprintf("Yes, allow %s for the rest of this session", tool), allow: true, scope: "session"},
		{label: "No", allow: false, scope: "once"},
	}
}

// permQuestion phrases the panel's question per tool, CC-style.
func permQuestion(tool string) string {
	switch tool {
	case "Bash":
		return "Do you want to run this command?"
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return "Do you want to make this edit?"
	case "WebFetch", "WebSearch":
		return "Do you want to fetch this?"
	default:
		return "Do you want to allow this?"
	}
}

// permPromptKey maps a key to the panel's action given the current selection
// and option count. It returns the (possibly moved) selection, the index of
// the option the key resolves (-1 for none), and whether the key was consumed.
// Resolution keys: the option number, enter (the selection), and the a/d
// accelerators (allow-once / deny). Navigation: ↑/↓. The caller applies the
// grace gate to resolutions and owns the diff toggle (needs model state).
func permPromptKey(key string, sel, n int) (newSel, resolve int, handled bool) {
	newSel = sel
	switch key {
	case "up":
		if newSel > 0 {
			newSel--
		}
		return newSel, -1, true
	case "down":
		if newSel < n-1 {
			newSel++
		}
		return newSel, -1, true
	case "enter":
		return newSel, newSel, true
	case "a":
		return newSel, 0, true
	case "d":
		return newSel, n - 1, true
	}
	// A bare option number resolves directly (1-based, single digit).
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if i := int(key[0] - '1'); i < n {
			return i, i, true
		}
	}
	return newSel, -1, false
}

// --------------------------------------------------------------------------
// permPrompt — the §2a permission-decision component
// --------------------------------------------------------------------------

// permPrompt is the §2a permission-decision component: one owner for both the
// tool question panel and the plan approval card — options, key grammar, and
// box rendering behind Render/Height/HandleKey. Static bodies (diff lines, plan
// markdown) are cached; dynamic chrome (the appear-fade border) renders live —
// ONE refresh discipline for both variants. It reads the model-owned decision
// state (m.pending) through the back-pointer rather than copying it, so a
// mutation (sel move, ctrl+o) is reflected without re-binding.
type permPrompt struct {
	m *TranscriptModel // for pending/showDiff, width helpers, theme epoch, clock via nowFunc

	// Static-body cache: the expensive content (rendered diff lines / wrapped
	// plan markdown) keyed by content identity. The fade border is NOT part of
	// this — it assembles live in Render, so the cache survives the appear window.
	cache  string
	key    permCacheKey
	valid  bool
	builds int // test observability: how many times the body was rebuilt
}

// permCacheKey identifies a cached body. pending is the pointer identity of the
// decision (a new request is a new pointer → miss); sel/showDiff/width/epoch are
// the mutable inputs the body depends on.
type permCacheKey struct {
	pending  *transcriptPermission
	width    int
	sel      int
	showDiff bool
	epoch    uint64
}

// permComp returns the permission component bound to this model. The component
// is a persistent field (its static-body cache survives across renders); the
// back-pointer is re-bound here so it stays correct regardless of construction
// order.
func (m *TranscriptModel) permComp() *permPrompt {
	m.perm.m = m
	return &m.perm
}

// buildPermissionBox renders the tool question box for the current pending tool
// permission. Thin shim over the component's Render (the box logic lives on
// permPrompt now); retained as the name tests and callers already reach for.
func (m *TranscriptModel) buildPermissionBox(width int) string {
	return m.permComp().Render(width)
}

// renderPlanCard renders the ExitPlanMode plan card for the current pending
// plan. Thin shim over the component's Render (dispatches on pending.isPlan).
func (m *TranscriptModel) renderPlanCard(width int) string {
	return m.permComp().Render(width)
}

// boxWidth is the framed box width for the current variant: width−2, floored at
// 10 for the tool box and 20 for the (wider) plan card.
func (p *permPrompt) boxWidth(width int) int {
	boxW := width - 2
	minW := 10
	if p.m.pending.isPlan {
		minW = 20
	}
	if boxW < minW {
		boxW = minW
	}
	return boxW
}

// body returns the cached static content (everything inside the border except
// the fade color), rebuilding only when the cache key changes.
func (p *permPrompt) body(width int) string {
	k := permCacheKey{
		pending:  p.m.pending,
		width:    width,
		sel:      p.m.pending.sel,
		showDiff: p.m.showDiff,
		epoch:    theme.Epoch(),
	}
	if p.valid && p.key == k {
		return p.cache
	}
	p.cache = p.buildBody(width)
	p.key = k
	p.valid = true
	return p.cache
}

// buildBody renders the variant's static content string. It is the expensive
// part (diff condensing / plan wrapping) the cache guards.
func (p *permPrompt) buildBody(width int) string {
	p.builds++
	if p.m.pending.isPlan {
		return p.planContent(width)
	}
	return p.toolContent(width)
}

// frame wraps the cached body in the shared kit panel — rounded border, 0×1
// padding, fixed width — with the given border color. Both Render and Height go
// through here so the geometry they see is identical (the border color is the
// only thing that varies between them).
func (p *permPrompt) frame(width int, border color.Color) string {
	// D2: framed by the shared kit panel.
	return kit.Card(kit.CardOpts{
		Content:     p.body(width),
		BorderColor: border,
		PadV:        0,
		PadH:        1,
		Width:       p.boxWidth(width),
	})
}

// Render frames the cached body with the live border: the tool box fades its
// gold border in over the appear window (§C.3); the plan card uses a steady gold
// border. Returns "" when nothing is pending.
func (p *permPrompt) Render(width int) string {
	if p.m.pending == nil {
		return ""
	}
	border := theme.Gold
	if !p.m.pending.isPlan {
		// Permission-appear: fade the gold border in from dim over the appear
		// window (§C.3), softening the mid-stream interruption.
		border = anim.LerpColor(theme.TextDim, theme.Gold, permissionAppear(p.m.pending.since))
	}
	return p.frame(width, border)
}

// Height is the framed box height. It is stable across the appear fade because
// it frames with a steady border — the fade only recolors the border, never
// changes the box's dimensions — and it reads no clock. It measures the frame
// (not body+2) so any narrow-width wrapping kit.Card does is reflected.
func (p *permPrompt) Height(width int) int {
	if p.m.pending == nil {
		return 0
	}
	return lipgloss.Height(p.frame(width, theme.Gold))
}

// toolContent builds the tool question panel's inner content: the gold tool
// badge (+ diff stat), the headline argument, the numbered options, and — when
// toggled — an expandable line-by-line diff.
func (p *permPrompt) toolContent(width int) string {
	pp := p.m.pending

	// The tool name sits on a gold badge (OnGold text needs the Gold background
	// to be visible at all — bare OnGold is near-invisible on a dark surface).
	head := lipgloss.NewStyle().Foreground(theme.OnGold).Background(theme.Gold).Bold(true).Padding(0, 1).
		Render(theme.GlyphWaiting + " " + pp.tool)
	if pp.adds > 0 || pp.dels > 0 {
		add := lipgloss.NewStyle().Foreground(theme.Guac).Render("+" + formatInt(pp.adds))
		del := lipgloss.NewStyle().Foreground(theme.Coral).Render("−" + formatInt(pp.dels))
		head += "  " + add + " " + del
	}
	lines := []string{head}
	// What the agent is actually asking to do: the Bash command, file path, URL,
	// pattern, … — so an approval is never blind.
	if pp.arg != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(truncate(pp.arg, max(4, width-6))))
	}
	// §2c: the numbered question panel replaces the [a]/[d] hotkey hint row —
	// options are the affordance (a/d remain hidden accelerators). ↵ now confirms
	// the selection, so the diff reveal moved to ctrl+o (the tool-card expansion
	// idiom); only advertise it when there is a diff to reveal.
	lines = append(lines, "")
	lines = append(lines, renderPermOptions(pp.tool, pp.sel, width-4)...)
	if len(pp.diffLines) > 0 {
		lines = append(lines, kit.KbdRow([2]string{"ctrl+o", "view diff"}))
	}
	if p.m.showDiff && len(pp.diffLines) > 0 {
		const maxDiff = 16
		shown := condenseDiff(pp.diffLines, maxDiff)
		for _, l := range shown {
			// styleDiffLine (transcript_render.go) is shared with the expanded tool
			// card so both surfaces color the diff identically. Style before
			// truncating: styleDiffLine expands tabs (H5), and truncating first
			// would measure the pre-expansion width and overflow the box.
			lines = append(lines, truncate(styleDiffLine(l), max(4, width-6)))
		}
	}
	return strings.Join(lines, "\n")
}

// planContent builds the ExitPlanMode plan card's inner content (slice 1c): the
// plan text plus three actions — reject / approve-stay / approve-and-switch. It
// is deliberately distinct from the tool panel so plan review reads as its own
// surface.
func (p *permPrompt) planContent(width int) string {
	inner := p.boxWidth(width) - 4 // account for border + horizontal padding

	// Gold badge header — OnGold text is only legible on the Gold background.
	lines := []string{lipgloss.NewStyle().Foreground(theme.OnGold).Background(theme.Gold).Bold(true).Padding(0, 1).
		Render("◈ Plan ready for review"), ""}

	body := strings.TrimSpace(p.m.pending.plan)
	if body == "" {
		body = "(the agent proposed a plan)"
	}
	const maxPlanLines = 18
	bodyStyle := lipgloss.NewStyle().Foreground(theme.TextBody)
	var wrapped []string
	for _, raw := range strings.Split(body, "\n") {
		wrapped = append(wrapped, wrapPlain(raw, inner)...)
	}
	if len(wrapped) > maxPlanLines {
		wrapped = append(wrapped[:maxPlanLines], "…")
	}
	for _, wl := range wrapped {
		lines = append(lines, bodyStyle.Render(wl))
	}

	lines = append(lines, "",
		kit.KbdRow([2]string{"r", "reject"}, [2]string{"a", "approve · stay in plan"}, [2]string{"↵", "approve & build →"}))
	return strings.Join(lines, "\n")
}

// wrapPlain word-wraps s to width columns (collapsing intra-line whitespace),
// returning at least one line so blank source lines survive as paragraph breaks.
func wrapPlain(s string, width int) []string {
	if width < 4 {
		width = 4
	}
	var lines []string
	var cur string
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case lipgloss.Width(cur)+1+lipgloss.Width(word) <= width:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// permActionKind is what a key resolves to in the permission component. The
// model executes it — the component only decides (no tea.Cmd here).
type permActionKind int

const (
	permActNone       permActionKind = iota // key consumed, no side effect (nav / grace-swallow)
	permActResolve                          // resolve the pending permission (allow/scope, optional mode step)
	permActToggleDiff                       // flip showDiff and relayout
)

// permAction is the decision HandleKey hands back to the model.
type permAction struct {
	kind    permActionKind
	allow   bool
	scope   string
	setMode bool // plan "approve & build": step to accept-edits before resolving
}

// HandleKey maps a key to the pending permission's action. answerable is the
// model-computed grace gate (permissionAnswerable): a resolving key inside the
// quiet window is consumed but swallowed (permActNone), never applied. The
// returned bool is whether the key belongs to the permission grammar at all —
// when false the model falls the key through to transcript scrolling. The model
// still owns the side effects (resolvePermission / mode change / relayout).
func (p *permPrompt) HandleKey(key string, answerable bool) (permAction, bool) {
	pp := p.m.pending
	if pp == nil {
		return permAction{}, false
	}
	if pp.isPlan {
		switch key {
		case "r": // reject: keep plan mode, deny the plan.
			if !answerable {
				return permAction{}, true
			}
			return permAction{kind: permActResolve, allow: false, scope: "once"}, true
		case "a": // approve, stay in plan mode.
			if !answerable {
				return permAction{}, true
			}
			return permAction{kind: permActResolve, allow: true, scope: "once"}, true
		case "enter": // approve & switch to accept-edits for subsequent turns.
			if !answerable {
				return permAction{}, true
			}
			return permAction{kind: permActResolve, allow: true, scope: "once", setMode: true}, true
		}
		return permAction{}, false // not plan grammar → scroll behind the prompt
	}

	// §2c numbered-options panel: ↑/↓ move the ❯ selection, ↵/number keys resolve
	// the selected/named option, a/d stay as hidden accelerators. The diff reveal
	// is ctrl+o (↵ now confirms).
	if key == "ctrl+o" {
		if len(pp.diffLines) > 0 {
			return permAction{kind: permActToggleDiff}, true
		}
		return permAction{}, true // consumed even with no diff to reveal
	}
	opts := permOptions(pp.tool)
	if newSel, resolve, handled := permPromptKey(key, pp.sel, len(opts)); handled {
		pp.sel = newSel
		if resolve < 0 {
			return permAction{}, true // navigation only
		}
		// Grace gate applies to any resolving key: a keystroke already in flight
		// when the box popped can't answer it.
		if !answerable {
			return permAction{}, true
		}
		o := opts[resolve]
		return permAction{kind: permActResolve, allow: o.allow, scope: o.scope}, true
	}
	return permAction{}, false // not panel grammar → scroll behind the prompt
}

// permWantsSummary is the shared "wants: <tool>  <arg>" one-line summary the
// dashboard perm queue (permqueue.go) renders per waiting session. It is the
// component's decision vocabulary in its terse, cross-session form; the queue
// stays allow-once and does not adopt the numbered panel — see permPrompt for
// any future richer per-item rendering. boxW bounds the argument truncation.
func permWantsSummary(tool, arg string, boxW int) string {
	note := "wants: " + tool
	// Show what is being approved (command/path/url), not just the tool name —
	// batch-approving blind defeats the queue's purpose.
	if arg != "" {
		note += "  " + truncate(arg, max(8, boxW-len(note)-6))
	}
	return note
}

// renderPermOptions renders the question line plus the numbered options with a
// ❯ on the selected row, each width-truncated.
func renderPermOptions(tool string, sel, width int) []string {
	opts := permOptions(tool)
	lines := []string{lipgloss.NewStyle().Foreground(theme.TextBody).Render(permQuestion(tool))}
	for i, o := range opts {
		row := fmt.Sprintf("%d. %s", i+1, o.label)
		if i == sel {
			lines = append(lines,
				lipgloss.NewStyle().Foreground(theme.Guac).Render(glyphChevron+" ")+
					lipgloss.NewStyle().Foreground(theme.TextBright).Render(truncate(row, max(4, width-2))))
		} else {
			lines = append(lines,
				"  "+lipgloss.NewStyle().Foreground(theme.TextMuted).Render(truncate(row, max(4, width-2))))
		}
	}
	return lines
}
