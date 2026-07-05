package sdktest

// tui_surface_test.go — compile-time pins of the public, importable tui/ SDK
// packages (tui/kit, tui/anim, tui/list, tui/theme, tui/terminal). These are
// documented (CLAUDE.md) as reusable building blocks other projects import
// directly, so a breaking rename/signature change must fail HERE — the same
// consumer-visible-changelog discipline as surface_test.go. Scope is
// load-bearing exports, not every helper; when a break is intentional, update
// the pin in the same change and call it out.

import (
	"image/color"
	"time"

	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// --- tui/anim: transitions, engine, spinner, color lerp --------------------

var (
	_ func(float64) float64                               = anim.EaseOutCubic
	_ func(time.Duration, time.Duration) float64          = anim.Progress
	_ func(color.Color, color.Color, float64) color.Color = anim.LerpColor
	_ func() bool                                         = anim.ReduceMotion
	_ func(int) string                                    = anim.Ellipsis
	_ func() *anim.Engine                                 = anim.NewEngine
	_ func() *anim.Spinner                                = anim.NewSpinner

	// Method expressions pin receiver + signature together.
	_ func(anim.Transition, time.Duration) float64 = anim.Transition.At
	_ func(*anim.Engine, time.Time) bool           = (*anim.Engine).AnyMotionActive
	_ func(*anim.Engine, time.Time)                = (*anim.Engine).StartTransition
	_ func(*anim.Engine, int)                      = (*anim.Engine).SetSpinners
	_ func(*anim.Spinner, bool) string             = (*anim.Spinner).Frame
)

var _ = anim.Transition{Total: time.Second}

// --- tui/kit: palette + widgets + formatters --------------------------------

var (
	_ func(kit.ComponentColors)       = kit.SetComponentColors
	_ func(int) string                = kit.FormatTokens
	_ func(float64) string            = kit.FormatCost
	_ func(int, int, int, int) string = kit.Scrollbar
	_ func(color.Color) color.Color   = kit.OnColor
	_ func(string, kit.Role) string   = kit.Badge
	_ func(string, bool) string       = kit.Button
	_ func(string, string) string     = kit.Kbd
)

var (
	_ kit.Role = kit.RoleBrand
	_          = kit.ComponentColors{}
)

// --- tui/list: the Item contract + list ops --------------------------------

var (
	_ func(...list.Item) *list.List  = list.New
	_ func(*list.List, ...list.Item) = (*list.List).SetItems
	_ func(*list.List, ...list.Item) = (*list.List).AppendItems
	_ func(*list.List) string        = (*list.List).Render
	_ func(*list.List, int, int)     = (*list.List).SetSize
	_ func(*list.List, int)          = (*list.List).ScrollBy
	_ func(*list.List) bool          = (*list.List).AtBottom
)

// consumerListItem proves list.Item stays implementable by outside consumers:
// WIDENING it (adding a method) is a breaking change and must fail here. NOTE:
// Item.Finished() is flagged for removal in TODO.md §8 — if it is dropped, this
// stub AND the interface change together, which is exactly the signal this pin
// exists to force.
type consumerListItem struct{ list.Versioned }

func (consumerListItem) Render(width int) string { return "" }
func (consumerListItem) Finished() bool          { return true }

// Pointer receiver: Version() is promoted from *list.Versioned, so the pin uses
// a pointer (the natural way a consumer embeds the counter).
var _ list.Item = &consumerListItem{}

// --- tui/theme: apply/epoch/tokens + text helpers ---------------------------

var (
	_ func(theme.Theme)                         = theme.ApplyTheme
	_ func() uint64                             = theme.Epoch
	_ func(func()) func()                       = theme.OnChange
	_ func(string, bool, ...color.Color) string = theme.GradientText
	_ func(color.Color, time.Time) color.Color  = theme.FadeColor
	_ func(int) string                          = theme.SpinnerFrame
)

// A representative slice of the exported active color tokens (the semantic
// palette other projects read); renaming any is a break.
var (
	_ color.Color = theme.Charple
	_ color.Color = theme.Gold
	_ color.Color = theme.Coral
	_ color.Color = theme.Page
	_ color.Color = theme.Surface
	_ color.Color = theme.TextBright
	_ color.Color = theme.TextSecondary
	_ color.Color = theme.BorderMedium
)

var _ = theme.Theme{}

// --- tui/terminal: OSC progress/notify, caps detection, kitty graphics ------

var (
	_ func(terminal.Progress) string                             = terminal.OSCProgress
	_ func(string, string) string                                = terminal.OSCNotify
	_ func(terminal.Caps, string, string) string                 = terminal.NotifyString
	_ func() terminal.Caps                                       = terminal.Detect
	_ func(float64, int, int, terminal.RGB, terminal.RGB) []byte = terminal.GaugeRGBA
	_ func(uint32, int, int, int, int, []byte) string            = terminal.KittyTransmitRGBA
	_ func(uint32, int, int) string                              = terminal.KittyPlaceholders
)

var (
	_ terminal.Progress = terminal.ProgressNone
	_                   = terminal.Caps{}
	_                   = terminal.RGB{}
)
