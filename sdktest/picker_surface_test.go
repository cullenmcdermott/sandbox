package sdktest

// picker_surface_test.go — compile-time pins for the public tui/picker selection
// overlay. Proves an external Bubble Tea app can build the Sandbox
// model/backend/account picker vocabulary (numbered rows, ↑/↓ + digit nav, enter
// choose, esc cancel) from public packages alone, naming no internal/ type.

import (
	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/tui/picker"
)

var (
	_ func(string, []picker.Item, ...picker.Option) *picker.Model = picker.New
	_ func(func(picker.Item)) picker.Option                       = picker.WithChoose
	_ func(func()) picker.Option                                  = picker.WithCancel
)

var (
	_ func(*picker.Model, tea.Msg) (*picker.Model, tea.Cmd) = (*picker.Model).Update
	_ func(*picker.Model, int) string                       = (*picker.Model).View
	_ func(*picker.Model) []picker.Item                     = (*picker.Model).Items
	_ func(*picker.Model, []picker.Item)                    = (*picker.Model).SetItems
	_ func(*picker.Model) int                               = (*picker.Model).Selected
	_ func(*picker.Model) picker.Item                       = (*picker.Model).SelectedItem
	_ func(*picker.Model)                                   = (*picker.Model).MoveUp
	_ func(*picker.Model)                                   = (*picker.Model).MoveDown
)

// Item field-set pin.
var _ = picker.Item{ID: "", Name: "", Desc: "", Current: false}
