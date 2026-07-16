package dashboard

// modelpicker.go — the Claude-Code-style /model picker overlay (CC parity).
// Replaces the per-model slash commands (/opus, /sonnet, /haiku, …) with a
// single /model command that opens a numbered picker. Two things this buys us
// over the old alias trio:
//   - The Default row replaces /model-default (revert to the account default).
//   - The static fallback list below always includes Fable, so Fable is
//     reachable BEFORE the models.available event lands — the old /opus /sonnet
//     /haiku fallback trio never surfaced it.
//
// The picker is a full-capture overlay like the help surface (showHelp): while
// open it owns every key (handleKey preempts ahead of the globals) and closes on
// esc without detaching (escapeConsumes mirrors this). Its box follows the
// help/permqueue idiom (rounded border, theme.Surface); rows are numbered CC
// style with a ❯ on the selection and a dim-green ✓ on the current choice.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// modelPicker is the /model picker overlay state. Zero value (open == false)
// costs nothing; rows are built once at open (openModelPicker).
type modelPicker struct {
	open bool
	sel  int              // cursor / selected row
	rows []modelPickerRow // built at open
}

// modelPickerRow is one selectable model. id "" marks the account-default row
// (setModelCmd treats an empty id as "revert to the account/session default").
type modelPickerRow struct {
	id, name, desc string
	current        bool // marks the active choice (matches m.modelOverride)
}

// fallbackModels is the STATIC list shown until a models.available event
// arrives (or with no session). The ids are exactly what TurnInput.Model sends
// on the next turn. Fable leads and MUST stay reachable here — pre-
// models.available this is the only way to pick Fable (the retired /opus
// /sonnet /haiku alias trio couldn't reach it).
var fallbackModels = []modelPickerRow{
	{id: "claude-fable-5", name: "Fable 5", desc: "most capable"},
	{id: "claude-opus-4-8", name: "Opus 4.8"},
	{id: "claude-sonnet-5", name: "Sonnet 5"},
	{id: "claude-haiku-4-5-20251001", name: "Haiku 4.5", desc: "fastest"},
}

// openModelPicker builds the picker rows and opens the overlay. Row 0 is the
// account-default choice; the rest come from m.availableModels when present,
// else the static fallback. The current row (id == m.modelOverride, which the
// Default row satisfies when there is no override) is marked and pre-selected.
func (m *TranscriptModel) openModelPicker() {
	rows := []modelPickerRow{{id: "", name: "Default", desc: m.defaultRowDesc()}}
	if len(m.availableModels) > 0 {
		for _, mi := range m.availableModels {
			rows = append(rows, modelPickerRow{id: mi.Value, name: mi.DisplayName, desc: mi.Description})
		}
	} else {
		rows = append(rows, fallbackModels...)
	}
	sel := 0
	for i := range rows {
		// An empty modelOverride marks the Default row (id ""); a set override
		// marks its matching model. Same predicate handles both.
		if rows[i].id == m.modelOverride {
			rows[i].current = true
			sel = i
		}
	}
	m.modelPicker = modelPicker{open: true, sel: sel, rows: rows}
}

// defaultRowDesc names the account default in the Default row when it is known
// (captured from the first session.started), else a bare label.
func (m *TranscriptModel) defaultRowDesc() string {
	if m.defaultModel != "" {
		return "account default (" + m.defaultModel + ")"
	}
	return "account default"
}

// modelPickerKey maps a key to the picker's action given the current selection
// and row count, mirroring permPromptKey's grammar. It returns the (possibly
// moved) selection, the row the key chooses (-1 for none), and whether the key
// was consumed. Nav: ↑/↓. Choose: a bare row number (1-based) jumps+selects, and
// enter selects the current row. esc is NOT handled here — the caller closes on
// esc (like showHelp closes ahead of the esc cascade).
func modelPickerKey(key string, sel, n int) (newSel, choose int, handled bool) {
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
	}
	// A bare row number jumps to and selects that row (1-based, single digit).
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if i := int(key[0] - '1'); i < n {
			return i, i, true
		}
		return newSel, -1, true // in-range-less digit: swallow, no-op
	}
	return newSel, -1, false
}

// modelPickerKeyHandler routes a key while the picker is open. esc closes it
// (no selection); otherwise the pure grammar drives nav/digit/enter, and a
// chosen row runs setModelCmd (modelOverride + optimistic Model/CtxLimit +
// confirm block — same semantics the old per-model commands used) then closes.
// Unhandled keys are swallowed so the blurred prompt never collects them.
func (m *TranscriptModel) modelPickerKeyHandler(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" {
		m.modelPicker = modelPicker{}
		return nil
	}
	newSel, choose, _ := modelPickerKey(msg.String(), m.modelPicker.sel, len(m.modelPicker.rows))
	m.modelPicker.sel = newSel
	if choose >= 0 && choose < len(m.modelPicker.rows) {
		r := m.modelPicker.rows[choose]
		cmd := setModelCmd(r.id, r.name)(m)
		m.modelPicker = modelPicker{}
		return cmd
	}
	return nil
}

// renderModelPicker builds the picker overlay box (CC-style numbered rows) for
// the given width. Mirrors the help/permqueue box: rounded border, theme.Surface.
func (m *TranscriptModel) renderModelPicker(width int) string {
	boxW := width - 8
	if boxW < 30 {
		boxW = 30
	} else if boxW > 60 {
		boxW = 60
	}
	lines := []string{lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render("Select model"), ""}
	for i, r := range m.modelPicker.rows {
		lines = append(lines, m.modelPickerLine(i, r, boxW-2))
	}
	lines = append(lines, "", kit.KbdRow(
		[2]string{"↑/↓", "select"},
		[2]string{"1-9", "jump"},
		[2]string{"enter", "choose"},
		[2]string{"esc", "close"},
	))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Malibu).
		Background(theme.Surface).
		Width(boxW).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// modelPickerLine renders one numbered row: "❯ 1. <name>  <dim desc>" with the
// current choice suffixed by a dim-green ✓. The selected row is highlighted
// (chevron + Raised2 background), matching the palette's cmdLine idiom.
func (m *TranscriptModel) modelPickerLine(i int, r modelPickerRow, w int) string {
	num := fmt.Sprintf("%d. ", i+1)
	var suffix string
	if r.current {
		suffix = " " + lipgloss.NewStyle().Foreground(theme.Guac).Faint(true).Render("✓")
	}
	var desc string
	if r.desc != "" {
		desc = "  " + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(r.desc)
	}
	if i == m.modelPicker.sel {
		body := lipgloss.NewStyle().Foreground(theme.Guac).Render(glyphChevron+" ") +
			lipgloss.NewStyle().Foreground(theme.TextDim).Render(num) +
			lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(r.name) + suffix + desc
		return lipgloss.NewStyle().Background(theme.Raised2).Width(w).Render(body)
	}
	return "  " + lipgloss.NewStyle().Foreground(theme.TextDim).Render(num) +
		lipgloss.NewStyle().Foreground(theme.Malibu).Render(r.name) + suffix + desc
}
