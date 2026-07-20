package dashboard

// KeyMap holds the dashboard's key bindings as bubbles key.Binding values so the
// `?` overlay (help.FullHelpView, via FullHelp) renders from this single source
// and can never drift from the real handlers. The footer no longer renders from
// here: it is derived from the dctxList binding table itself (Model.shortHelp →
// footerBindings), so its advertising can never lie about the live keys (§2d).

import (
	"charm.land/bubbles/v2/key"
)

// KeyMap is the complete keybinding set for the dashboard.
type KeyMap struct {
	// Navigation
	Up            key.Binding
	Down          key.Binding
	Top           key.Binding
	Bottom        key.Binding
	Attach        key.Binding
	Detach        key.Binding
	NextAttention key.Binding

	// Filter & sort
	Filter          key.Binding
	SortCycle       key.Binding
	SortFlip        key.Binding
	AttentionToggle key.Binding

	// Session actions
	New     key.Binding
	Suspend key.Binding
	Resume  key.Binding
	Destroy key.Binding

	// Global
	Help     key.Binding
	Quit     key.Binding
	Command  key.Binding
	Switcher key.Binding

	// Session organization
	GroupToggle key.Binding
	Rename      key.Binding
	Branch      key.Binding
}

// DefaultKeyMap returns the canonical keybinding set.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:              key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("↑/k", "up")),
		Down:            key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("↓/j", "down")),
		Top:             key.NewBinding(key.WithKeys("g"), key.WithHelp("gg", "top")),
		Bottom:          key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
		Attach:          key.NewBinding(key.WithKeys("enter", "o"), key.WithHelp("↵/o", "attach")),
		Detach:          key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "detach / clear filter")),
		NextAttention:   key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("^g", "next attention")),
		Filter:          key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		SortCycle:       key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort key")),
		SortFlip:        key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "sort dir")),
		AttentionToggle: key.NewBinding(key.WithKeys("\\"), key.WithHelp("\\", "attention first")),
		New:             key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Suspend:         key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "suspend")),
		Resume:          key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "resume")),
		Destroy:         key.NewBinding(key.WithKeys("!"), key.WithHelp("!", "destroy")),
		Help:            key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:            key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Command:         key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "command"), key.WithDisabled()),
		Switcher:        key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("^k", "quick switch")),
		GroupToggle:     key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "group view · gg top")),
		Rename:          key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "rename")),
		Branch:          key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "branch")),
	}
}

// FullHelp returns the grouped bindings for the `?` overlay (one inner slice
// per column). It is the `?` overlay's source (via keymapCategories); the footer
// derives from the dctxList table instead (Model.shortHelp).
func (km KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{km.Up, km.Down, km.Top, km.Bottom, km.Attach, km.Detach, km.NextAttention},
		{km.Filter, km.SortCycle, km.SortFlip, km.AttentionToggle, km.GroupToggle},
		{km.New, km.Suspend, km.Resume, km.Destroy},
		{km.Rename, km.Branch, km.Help, km.Switcher, km.Quit},
	}
}
