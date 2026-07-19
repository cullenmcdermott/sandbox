package sdktest

// composer_surface_test.go — compile-time pins for the public tui/composer chat
// input component. Proves an external Bubble Tea app can build the Sandbox
// composer (multi-line input, queue-while-busy steering, escape cascade,
// grace-gated permission answering) from public packages alone, naming no
// internal/ type. A breaking rename/signature change fails HERE.

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/tui/composer"
)

// --- constructor + options ---------------------------------------------------

var (
	_ func(...composer.Option) *composer.Model = composer.New

	_ func(func() time.Time) composer.Option = composer.WithNow
	_ func(int) composer.Option              = composer.WithMaxRows
	_ func(string) composer.Option           = composer.WithPlaceholder
	_ func(func(string)) composer.Option     = composer.WithSubmit
	_ func(func(string)) composer.Option     = composer.WithSteer
	_ func(func()) composer.Option           = composer.WithInterrupt
	_ func(func(string)) composer.Option     = composer.WithApprove
	_ func(func()) composer.Option           = composer.WithDeny
	_ func(func()) composer.Option           = composer.WithDetach
)

// --- the Bubble Tea component surface ----------------------------------------

var (
	_ func(*composer.Model) tea.Cmd                             = (*composer.Model).Init
	_ func(*composer.Model, tea.Msg) (*composer.Model, tea.Cmd) = (*composer.Model).Update
	_ func(*composer.Model) string                              = (*composer.Model).View
	_ func(*composer.Model) tea.Cmd                             = (*composer.Model).Focus
	_ func(*composer.Model)                                     = (*composer.Model).Blur
	_ func(*composer.Model) bool                                = (*composer.Model).Focused
)

// --- draft / state / sizing --------------------------------------------------

var (
	_ func(*composer.Model) string          = (*composer.Model).Value
	_ func(*composer.Model, string)         = (*composer.Model).SetValue
	_ func(*composer.Model)                 = (*composer.Model).Reset
	_ func(*composer.Model) composer.State  = (*composer.Model).State
	_ func(*composer.Model, composer.State) = (*composer.Model).SetState
	_ func(*composer.Model) bool            = (*composer.Model).Queued
	_ func(*composer.Model, bool)           = (*composer.Model).SetPermissionPending
	_ func(*composer.Model, int)            = (*composer.Model).SetWidth
	_ func(*composer.Model) int             = (*composer.Model).Width
	_ func(*composer.Model) int             = (*composer.Model).Height
)

// --- state enum --------------------------------------------------------------

var (
	_ composer.State = composer.StateReady
	_ composer.State = composer.StateBusy
	_ composer.State = composer.StateDisabled
)
