package dashboard

// search.go — in-transcript search / jump + unread marker (slice 5i / design
// S14, S12). ctrl+f opens a search overlay; enter / ctrl+n jump to the next
// match and ctrl+p to the previous (n/N are reserved for typing into the
// query, so navigation uses modified keys — NEW-1); space on an empty prompt
// collapses all tool/subagent cards (ctrl+c is the global quit); an unread
// divider appears on reattach at the last seen turn boundary.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// searchModel holds the in-transcript search state.
type searchModel struct {
	open       bool
	query      string
	matches    [][2]int // [blockIndex, runeOffset] pairs
	matchIndex int      // current match
	jumped     bool     // a jump happened for this query (first enter lands on match 1, not 2)
}

// openSearch initializes transcript search. It re-runs layout so the viewport
// shrinks by the one row the search bar occupies (T3).
func (m *TranscriptModel) openSearch() {
	m.search = searchModel{open: true}
	m.layout()
}

// closeSearch hides the search overlay and gives the row back to the viewport.
func (m *TranscriptModel) closeSearch() {
	m.search = searchModel{}
	m.layout()
}

// searchKey handles keys while the search overlay is open.
func (m *TranscriptModel) searchKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	k := msg.String()
	switch k {
	case "esc", "ctrl+f":
		m.closeSearch()
		return nil, true
	case "enter", "ctrl+n":
		// First jump for a query lands on the CURRENT (first) match; advancing
		// immediately would skip it (matchIndex starts at 0 == first match).
		if !m.search.jumped && len(m.search.matches) > 0 {
			m.search.jumped = true
			m.scrollToMatch()
			return nil, true
		}
		m.nextSearchMatch()
		return nil, true
	case "ctrl+p":
		m.search.jumped = true
		m.prevSearchMatch()
		return nil, true
	case "backspace":
		// Rune-wise, not byte-wise: chopping one byte off a multibyte tail (é →
		// dangling 0xC3) corrupts the query into U+FFFD and poisons fuzzy matching.
		// Mirrors the filter/rename buffers (model.go:2036/2076).
		if r, size := utf8.DecodeLastRuneInString(m.search.query); r != utf8.RuneError {
			m.search.query = m.search.query[:len(m.search.query)-size]
			m.updateSearchMatches()
		}
		return nil, true
	}
	key := msg.Key()
	// Accept typed text. bubbletea v2's decoder sets ModShift on a plain uppercase
	// letter, so require only that no NON-shift modifier is held — otherwise every
	// capital ("TODO", the T in "Readme") would be silently dropped. Matches the
	// filter/rename inputs' handling.
	if key.Code != 0 && key.Mod&^tea.ModShift == 0 && key.Text != "" {
		m.search.query += key.Text
		m.updateSearchMatches()
		return nil, true
	}
	return nil, false
}

// updateSearchMatches recomputes matches across all blocks using a fuzzy
// (subsequence) match (T3): the query characters must appear in order but not
// contiguously, so "rdme" matches "README". One match is recorded per block (the
// position of the first matched rune), in document order.
func (m *TranscriptModel) updateSearchMatches() {
	m.search.matches = nil
	m.search.matchIndex = 0
	m.search.jumped = false
	q := m.search.query
	if q == "" {
		return
	}
	for i, b := range m.blocks {
		text := blockSearchText(b)
		if off, ok := fuzzyMatchOffset(q, text); ok {
			// Store a RUNE offset (NEW-3): scrollToMatch indexes []rune(text), so a
			// byte offset would mis-place the jump inside any multibyte block.
			m.search.matches = append(m.search.matches, [2]int{i, off})
		}
	}
}

// fuzzyMatchOffset reports whether every rune of query appears in text in order
// (case-insensitive subsequence), and returns the rune offset of the first
// matched rune. An empty query matches at offset 0. (sort.go's fuzzyMatch
// answers a different question — match + score for list filtering — so search
// keeps its own offset-returning variant.)
func fuzzyMatchOffset(query, text string) (offset int, ok bool) {
	q := []rune(strings.ToLower(query))
	if len(q) == 0 {
		return 0, true
	}
	qi := 0
	first := -1
	for ri, r := range []rune(strings.ToLower(text)) {
		if r == q[qi] {
			if first < 0 {
				first = ri
			}
			qi++
			if qi == len(q) {
				return first, true
			}
		}
	}
	return 0, false
}

