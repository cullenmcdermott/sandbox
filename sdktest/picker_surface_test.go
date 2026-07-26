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
	_ func() picker.Option                                        = picker.WithFilter
	_ func(int) picker.Option                                     = picker.WithMaxRows
	_ func(int) picker.Option                                     = picker.WithMaxWidth
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
	// Type-to-filter (WithFilter): the query and the visible subset it selects.
	_ func(*picker.Model) []picker.Item = (*picker.Model).Filtered
	_ func(*picker.Model) string        = (*picker.Model).Query
	_ func(*picker.Model, string)       = (*picker.Model).SetQuery
	// Height budget (WithMaxRows): the row cap a host recomputes on resize.
	_ func(*picker.Model) int  = (*picker.Model).MaxRows
	_ func(*picker.Model, int) = (*picker.Model).SetMaxRows
	// Width budget (WithMaxWidth): the cap a columned host raises.
	_ func(*picker.Model) int  = (*picker.Model).MaxWidth
	_ func(*picker.Model, int) = (*picker.Model).SetMaxWidth
)

// Item field-set pin. Cols makes Item non-comparable (it holds a slice) — an
// external consumer must compare rows by ID, not with ==.
var _ = picker.Item{ID: "", Name: "", Desc: "", Cols: []string{}, Current: false}
