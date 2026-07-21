package dashboard

// dirpicker.go — the project-directory step of the new-session overlay (TODO §9
// T10). It is the overlay's FIRST stage: `n` lands here before the backend
// chooser, so a dashboard-created session is no longer hard-wired to the
// dashboard process's cwd. Rows: the cwd (the default — one extra enter keeps
// the old flow), recent project paths from the local session index (injected
// via RunOptions.RecentProjects; the dashboard never imports internal/index),
// and a free-text path entry with ~-expansion + Tab-completion. Every accepted
// path is validated fail-closed through internal/projpath — the same
// normalization `sandbox claude` applies to its cwd — and threads to the
// Creator as CreateParams.ProjectPath via the beginCreate funnel.

import (
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/projpath"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// dirRowKind identifies what a directory-picker row is.
type dirRowKind int

const (
	// dirRowCwd is the dashboard process's working directory (the default row).
	dirRowCwd dirRowKind = iota
	// dirRowRecent is a recently used project path from the local session index.
	dirRowRecent
	// dirRowOther opens the free-text path entry (stageDirInput).
	dirRowOther
)

// dirRow is one selectable row of the directory stage. path is the project
// path the row stands for ("" for dirRowOther).
type dirRow struct {
	kind dirRowKind
	path string
}

// maxRecentDirRows caps the recents shown so the overlay stays a compact
// dialog; the free-text row reaches anything older.
const maxRecentDirRows = 5

// buildDirRows assembles the directory stage's rows: cwd first (when known),
// then deduped most-recent-first recents (cwd excluded — it's already row 0),
// then the free-text entry.
func (a *App) buildDirRows() []dirRow {
	var rows []dirRow
	if a.workDir != "" {
		rows = append(rows, dirRow{kind: dirRowCwd, path: a.workDir})
	}
	if a.recentProjects != nil {
		seen := map[string]bool{a.workDir: true}
		recents := 0
		for _, p := range a.recentProjects() {
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			rows = append(rows, dirRow{kind: dirRowRecent, path: p})
			recents++
			if recents >= maxRecentDirRows {
				break
			}
		}
	}
	return append(rows, dirRow{kind: dirRowOther})
}

// enterDirStage (re)enters the directory stage. Rows are built once per overlay
// open (they can't change mid-flow); when returning from a later stage the
// selection lands back on the previously accepted path.
func (a *App) enterDirStage() {
	if a.picker.dirRows == nil {
		a.picker.dirRows = a.buildDirRows()
	}
	a.picker.stage = stageDir
	a.picker.formErr = nil
	a.picker.sel = dirRowIndexFor(a.picker.dirRows, a.picker.projectPath)
}

// dirRowIndexFor returns the row index holding path. An empty path (nothing
// accepted yet) selects row 0; a path no row lists (it came from free text)
// selects the free-text row so esc-back lands where the user actually was.
func dirRowIndexFor(rows []dirRow, path string) int {
	if path == "" {
		return 0
	}
	for i, r := range rows {
		if r.kind != dirRowOther && r.path == path {
			return i
		}
	}
	if n := len(rows); n > 0 && rows[n-1].kind == dirRowOther {
		return n - 1
	}
	return 0
}

// pickerKeyDir handles the directory row chooser. enter on a path row validates
// it fail-closed (a recent's directory may have been deleted since its session
// ran — surface that here, inline, not deep inside the create) and advances to
// the backend stage; enter on the free-text row opens the path input; esc
// closes the overlay (this is the first stage).
func (a *App) pickerKeyDir(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	rows := a.picker.dirRows
	switch msg.String() {
	case "esc":
		a.closeBackendPicker()
		return nil, true
	case "up", "k":
		if a.picker.sel > 0 {
			a.picker.sel--
		}
		return nil, true
	case "down", "j":
		if a.picker.sel < len(rows)-1 {
			a.picker.sel++
		}
		return nil, true
	case "enter":
		sel := a.picker.sel
		if sel < 0 || sel >= len(rows) {
			sel = 0
		}
		row := rows[sel]
		if row.kind == dirRowOther {
			return a.enterDirInput(), true
		}
		p, err := projpath.ValidateDir(row.path, a.homeDir)
		if err != nil {
			a.picker.formErr = err
			return nil, true
		}
		a.acceptProjectDir(p)
		return nil, true
	}
	return nil, false
}

// acceptProjectDir records the validated project path and advances to the
// backend stage — the single hand-off from both the row chooser and the
// free-text input.
func (a *App) acceptProjectDir(path string) {
	a.picker.projectPath = path
	a.picker.formErr = nil
	a.picker.input = textinput.Model{}
	a.picker.stage = stageBackend
	a.picker.sel = 0
}

// enterDirInput opens the free-text path entry, prefilled with the working
// directory (in ~ form) as an editable base for Tab-completion.
func (a *App) enterDirInput() tea.Cmd {
	ti := textinput.New()
	ti.Placeholder = "~/path/to/project"
	ti.CharLimit = 512
	ti.Prompt = "  path: "
	ti.SetValue(abbreviateHome(a.workDir, a.homeDir))
	ti.CursorEnd()
	cmd := ti.Focus()
	a.picker.input = ti
	a.picker.formErr = nil
	a.picker.stage = stageDirInput
	a.picker.sel = 0
	return cmd
}

// pickerKeyDirInput edits the free-text path field. Tab completes against the
// filesystem (dirpicker_path.go); enter validates fail-closed (~-expansion,
// must exist, must be a directory) with the error inline, exactly the console
// form's formErr pattern; esc walks back to the row chooser.
func (a *App) pickerKeyDirInput(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		a.picker.input = textinput.Model{}
		a.picker.formErr = nil
		a.enterDirStage()
		return nil, true
	case "tab":
		if v := completeDirPath(a.picker.input.Value(), a.homeDir); v != a.picker.input.Value() {
			a.picker.input.SetValue(v)
			a.picker.input.CursorEnd()
		}
		return nil, true
	case "enter":
		p, err := projpath.ValidateDir(a.picker.input.Value(), a.homeDir)
		if err != nil {
			a.picker.formErr = err
			return nil, true
		}
		a.acceptProjectDir(p)
		return nil, true
	}
	ti, cmd := a.picker.input.Update(msg)
	a.picker.input = ti
	return cmd, true
}

