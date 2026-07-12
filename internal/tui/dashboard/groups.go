package dashboard

// groups.go — group-by-repo and rename for the session list (slice
// 5f / Mockup C, design S15). Sessions can be grouped by project repo
// (collapsible) and renamed to human labels.
//
// Row model (§2a): the session list is ONE ordered slice of listRow values —
// visibleRows(). Each row is a typed variant (rowKind): a session row or a
// group header. The cursor indexes THAT slice, and sessionAt(cursor) is the
// single accessor render, navigation, and actions share. Group view on/off only
// changes how visibleRows() is built, never how consumers walk it. visibleSessions()
// is the flat, filtered+sorted session *data* the builder draws from — it is never
// indexed by the cursor.
//
// Archive (a separate "finished" section, design S15) is intentionally NOT
// implemented here: the earlier `A`/archiveSelected/Session.Archived scaffold
// was a no-op (nothing read the flag) and misled users, so it was removed. When
// it lands it becomes a new rowKind (e.g. a section header + archived rows) added
// to visibleRows() — the row model is now the right home for it. See TODO.md §1b.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// groupViewState tracks which repo groups are expanded in group view.
type groupViewState struct {
	open  bool
	repos map[string]bool // repo -> expanded
}

// toggleGroupView cycles group view on/off. When turning on, expand all groups.
func (m *Model) toggleGroupView() {
	m.groupView.open = !m.groupView.open
	if m.groupView.open {
		if m.groupView.repos == nil {
			m.groupView.repos = make(map[string]bool)
		}
		for _, s := range m.sessions {
			m.groupView.repos[repoKey(s)] = true
		}
	}
	m.cursor = 0
	m.clampCursor()
}

// toggleRepoGroup expands or collapses the group for the repo at the cursor.
// The cursor indexes display rows (headers included), so it resolves the repo
// from the row itself: a header row toggles its own group, a session row
// toggles the group it belongs to.
func (m *Model) toggleRepoGroup() {
	if !m.groupView.open {
		return
	}
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return
	}
	row := rows[m.cursor]
	repo := row.repo
	if row.kind == rowSession {
		repo = repoKey(*row.session)
	}
	m.groupView.repos[repo] = !m.groupView.repos[repo]
}

// repoKey returns the grouping key for a session (project repo base).
func repoKey(s Session) string {
	return filepathBaseLocal(s.State.ProjectPath)
}

// groupedSessions returns sessions partitioned by repo when group view is on.
// Each group header is represented by a headerRow (rowHeader) with its repo label.
//
// Groups are built from visibleSessions() — NOT the raw m.sessions — so the `/`
// filter narrows group contents (and drops now-empty groups) and the
// attention-first float carries into group ordering. Repo order follows first
// appearance in the already-filtered/sorted visibleSessions list.
func (m *Model) groupedSessions() []listRow {
	visible := m.visibleSessions()
	type key struct {
		repo     string
		expanded bool
	}
	order := make(map[string]int)
	var groups []key
	for _, s := range visible {
		r := repoKey(s)
		if _, ok := order[r]; !ok {
			order[r] = len(groups)
			expanded := true
			if m.groupView.repos != nil {
				expanded = m.groupView.repos[r]
			}
			groups = append(groups, key{repo: r, expanded: expanded})
		}
	}
	var out []listRow
	for _, g := range groups {
		out = append(out, headerRow(g.repo))
		if !g.expanded {
			continue
		}
		for i := range visible {
			if repoKey(visible[i]) == g.repo {
				s := visible[i]
				out = append(out, sessionRow(&s))
			}
		}
	}
	return out
}

// rowKind is the closed set of session-list row variants. It is the discriminator
// for the typed row model: a new row class (e.g. the S15 archived section) is a new
// rowKind added here and emitted by visibleRows() — the one place the list's row
// vocabulary is defined.
type rowKind int

const (
	rowSession rowKind = iota // a real session; session != nil
	rowHeader                 // a repo group header; repo carries the label
)

// listRow is one display row of the session list: either a repo header or a real
// session, tagged by kind. The cursor indexes the []listRow that visibleRows()
// returns; sessionAt() resolves a row index to its session.
type listRow struct {
	kind    rowKind
	repo    string   // header label (rowHeader)
	session *Session // payload (rowSession)
}

// sessionRow builds a session data row.
func sessionRow(s *Session) listRow { return listRow{kind: rowSession, session: s} }

