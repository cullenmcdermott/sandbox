package dashboard

// KeyMap holds the dashboard's key bindings as bubbles key.Binding values, so
// the footer (help.ShortHelpView) and the `?` overlay (help.FullHelpView)
// render from this single source and can never drift from the real handlers.
// KeyMap implements the bubbles help.KeyMap interface.

import (
	"charm.land/bubbles/v2/key"
)

// KeyMap is the complete keybinding set for the dashboard.
type KeyMap struct {
	// Navigation
	Up     key.Binding
	Down   key.Binding
	Top    key.Binding
	Bottom key.Binding
	Attach key.Binding
	Detach key.Binding

	// Filter & sort
	Filter          key.Binding
	SortCycle       key.Binding
	SortFlip        key.Binding
	AttentionToggle key.Binding

	// Session actions
	New     key.Binding
	Suspend key.Binding
	Resume  key.Binding
	Approve key.Binding
	Deny    key.Binding
	Destroy key.Binding

	// Global
	Help     key.Binding
	Quit     key.Binding
	Command  key.Binding
	Switcher key.Binding

	// Session organization
	GroupToggle key.Binding
	Rename      key.Binding
	Archive     key.Binding
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
		Filter:          key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		SortCycle:       key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort key")),
		SortFlip:        key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "sort dir")),
		AttentionToggle: key.NewBinding(key.WithKeys("\\"), key.WithHelp("\\", "attention first")),
		New:             key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Suspend:         key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "suspend")),
		Resume:          key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "resume")),
		Approve:         key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "approve")),
		Deny:            key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "deny")),
		Destroy:         key.NewBinding(key.WithKeys("!"), key.WithHelp("!", "destroy")),
		Help:            key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:            key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Command:         key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "command"), key.WithDisabled()),
		Switcher:        key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("^k", "quick switch")),
		GroupToggle:     key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "group view")),
		Rename:          key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "rename")),
		Archive:         key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "archive")),
	}
}

// ShortHelp returns the footer bindings, in display order. Implements
// help.KeyMap.
func (km KeyMap) ShortHelp() []key.Binding {
	// Keep the footer to the handful of actions a user reaches for at a glance
	// (T4). Suspend / destroy / sort live in the `?` full-help overlay — they
	// cluttered the band and the destructive ones don't belong in muscle memory.
	return []key.Binding{
		km.Up, km.Down, km.Attach, km.Filter, km.New, km.Help, km.Quit,
	}
}

// FullHelp returns the grouped bindings for the `?` overlay (one inner slice
// per column). Implements help.KeyMap.
func (km KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{km.Up, km.Down, km.Top, km.Bottom, km.Attach},
		{km.Filter, km.SortCycle, km.SortFlip, km.AttentionToggle},
		{km.New, km.Suspend, km.Resume, km.Approve, km.Deny, km.Destroy},
		{km.Help, km.Switcher, km.Quit},
	}
}
