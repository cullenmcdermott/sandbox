package dashboard

// permprompt.go — the numbered-option permission question panel (§2c): the
// single owner of the tool-permission decision options and their key grammar
// (the §2a permissionPrompt component's base; plan cards and the dashboard
// perm queue still render their own surfaces). CC-signature "Do you want to…?"
// question + numbered, ↑/↓-navigable options with a ❯ on the selection; a/d
// stay as hidden accelerators. The session option issues the runner's
// session-scope grant (§2b gap 2) — tool-NAME-level ("allow Bash for the rest
// of this session", runner/src/grants.ts), so the label names the tool, never
// the argument.

import (
	"fmt"

	"charm.land/lipgloss/v2"

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
