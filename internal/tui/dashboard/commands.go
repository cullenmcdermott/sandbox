package dashboard

// commands.go — the chat `/` slash-command palette and `!` one-shot shell
// (slice 2b / Mockup B). Typing `/` opens a grouped, fuzzy-filtered palette
// above the prompt (↑/↓ select, enter runs); typing `!cmd` runs a one-shot
// shell via the runner exec endpoint and renders the output as a distinct
// block. Presentation follows the original UX-lab prototype (compact grouped
// list), themed via the Phase-0a tokens.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/models"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// slashCmd is one entry in the `/` palette. run performs the command against
// the transcript model and may return a tea.Cmd for async work (e.g. exec).
type slashCmd struct {
	name string
	desc string
	run  func(m *TranscriptModel) tea.Cmd
}

// cmdGroup groups related slash commands (Session / Mode / Tools / Help).
type cmdGroup struct {
	name  string
	glyph string
	cmds  []slashCmd
}

// commandGroups is the grouped slash-command registry. Client-side commands run
// locally; /diff runs `git diff` via the runner exec endpoint. The Model group
// is built from m (its live supportedModels() list when present, else the
// alias fallback); m may be nil for contexts with no session (e.g. the static
// help reference), which yields the alias fallback.
func commandGroups(m *TranscriptModel) []cmdGroup {
	return []cmdGroup{
		{name: "Session", glyph: "◆", cmds: []slashCmd{
			{"/clear", "clear the transcript view", func(m *TranscriptModel) tea.Cmd {
				m.blocks = nil
				m.assistantBuf.Reset()
				m.streaming = false
				m.pendingTools = nil
				m.unreadIndex = 0        // re-clamp after shrink (B16)
				m.droppedPartialIdx = -1 // stale index would mis-target after rebuild (RV9)
				m.syncBody()
				return nil
			}},
		}},
		{name: "Mode", glyph: "↯", cmds: []slashCmd{
			{"/plan", "plan mode (read-only)", setModeCmd(modePlan)},
			{"/auto", "accept-edits mode (auto-accept)", setModeCmd(modeAcceptEdits)},
			{"/normal", "ask before each tool", setModeCmd(modeDefault)},
			{"/yolo", "bypass all permissions", setModeCmd(modeBypass)},
			{"/vim", "toggle vim-style modal editing (off by default)", toggleVimCmd},
		}},
		{name: "Model", glyph: "✸", cmds: modelGroupCmds(m)},
		{name: "Effort", glyph: "⚡", cmds: effortGroupCmds()},
		{name: "Tools", glyph: "▣", cmds: []slashCmd{
			{"/diff", "show the working-tree diff", func(m *TranscriptModel) tea.Cmd {
				return m.runShell("git --no-pager diff")
			}},
			{"/theme", "cycle the color theme", func(m *TranscriptModel) tea.Cmd {
				theme.Cycle()
				m.layout()
				m.appendBlock(blockInfo, "theme → "+theme.Active())
				return nil
			}},
		}},
		{name: "Help", glyph: "✦", cmds: []slashCmd{
			{"/help", "open the grouped command reference", func(m *TranscriptModel) tea.Cmd {
				m.openHelp()
				return nil
			}},
			{"/keys", "show keyboard shortcuts", func(m *TranscriptModel) tea.Cmd {
				m.openHelp()
				return nil
			}},
		}},
	}
}

// setModeCmd returns a handler that switches the per-attach permission mode and
// confirms it in the transcript (the status-line mode row also updates).
func setModeCmd(mode permMode) func(*TranscriptModel) tea.Cmd {
	return func(m *TranscriptModel) tea.Cmd {
		m.mode = mode
		m.appendBlock(blockInfo, "mode → "+mode.label())
		return nil
	}
}

// modelGroupCmds builds the Model palette group. With a live supportedModels()
// list (from a models.available event), it lists each real account model by its
// id; before that arrives — or with no session (nil m) — it falls back to the
// stable opus/sonnet/haiku aliases. /model-default is always the last entry.
func modelGroupCmds(m *TranscriptModel) []slashCmd {
	var cmds []slashCmd
	if m != nil && len(m.availableModels) > 0 {
		for _, mi := range m.availableModels {
			desc := mi.DisplayName
			if mi.Description != "" {
				desc += " — " + mi.Description
			}
			cmds = append(cmds, slashCmd{"/" + modelSlug(mi.Value), desc, setModelCmd(mi.Value, mi.DisplayName)})
		}
	} else {
		cmds = []slashCmd{
			{"/opus", "switch to Opus for new turns", setModelCmd("opus", "Opus")},
			{"/sonnet", "switch to Sonnet for new turns", setModelCmd("sonnet", "Sonnet")},
			{"/haiku", "switch to Haiku for new turns", setModelCmd("haiku", "Haiku")},
		}
	}
	return append(cmds, slashCmd{"/model-default", "revert to the account/session default model", setModelCmd("", "account default")})
}