// headerRow builds a repo group header row.
func headerRow(repo string) listRow { return listRow{kind: rowHeader, repo: repo} }

// openRename starts renaming the selected session.
func (m *Model) openRename() {
	sel := m.selectedSession()
	if sel == nil {
		return
	}
	m.renaming = true
	m.renameBuf = sel.DisplayTitle()
}

// commitRename applies the rename buffer to the selected session (via the
// header-aware accessor, so group view renames the highlighted session, not
// the one at the raw cursor index).
func (m *Model) commitRename() {
	if !m.renaming {
		return
	}
	sel := m.selectedSession()
	if sel == nil {
		m.renaming = false
		m.renameBuf = ""
		return
	}
	title := strings.TrimSpace(m.renameBuf)
	if title == "" {
		// Empty rename is a no-op (matches the CLI, which rejects empty names).
		// The index Save merge treats "" as "keep the existing title", so
		// persisting an empty rename would clear it in the live UI yet resurrect
		// the old title on the next load — a live/persisted divergence. Cancel.
		m.renaming = false
		m.renameBuf = ""
		return
	}
	for i := range m.sessions {
		if m.sessions[i].ID() == sel.ID() {
			m.sessions[i].RenamedTitle = title
			// Persist so the rename survives restart / reattach (T5). nil store
			// (unit tests) keeps it in-memory only.
			if m.titleStore != nil {
				m.titleStore.SaveTitle(m.sessions[i].ID(), title)
			}
			break
		}
	}
	m.renaming = false
	m.renameBuf = ""
}

// visibleRows returns the display rows for the session list — the ONE ordered row
// slice the cursor indexes (§2a). In group view it includes repo headers; in flat
// view it is one session row per visibleSessions() entry. Group view on/off changes
// only this builder, never how consumers walk the result.
func (m *Model) visibleRows() []listRow {
	if !m.groupView.open {
		visible := m.visibleSessions()
		rows := make([]listRow, len(visible))
		for i := range visible {
			s := visible[i]
			rows[i] = sessionRow(&s)
		}
		return rows
	}
	return m.groupedSessions()
}

// sessionAt returns the session that the row at index i selects, skipping group
// headers: a header row resolves to the nearest session below it, then above. It
// is the single accessor render, navigation, and actions share (§2a); out-of-range
// indices (and a list of only headers) yield nil.
func (m *Model) sessionAt(i int) *Session {
	rows := m.visibleRows()
	if i < 0 || i >= len(rows) {
		return nil
	}
	if rows[i].kind == rowSession {
		return rows[i].session
	}
	// The cursor is on a header: look down for the next session row, then up.
	for j := i + 1; j < len(rows); j++ {
		if rows[j].kind == rowSession {
			return rows[j].session
		}
	}
	for j := i - 1; j >= 0; j-- {
		if rows[j].kind == rowSession {
			return rows[j].session
		}
	}
	return nil
}

// renderGroupHeader renders a repo header row.
func (m *Model) renderGroupHeader(repo string, width int) string {
	expanded := m.groupView.repos[repo]
	caret := "▸"
	if expanded {
		caret = "▾"
	}
	label := fmt.Sprintf("%s %s", caret, repo)
	// For collapsed groups, show an attention count so off-screen children still
	// signal (D4 group rollup, design-system-and-states §3.3).
	if !expanded {
		// Count from visibleSessions() so the badge respects the active filter —
		// it must not advertise attention in sessions the filter hid.
		var groupSessions []Session
		for _, s := range m.visibleSessions() {
			if repoKey(s) == repo {
				groupSessions = append(groupSessions, s)
			}
		}
		if n := groupAttentionCount(groupSessions); n > 0 {
			badge := lipgloss.NewStyle().Foreground(theme.Gold).Bold(true).Render(
				fmt.Sprintf(" (%d needs attention)", n),
			)
			label += badge
		}
	}
	return lipgloss.NewStyle().Foreground(theme.TextSecondary).Bold(true).Width(width).Render(label)
}

// renderRenameOverlay renders the rename input overlay.
func (m *Model) renderRenameOverlay(w int) string {
	prompt := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("rename: ")
	input := lipgloss.NewStyle().Foreground(theme.TextBright).Render(m.renameBuf)
	cursor := lipgloss.NewStyle().Foreground(theme.Charple).Render("█")
	line := prompt + input + cursor
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Malibu).
		Background(theme.Surface).
		Padding(0, 1).
		Render(line)
}
