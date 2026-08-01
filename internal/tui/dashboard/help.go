package dashboard

// help.go — the shared grouped, expandable help/command surface used by `?`
// (overlay) and `/help` on both the command center and the chat (Phase 3,
// Mockup B variant 2). Categories are built from keymap.go and the slash-command
// registry, so the surface can never drift from the real bindings/commands. It
// is keyboard-first (S16): ↑/↓ move the selected category, space/enter expand or
// collapse it.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// helpEntry is one row: a key chord (or command) and its description.
type helpEntry struct {
	key  string
	desc string
}

// helpCategory is an expandable group of entries.
type helpCategory struct {
	name    string
	entries []helpEntry
}

// helpModel is the keyboard-driven grouped help surface shared by both screens.
type helpModel struct {
	title      string
	categories []helpCategory
	expanded   []bool
	sel        int
}

func newHelpModel(title string, cats []helpCategory) helpModel {
	exp := make([]bool, len(cats))
	for i := range exp {
		exp[i] = true // start expanded so content is visible immediately
	}
	return helpModel{title: title, categories: cats, expanded: exp}
}

// handleKey processes navigation/expansion. It returns true when it consumed the
// key (caller keeps the overlay open); false means the caller should close it.
func (h *helpModel) handleKey(key string) bool {
	switch key {
	case "up", "k":
		if h.sel > 0 {
			h.sel--
		}
		return true
	case "down", "j":
		if h.sel < len(h.categories)-1 {
			h.sel++
		}
		return true
	case " ", "space", "enter":
		if h.sel >= 0 && h.sel < len(h.expanded) {
			h.expanded[h.sel] = !h.expanded[h.sel]
		}
		return true
	}
	return false
}

// view renders the bordered overlay: title, each category header (caret + name +
// count), and the entries of expanded categories.
func (h helpModel) view(width int) string {
	boxW := width - 8
	if boxW < 30 {
		boxW = 30
	} else if boxW > 64 {
		boxW = 64
	}
	keyW := 0
	for _, cat := range h.categories {
		for _, e := range cat.entries {
			if w := lipgloss.Width(e.key); w > keyW {
				keyW = w
			}
		}
	}
	if keyW > 16 {
		keyW = 16
	}

	lines := []string{lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(h.title), ""}
	for i, cat := range h.categories {
		caret := "▸"
		if h.expanded[i] {
			caret = "▾"
		}
		headStyle := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true)
		if i == h.sel {
			headStyle = headStyle.Background(theme.Raised2)
		}
		head := headStyle.Render(fmt.Sprintf("%s %s", caret, strings.ToUpper(cat.name))) +
			lipgloss.NewStyle().Foreground(theme.TextDim).Render(fmt.Sprintf("  (%d)", len(cat.entries)))
		lines = append(lines, head)
		if h.expanded[i] {
			for _, e := range cat.entries {
				lines = append(lines, "  "+kit.Kbd(padTo(e.key, keyW), e.desc))
			}
		}
	}
	lines = append(lines, "", kit.KbdRow(
		[2]string{"↑/↓", "select"},
		[2]string{"space", "expand/collapse"},
		[2]string{"any", "close"},
	))

	return styleHelp.Width(boxW).Render(strings.Join(lines, "\n"))
}

// keymapCategories builds help categories from the keymap's FullHelp groups
// (skipping disabled bindings) so the help never drifts from the handlers.
func keymapCategories(km KeyMap) []helpCategory {
	names := []string{"Navigate", "Filter & Sort", "Actions", "Global"}
	var cats []helpCategory
	for i, g := range km.FullHelp() {
		var entries []helpEntry
		for _, b := range g {
			if !b.Enabled() {
				continue
			}
			hb := b.Help()
			entries = append(entries, helpEntry{key: hb.Key, desc: hb.Desc})
		}
		if len(entries) == 0 {
			continue
		}
		name := "More"
		if i < len(names) {
			name = names[i]
		}
		cats = append(cats, helpCategory{name: name, entries: entries})
	}
	// Mouse behavior is the one thing here that is NOT a keymap binding, so it
	// cannot be derived from FullHelp like everything above — it is the terminal's
	// own handling, which changes depending on whether the current screen captures
	// the mouse (see App.View). It still belongs in the help, because "how do I
	// select text in the agent pane" is otherwise unanswerable from inside the UI.
	cats = append(cats, helpCategory{name: "Mouse", entries: []helpEntry{
		{key: "drag", desc: "select text (list, feed, pickers)"},
		{key: "^] s", desc: "agent pane: release the mouse to select/copy (toggle)"},
		{key: "shift+drag", desc: "select in the pane without toggling (terminal bypass)"},
		{key: "wheel", desc: "scroll pane scrollback; move selection in the list"},
	}})
	return cats
}
