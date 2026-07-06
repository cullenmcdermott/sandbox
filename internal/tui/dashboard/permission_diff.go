package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// buildPermissionBox renders the inline gold-bordered permission prompt with a
// +N −N diff stat and, when toggled, an expandable line-by-line diff.
func (m *TranscriptModel) buildPermissionBox(width int) string {
	p := m.pending

	// The tool name sits on a gold badge (OnGold text needs the Gold background
	// to be visible at all — bare OnGold is near-invisible on a dark surface).
	head := lipgloss.NewStyle().Foreground(theme.OnGold).Background(theme.Gold).Bold(true).Padding(0, 1).
		Render(theme.GlyphWaiting + " " + p.tool)
	if p.adds > 0 || p.dels > 0 {
		add := lipgloss.NewStyle().Foreground(theme.Guac).Render("+" + formatInt(p.adds))
		del := lipgloss.NewStyle().Foreground(theme.Coral).Render("−" + formatInt(p.dels))
		head += "  " + add + " " + del
	}
	// [↵] only does something when there is a diff to reveal; advertising it for
	// Bash/WebFetch/… was a dead affordance.
	keys := [][2]string{{"a", "approve"}, {"d", "deny"}}
	if len(p.diffLines) > 0 {
		keys = append(keys, [2]string{"↵", "view diff"})
	}
	hint := kit.KbdRow(keys...)

	lines := []string{head}
	// What the agent is actually asking to do: the Bash command, file path, URL,
	// pattern, … — so an approval is never blind.
	if p.arg != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(truncate(p.arg, max(4, width-6))))
	}
	lines = append(lines, hint)
	if m.showDiff && len(p.diffLines) > 0 {
		const maxDiff = 16
		shown := condenseDiff(p.diffLines, maxDiff)
		for _, l := range shown {
			// styleDiffLine (transcript_render.go) is shared with the expanded tool
			// card so both surfaces color the diff identically.
			lines = append(lines, styleDiffLine(truncate(l, max(4, width-6))))
		}
	}

	boxW := width - 2
	if boxW < 10 {
		boxW = 10
	}
	// Permission-appear: fade the gold border in from dim over the appear window
	// (§C.3), softening the mid-stream interruption.
	border := anim.LerpColor(theme.TextDim, theme.Gold, permissionAppear(p.since))
	// D2: framed by the shared kit panel — same rounded border, 0×1 padding, and
	// fixed width as before, with the animated border color passed through.
	return kit.Card(kit.CardOpts{
		Content:     strings.Join(lines, "\n"),
		BorderColor: border,
		PadV:        0,
		PadH:        1,
		Width:       boxW,
	})
}

// renderPlanCard renders the gold ExitPlanMode plan card (slice 1c): the plan
// text plus three actions — reject / approve-stay / approve-and-switch. It is
// deliberately distinct from the permission box so plan review reads as its own
// surface.
func (m *TranscriptModel) renderPlanCard(width int) string {
	boxW := width - 2
	if boxW < 20 {
		boxW = 20
	}
	inner := boxW - 4 // account for border + horizontal padding

	// Gold badge header — OnGold text is only legible on the Gold background.
	lines := []string{lipgloss.NewStyle().Foreground(theme.OnGold).Background(theme.Gold).Bold(true).Padding(0, 1).
		Render("◈ Plan ready for review"), ""}

	body := strings.TrimSpace(m.pending.plan)
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

	// D2: framed by the shared kit panel (gold border, 0×1 padding, fixed width).
	return kit.Card(kit.CardOpts{
		Content:     strings.Join(lines, "\n"),
		BorderColor: theme.Gold,
		PadV:        0,
		PadH:        1,
		Width:       boxW,
	})
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

// resolvePermission answers the pending permission and dispatches the decision.
func (m *TranscriptModel) resolvePermission(allow bool) tea.Cmd {
	if m.pending == nil {
		return nil
	}
	decision := session.PermissionDecision{
		Session:    m.ref.ID,
		Permission: m.pending.id,
		Allow:      allow,
		Scope:      "once",
	}
	label := "denied"
	if allow {
		label = "approved"
	}
	m.appendBlock(blockInfo, "  [permission "+label+"]")
	m.pending = nil
	m.showDiff = false
	if m.turnActive {
		m.DashStatus = StatusBusy
	}
	m.layout()

	client, ref := m.client, m.ref
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := client.ResolvePermission(ctx, ref, decision); err != nil {
			return permResolveErrMsg{err: err}
		}
		return nil
	}
}

// --------------------------------------------------------------------------
// Diff stat
// --------------------------------------------------------------------------

// permissionDiffStat extracts a +adds/−dels line count and a "+"/"−"-prefixed
// diff preview from an edit-like tool's input. Non-edit tools yield zeroes.
func permissionDiffStat(tool string, input json.RawMessage) (adds, dels int, diffLines []string) {
	switch tool {
	case "Edit":
		var p struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if json.Unmarshal(input, &p) == nil {
			return diffOf(p.OldString, p.NewString)
		}
	case "Write":
		var p struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(input, &p) == nil {
			return diffOf("", p.Content)
		}
	case "MultiEdit":
		var p struct {
			Edits []struct {
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			} `json:"edits"`
		}
		if json.Unmarshal(input, &p) == nil {
			for _, e := range p.Edits {
				a, d, dl := diffOf(e.OldString, e.NewString)
				adds += a
				dels += d
				diffLines = append(diffLines, dl...)
			}
			return adds, dels, diffLines
		}
	}
	return 0, 0, nil
}

// diffOf computes a minimal line diff between oldStr and newStr via LCS, so an
// edit that changes a few lines of many renders as a few +/− lines around
// unchanged context — not the whole block twice. diffLines are prefixed with
// "+", "−", or " " (context). adds/dels are the changed-line counts.
func diffOf(oldStr, newStr string) (adds, dels int, diffLines []string) {
	a := splitLines(oldStr)
	b := splitLines(newStr)
	n, mm := len(a), len(b)

	// LCS length table (suffix DP).
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, mm+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := mm - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	// Walk the table emitting an interleaved unified diff.
	i, j := 0, 0
	for i < n && j < mm {
		switch {
		case a[i] == b[j]:
			diffLines = append(diffLines, " "+a[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			diffLines = append(diffLines, "−"+a[i])
			dels++
			i++
		default:
			diffLines = append(diffLines, "+"+b[j])
			adds++
			j++
		}
	}
	for ; i < n; i++ {
		diffLines = append(diffLines, "−"+a[i])
		dels++
	}
	for ; j < mm; j++ {
		diffLines = append(diffLines, "+"+b[j])
		adds++
	}
	return adds, dels, diffLines
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// condenseDiff keeps changed lines plus one line of surrounding context and
// collapses long unchanged runs into a "… N unchanged" marker, capping the
// result at maxLines (with a trailing "… more" when it overflows).
func condenseDiff(lines []string, maxLines int) []string {
	n := len(lines)
	keep := make([]bool, n)
	for i, l := range lines {
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "−") {
			keep[i] = true
			if i > 0 {
				keep[i-1] = true
			}
			if i+1 < n {
				keep[i+1] = true
			}
		}
	}
	var out []string
	for i := 0; i < n; {
		if keep[i] {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < n && !keep[j] {
			j++
		}
		if skipped := j - i; skipped > 0 {
			out = append(out, "… "+formatInt(skipped)+" unchanged")
		}
		i = j
	}
	if len(out) > maxLines {
		out = append(out[:maxLines], "… more")
	}
	return out
}