// blockSearchText returns the searchable text for a block.
func blockSearchText(b tblock) string {
	switch b.kind {
	case blockUser:
		return b.text
	case blockAssistant:
		return stripANSI(b.text)
	case blockToolCard:
		if b.tool != nil {
			return b.tool.tool + " " + b.tool.arg + " " + b.tool.summary
		}
	case blockSubagent:
		if b.sub != nil {
			return b.sub.prompt
		}
	case blockShell:
		return b.text
	case blockError, blockInfo, blockWarn:
		return b.text
	}
	return ""
}

// nextSearchMatch jumps to the next match and scrolls the viewport to it.
func (m *TranscriptModel) nextSearchMatch() {
	if len(m.search.matches) == 0 {
		return
	}
	m.search.matchIndex++
	if m.search.matchIndex >= len(m.search.matches) {
		m.search.matchIndex = 0
	}
	m.scrollToMatch()
}

// prevSearchMatch jumps to the previous match.
func (m *TranscriptModel) prevSearchMatch() {
	if len(m.search.matches) == 0 {
		return
	}
	m.search.matchIndex--
	if m.search.matchIndex < 0 {
		m.search.matchIndex = len(m.search.matches) - 1
	}
	m.scrollToMatch()
}

// scrollToMatch moves the viewport so the current match is visible.
func (m *TranscriptModel) scrollToMatch() {
	if m.search.matchIndex >= len(m.search.matches) {
		return
	}
	match := m.search.matches[m.search.matchIndex]

	// Sum exact rendered heights of all blocks before the match block.
	line := 0
	for i := 0; i < match[0] && i < len(m.blocks); i++ {
		line += m.body.HeightAt(i)
	}

	// Within the matched block, estimate the visual line from the rune offset.
	// This is approximate (styled blocks may not wrap linearly) but better than
	// block-granularity only.
	if match[0] < len(m.blocks) && match[1] > 0 {
		b := m.blocks[match[0]]
		text := blockSearchText(b)
		if text != "" {
			runes := []rune(text)
			if match[1] < len(runes) {
				// Proportion of runes before the match × block height.
				blockH := m.body.HeightAt(match[0])
				if blockH > 0 {
					prop := float64(match[1]) / float64(len(runes))
					line += int(prop * float64(blockH))
				}
			}
		}
	}

	m.body.GotoTop()
	m.body.ScrollBy(line)
	// A search jump is absolute positioning, not a "follow the live tail" intent.
	// Clear follow so a match that happens to land on the last line doesn't re-arm
	// auto-scroll and yank the view to the bottom on the next streamed delta.
	m.body.SetFollow(false)
}

// renderSearchBar renders the bottom search input overlay.
func (m *TranscriptModel) renderSearchBar(w int) string {
	status := ""
	if m.search.query != "" {
		total := len(m.search.matches)
		cur := 0
		if total > 0 {
			cur = m.search.matchIndex + 1
		}
		status = fmt.Sprintf(" %d/%d", cur, total)
	}
	prompt := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("find: ")
	input := lipgloss.NewStyle().Foreground(theme.TextBright).Render(m.search.query)
	meta := lipgloss.NewStyle().Foreground(theme.TextMuted).Render(status+" · ") +
		kit.KbdRow([2]string{"↵/^n", "next"}, [2]string{"^p", "prev"}, [2]string{"esc", "close"})
	line := prompt + input + meta
	return lipgloss.NewStyle().Background(theme.Surface).Width(w).Render(line)
}

// stripANSI removes ANSI escape sequences from s (basic version for search).
func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && ((s[j] >= '0' && s[j] <= '?') || (s[j] >= ' ' && s[j] <= '/')) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
