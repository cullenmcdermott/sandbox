package dashboard

// attention.go — Tier-4 attention routing (design-system-and-states.md §3): the
// persistent attention surface, attention-first ordering, overflow summary,
// per-session dots, and group rollups that answer "which of N agents needs me?".

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// needsAttention reports whether a session needs user attention.
func needsAttention(s Session) bool {
	return s.DashStatus == StatusWaiting || s.DashStatus == StatusNeedsInput || s.DashStatus == StatusFailed
}

// sortByAttention floats sessions that need attention to the top (regardless of
// the manual sort) when attentionFirst is set, preserving the input order within
// each attention tier (a stable partition). With attentionFirst false it returns
// the input order unchanged.
func sortByAttention(sessions []Session, attentionFirst bool) []Session {
	if !attentionFirst {
		return sessions
	}
	out := make([]Session, 0, len(sessions))
	for _, s := range sessions {
		if needsAttention(s) {
			out = append(out, s)
		}
	}
	for _, s := range sessions {
		if !needsAttention(s) {
			out = append(out, s)
		}
	}
	return out
}

// attentionSummary renders the persistent attention count, e.g.
// "2 waiting · 1 needs input", or "" when nothing needs attention.
func attentionSummary(sessions []Session) string {
	waiting, needs, failed := 0, 0, 0
	for _, s := range sessions {
		switch s.DashStatus {
		case StatusWaiting:
			waiting++
		case StatusNeedsInput:
			needs++
		case StatusFailed:
			failed++
		}
	}
	var parts []string
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", waiting))
	}
	if needs > 0 {
		parts = append(parts, fmt.Sprintf("%d needs input", needs))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	return strings.Join(parts, " · ")
}

// overflowSummary renders the off-screen attention band, e.g.
// "+18 more · 3 busy · 1 waiting below", or "" when nothing is hidden.
func overflowSummary(hidden []Session) string {
	if len(hidden) == 0 {
		return ""
	}
	busy, waiting, needs, failed := 0, 0, 0, 0
	for _, s := range hidden {
		switch s.DashStatus {
		case StatusBusy:
			busy++
		case StatusWaiting:
			waiting++
		case StatusNeedsInput:
			needs++
		case StatusFailed:
			failed++
		}
	}
	parts := []string{fmt.Sprintf("+%d more", len(hidden))}
	if busy > 0 {
		parts = append(parts, fmt.Sprintf("%d busy", busy))
	}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting below", waiting))
	}
	if needs > 0 {
		parts = append(parts, fmt.Sprintf("%d needs input below", needs))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed below", failed))
	}
	return strings.Join(parts, " · ")
}

// attentionDot renders a per-session unread/attention dot for the list row, or
// "" when the session does not currently need attention.
func attentionDot(s Session) string {
	switch s.DashStatus {
	case StatusWaiting:
		return lipgloss.NewStyle().Foreground(theme.Gold).Render("●")
	case StatusNeedsInput:
		return lipgloss.NewStyle().Foreground(theme.Guac).Render("●")
	case StatusFailed:
		// Failed sessions are actionable too (P6): a coral dot floats them up
		// alongside waiting/needs-input rows.
		return lipgloss.NewStyle().Foreground(theme.Coral).Render("●")
	}
	return ""
}

// groupAttentionCount counts sessions in a group that need attention so a
// collapsed group header can still signal.
func groupAttentionCount(sessions []Session) int {
	c := 0
	for _, s := range sessions {
		if needsAttention(s) {
			c++
		}
	}
	return c
}