// modelSlug turns a model id into a short, unique palette-command suffix:
// "claude-opus-4-8" -> "opus-4-8". Strips the "claude-" prefix when present and
// normalizes spaces to dashes; otherwise returns the lowercased value.
func modelSlug(value string) string {
	s := strings.ToLower(value)
	s = strings.TrimPrefix(s, "claude-")
	return strings.ReplaceAll(s, " ", "-")
}

// setModelCmd returns a handler that selects the model for subsequent turns
// (sent as TurnInput.Model on the next prompt) and confirms it in the
// transcript. An empty id reverts to the session/account default. It also
// optimistically updates the status-line model name + ctx window so the switch
// is reflected immediately instead of only after the next turn's session.started
// (T8); the display self-heals to the exact SDK-resolved id on that event.
func setModelCmd(id, label string) func(*TranscriptModel) tea.Cmd {
	return func(m *TranscriptModel) tea.Cmd {
		m.modelOverride = id
		// /model-default (empty id) restores the captured account default; a named
		// alias ("opus") shows as-is until session.started reports the full id.
		if id == "" {
			m.model = m.defaultModel
		} else {
			m.model = id
		}
		m.ctxLimit = models.Limit(m.model).ContextLimit
		m.appendBlock(blockInfo, "model → "+label)
		return nil
	}
}

// effortGroupCmds builds the STATIC /effort palette group. Unlike the Model
// group it never varies by session — the SDK reasoning-effort levels are a
// closed enum. Each entry's wire value is the real SDK EffortLevel; the top tier
// "max" is shown under the recognizable label "ultracode" (label → wire value:
// ultracode → "max"). /effort-auto clears the override (empty => SDK adaptive
// thinking). Typing /effort surfaces the whole group via the group-name match in
// filteredGroups.
func effortGroupCmds() []slashCmd {
	return []slashCmd{
		{"/effort-low", "minimal reasoning effort", setEffortCmd("low", "low")},
		{"/effort-medium", "moderate reasoning effort", setEffortCmd("medium", "medium")},
		{"/effort-high", "high reasoning effort", setEffortCmd("high", "high")},
		{"/effort-xhigh", "very high reasoning effort", setEffortCmd("xhigh", "xhigh")},
		{"/effort-ultracode", "max reasoning effort", setEffortCmd("max", "ultracode")},
		{"/effort-auto", "revert to adaptive (SDK default) effort", setEffortCmd("", "auto")},
	}
}

// setEffortCmd returns a handler that selects the reasoning-effort level for
// subsequent turns (sent as TurnInput.Effort on the next prompt) and confirms it
// in the transcript. level is the SDK wire value ("low".."max"; "max" is labeled
// "ultracode"); an empty level clears the override (SDK adaptive thinking).
// Modeled on setModeCmd — no ctxLimit recompute, no display-model mutation; the
// status-line effort tag reads m.effortOverride directly.
func setEffortCmd(level, label string) func(*TranscriptModel) tea.Cmd {
	return func(m *TranscriptModel) tea.Cmd {
		m.effortOverride = level
		m.appendBlock(blockInfo, "effort → "+label)
		return nil
	}
}

// toggleVimCmd flips vim-style modal editing. Off (the default) keeps the prompt
// focused so keys always type; on enables the NORMAL/INSERT chords (i/a/j/k/g/G/q)
// and the mode badge. The follow-up Cmd re-focuses the prompt (off) or drops to
// NORMAL (on); a transcript note confirms the new state.
func toggleVimCmd(m *TranscriptModel) tea.Cmd {
	cmd := m.setVim(!m.vimEnabled)
	state := "off"
	if m.vimEnabled {
		state = "on"
	}
	m.appendBlock(blockInfo, "vim → "+state)
	return cmd
}

// openHelp opens the shared grouped help overlay (slash commands + chat keys).
func (m *TranscriptModel) openHelp() {
	m.helpUI = newHelpModel("help", chatHelpCategories())
	m.showHelp = true
}

// --- palette filtering ----------------------------------------------------

// filteredGroups returns the groups whose commands match the query (by name,
// description, or group name, case-insensitive), preserving group/command
// order. A group-name match (e.g. "model" → "Model" group) includes the whole
// group so typing /model shows all model-switching commands, not just
// /model-default.
func filteredGroups(m *TranscriptModel, query string) []cmdGroup {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []cmdGroup
	for _, g := range commandGroups(m) {
		groupMatch := q != "" && strings.Contains(strings.ToLower(g.name), q)
		var cmds []slashCmd
		for _, c := range g.cmds {
			if q == "" || groupMatch || strings.Contains(c.name, q) || strings.Contains(strings.ToLower(c.desc), q) {
				cmds = append(cmds, c)
			}
		}
		if len(cmds) > 0 {
			out = append(out, cmdGroup{name: g.name, glyph: g.glyph, cmds: cmds})
		}
	}
	return out
}

// flatCmds is the flat, selectable command list for the current query.
func flatCmds(m *TranscriptModel, query string) []slashCmd {
	var out []slashCmd
	for _, g := range filteredGroups(m, query) {
		out = append(out, g.cmds...)
	}
	return out
}

