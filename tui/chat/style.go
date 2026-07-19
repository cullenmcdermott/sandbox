package chat

// style.go — the transcript items' pre-built lipgloss styles. Every style is a
// package-level var built once (and rebuilt whenever the theme swaps, via the
// theme.OnChange hook registered below), so the item Render paths reference a
// ready style instead of allocating one per frame. This is what lets
// TestNoNewStyleInRenderPaths pass and what makes a /theme swap re-skin the
// whole transcript: OnChange runs rebuildItemStyles, which re-reads the active
// palette tokens.
//
// Colors are read from tui/theme (all exported, protocol-neutral tokens). No
// value here couples to a session backend or any internal/ type.

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

var (
	// Message chrome.
	styUserQuote       lipgloss.Style // dim "> " quote head
	styUserBody        lipgloss.Style // the user's own words (quiet)
	styFocusBar        lipgloss.Style // the focus gutter bar "▌"
	styAssistantBullet lipgloss.Style // the neutral "⏺" assistant bullet

	// Tool-card chrome. The three bullet styles are indexed by tool status so
	// the ⏺ head bullet carries the running/ok/error signal.
	styToolBulletRunning lipgloss.Style
	styToolBulletOK      lipgloss.Style
	styToolBulletErr     lipgloss.Style
	styToolName          lipgloss.Style // "Name" (TextSecondary)
	styToolArg           lipgloss.Style // "(arg)" (TextMuted)
	styElbowChrome       lipgloss.Style // "  ⎿  " and the ctrl+o hint (TextDim)
	styElbowResult       lipgloss.Style // the result summary (TextMuted)
	styElbowResultErr    lipgloss.Style // the result summary on a failed card (Coral)

	// Diff lines (shared by the expanded tool card).
	styDiffAdd lipgloss.Style // "+" additions (Guac)
	styDiffDel lipgloss.Style // "−" deletions (Coral)
	styDiffCtx lipgloss.Style // context (TextMuted)
	styDiffEli lipgloss.Style // "…" elision (TextDim)

	// Reasoning ("∴ Thought"/"∴ Thinking").
	styReasonLabel lipgloss.Style // bold muted label
	styReasonBody  lipgloss.Style // italic muted body
	styReasonTrail lipgloss.Style // dim "… +N lines" trailer

	// Todos checklist.
	styTodoDone    lipgloss.Style // strikethrough faint green
	styTodoActive  lipgloss.Style // bright bold
	styTodoPending lipgloss.Style // dim
	styTodoCleared lipgloss.Style // dim "todos cleared"

	// Citations footnote.
	styCitation lipgloss.Style // dim

	// Per-turn outcome footer ("◇ model · via backend · …").
	styFooter lipgloss.Style // dim muted

	// Permission request (gold approval card).
	styPermLabel lipgloss.Style // gold bold tool/plan label
	styPermArg   lipgloss.Style // secondary arg

	// Notices (info / warning / error tones) and the coral shell elbow.
	styNoticeInfo lipgloss.Style
	styNoticeWarn lipgloss.Style
	styNoticeErr  lipgloss.Style
	styElbowCoral lipgloss.Style

	// Subagent (Task) card.
	stySubHeader   lipgloss.Style // "⊟ Task" (Hazy bold)
	stySubPrompt   lipgloss.Style // the task prompt (TextBody)
	stySubMeta     lipgloss.Style // "· agent · N tools" (TextMuted)
	stySubOK       lipgloss.Style // "✓" (Guac)
	stySubErr      lipgloss.Style // "✗" (Coral)
	stySubTree     lipgloss.Style // tree chrome "├"/"└"/"   └ " (TextDim)
	stySubChildNm  lipgloss.Style // child tool name (TextSecondary)
	stySubChildArg lipgloss.Style // child arg / detail (TextMuted)
	stySubNarr     lipgloss.Style // narration line (italic muted)
	stySubIconOK   lipgloss.Style // child ✓ (Guac)
	stySubIconErr  lipgloss.Style // child ✗ (Coral)
)

// init wires the item styles to the shared theme so a swap re-skins them, and
// populates them from the active palette once now (OnChange runs the hook
// immediately).
func init() { theme.OnChange(rebuildItemStyles) }

func rebuildItemStyles() {
	fg := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

	styUserQuote = fg(theme.TextDim)
	styUserBody = fg(theme.TextMuted)
	styFocusBar = fg(theme.Charple)
	styAssistantBullet = fg(theme.TextMuted)

	styToolBulletRunning = fg(theme.Malibu)
	styToolBulletOK = fg(theme.Guac)
	styToolBulletErr = fg(theme.Coral)
	styToolName = fg(theme.TextSecondary)
	styToolArg = fg(theme.TextMuted)
	styElbowChrome = fg(theme.TextDim)
	styElbowResult = fg(theme.TextMuted)
	styElbowResultErr = fg(theme.Coral)

	styDiffAdd = fg(theme.Guac)
	styDiffDel = fg(theme.Coral)
	styDiffCtx = fg(theme.TextMuted)
	styDiffEli = fg(theme.TextDim)

	styReasonLabel = lipgloss.NewStyle().Foreground(theme.TextMuted).Bold(true)
	styReasonBody = lipgloss.NewStyle().Foreground(theme.TextMuted).Italic(true)
	styReasonTrail = fg(theme.TextDim)

	styTodoDone = lipgloss.NewStyle().Foreground(theme.Success).Strikethrough(true).Faint(true)
	styTodoActive = lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true)
	styTodoPending = fg(theme.TextDim)
	styTodoCleared = fg(theme.TextDim)

	styCitation = fg(theme.TextDim)

	styFooter = fg(theme.TextMuted)

	styPermLabel = lipgloss.NewStyle().Foreground(theme.Gold).Bold(true)
	styPermArg = fg(theme.TextSecondary)

	styNoticeInfo = fg(theme.TextMuted)
	styNoticeWarn = fg(theme.Warning)
	styNoticeErr = fg(theme.Coral)
	styElbowCoral = fg(theme.Coral)

	stySubHeader = lipgloss.NewStyle().Foreground(theme.Hazy).Bold(true)
	stySubPrompt = fg(theme.TextBody)
	stySubMeta = fg(theme.TextMuted)
	stySubOK = fg(theme.Guac)
	stySubErr = fg(theme.Coral)
	stySubTree = fg(theme.TextDim)
	stySubChildNm = fg(theme.TextSecondary)
	stySubChildArg = fg(theme.TextMuted)
	stySubNarr = lipgloss.NewStyle().Foreground(theme.TextMuted).Italic(true)
	stySubIconOK = fg(theme.Guac)
	stySubIconErr = fg(theme.Coral)
}

// styleDiffLine colors a unified-diff line by its prefix ("+" add, "−" del, "…"
// elision, " " context). Tabs are expanded first so callers' truncation sees the
// real width. Uses the pre-built diff styles (no per-call allocation).
func styleDiffLine(l string) string {
	l = expandTabs(l)
	switch {
	case len(l) > 0 && l[0] == '+':
		return styDiffAdd.Render(l)
	case strings.HasPrefix(l, "−") || (len(l) > 0 && l[0] == '-'):
		return styDiffDel.Render(l)
	case strings.HasPrefix(l, "…"):
		return styDiffEli.Render(l)
	default:
		return styDiffCtx.Render(l)
	}
}
