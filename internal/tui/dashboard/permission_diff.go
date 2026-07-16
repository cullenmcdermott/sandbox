package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// resolvePermission answers the pending permission and dispatches the decision.
// scope "session" records a tool-name grant runner-side (grants.ts) so the tool
// auto-allows for the rest of this session (§2b gap 2); anything else is a
// one-shot "once".
func (m *TranscriptModel) resolvePermission(allow bool, scope string) tea.Cmd {
	if m.pending == nil {
		return nil
	}
	if scope != "session" {
		scope = "once"
	}
	decision := session.PermissionDecision{
		Session:    m.ref.ID,
		Permission: m.pending.id,
		Allow:      allow,
		Scope:      scope,
	}
	label := "denied"
	if allow {
		label = "approved"
		if scope == "session" {
			// Name the grant's real breadth (tool-level, not this exact argument).
			label = "approved · " + m.pending.tool + " allowed for this session"
		}
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