// --------------------------------------------------------------------------
// Rendering
// --------------------------------------------------------------------------

// dirRowParts returns the (mark, label, desc) triple a directory row renders.
func dirRowParts(row dirRow, home string) (mark, label, desc string) {
	switch row.kind {
	case dirRowCwd:
		return "⌂", "current dir", abbreviateHome(row.path, home)
	case dirRowRecent:
		return "•", filepath.Base(row.path), abbreviateHome(row.path, home)
	default: // dirRowOther
		return "…", "other path", "type a directory path (Tab completes)"
	}
}

// renderDirPicker builds the directory row chooser box.
func (a *App) renderDirPicker() string {
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("project directory")
	lines := []string{kit.TitledRule(title, pickerInnerW, theme.Charple, theme.Dolly)}

	const labelW = 16
	sel := a.picker.sel
	if sel < 0 {
		sel = 0
	}
	if sel >= len(a.picker.dirRows) {
		sel = len(a.picker.dirRows) - 1
	}
	for i, row := range a.picker.dirRows {
		mark, label, desc := dirRowParts(row, a.homeDir)
		lines = append(lines, pickerRow(mark, label, desc, i == sel, pickerInnerW, labelW))
	}
	if a.picker.formErr != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.Coral).Render(truncate("✗ "+a.picker.formErr.Error(), pickerInnerW)))
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", pickerInnerW)),
		kit.KbdRow([2]string{"↑/↓", "select"}, [2]string{"↵", "choose"}, [2]string{"esc", "cancel"}),
	)
	return pickerBox(lines)
}

// renderDirInput builds the free-text path entry box. Validation errors render
// inline in the console form's ✗ style.
func (a *App) renderDirInput() string {
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("project directory")
	lines := []string{
		kit.TitledRule(title, pickerInnerW, theme.Charple, theme.Dolly),
		lipgloss.NewStyle().Foreground(theme.TextMuted).Render("type the project directory (Tab completes):"),
		a.picker.input.View(),
	}
	if a.picker.formErr != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.Coral).Render(truncate("✗ "+a.picker.formErr.Error(), pickerInnerW)))
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", pickerInnerW)),
		kit.KbdRow([2]string{"tab", "complete"}, [2]string{"↵", "use"}, [2]string{"esc", "back"}),
	)
	return pickerBox(lines)
}