// --- palette state (derived from the prompt input) ------------------------

func (m *TranscriptModel) paletteOpen() bool { return strings.HasPrefix(m.input.Value(), "/") }
func (m *TranscriptModel) paletteQuery() string {
	return strings.TrimPrefix(m.input.Value(), "/")
}

// --- palette rendering (compact grouped list) -----------------------------

func (m *TranscriptModel) renderPalette(width int) string {
	groups := filteredGroups(m, m.paletteQuery())
	boxW := width - 4
	if boxW < 30 {
		boxW = 30
	} else if boxW > 60 {
		boxW = 60
	}
	var lines []string
	idx := 0
	for _, g := range groups {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextMuted).Bold(true).Render(g.glyph+" "+strings.ToUpper(g.name)))
		for _, c := range g.cmds {
			lines = append(lines, m.cmdLine(c, idx == m.cmdSel, boxW-2))
			idx++
		}
	}
	if len(lines) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextMuted).Render("no matching commands"))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Malibu).
		Background(theme.Surface).
		Width(boxW).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m *TranscriptModel) cmdLine(c slashCmd, sel bool, w int) string {
	if sel {
		return lipgloss.NewStyle().Background(theme.Raised2).Width(w).Render(
			lipgloss.NewStyle().Foreground(theme.Guac).Render("› ") +
				lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(padTo(c.name, 10)) +
				lipgloss.NewStyle().Foreground(theme.TextBody).Render(c.desc))
	}
	return "  " + lipgloss.NewStyle().Foreground(theme.Malibu).Render(padTo(c.name, 12)) +
		lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(c.desc)
}

// padTo pads s to w visible columns (no truncation; command names are short).
func padTo(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// --- palette key handling -------------------------------------------------

// paletteKey handles a key while the `/` palette is open. It returns the
// follow-up command and whether the key was consumed by the palette.
func (m *TranscriptModel) paletteKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "up":
		if m.cmdSel > 0 {
			m.cmdSel--
		}
		return nil, true
	case "down":
		if n := len(flatCmds(m, m.paletteQuery())); m.cmdSel < n-1 {
			m.cmdSel++
		}
		return nil, true
	case "enter":
		cmds := flatCmds(m, m.paletteQuery())
		var cmd tea.Cmd
		if len(cmds) > 0 && m.cmdSel < len(cmds) {
			cmd = cmds[m.cmdSel].run(m)
		} else {
			m.appendBlock(blockInfo, "unknown command: "+strings.TrimSpace(m.input.Value()))
		}
		m.input.Reset()
		m.cmdSel = 0
		m.layout()
		return cmd, true
	}
	// Any other key edits the query: let the input handle it, then re-clamp the
	// selection and re-layout (the palette height changes as results filter).
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if n := len(flatCmds(m, m.paletteQuery())); m.cmdSel >= n {
		m.cmdSel = 0
	}
	m.layout()
	return cmd, true
}

// --- shell passthrough ----------------------------------------------------

// shellResultMsg carries a one-shot `!` command result back to the model.
type shellResultMsg struct {
	command string
	res     session.ExecResult
	err     error
}

// runShell runs a `!cmd` one-shot via the runner exec endpoint; the result is
// delivered as a shellResultMsg and rendered as a distinct shell block.
func (m *TranscriptModel) runShell(command string) tea.Cmd {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	client, ref := m.client, m.ref
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		res, err := client.Exec(ctx, ref, command)
		return shellResultMsg{command: command, res: res, err: err}
	}
}

// appendShellBlock renders a completed `!cmd` as a distinct block: a peach
// header, the captured output, and a coral exit-code note when nonzero.
func (m *TranscriptModel) appendShellBlock(command string, res session.ExecResult, err error) {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(theme.Peach).Bold(true).Render("! " + command))
	if err != nil {
		b.WriteString("\n" + styleTError.Render("  "+err.Error()))
		m.blocks = append(m.blocks, tblock{kind: blockShell, text: b.String()})
		m.syncBody()
		return
	}
	body := strings.TrimRight(res.Stdout, "\n")
	if res.Stderr != "" {
		if body != "" {
			body += "\n"
		}
		body += strings.TrimRight(res.Stderr, "\n")
	}
	if body != "" {
		// Remap the program's own ANSI (bright red, etc.) onto the active theme
		// palette before it enters the block (§A.2), then indent each line. The
		// indent carries the base body color; embedded (remapped) SGR overrides.
		body = kit.RemapANSI(body)
		indent := lipgloss.NewStyle().Foreground(theme.TextBody).Render("  ")
		for _, line := range strings.Split(body, "\n") {
			b.WriteString("\n" + indent + line)
		}
	}
	if res.ExitCode != 0 {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(theme.Coral).Render(fmt.Sprintf("  · exit %d", res.ExitCode)))
	}
	m.blocks = append(m.blocks, tblock{kind: blockShell, text: b.String()})
	m.syncBody()
}
