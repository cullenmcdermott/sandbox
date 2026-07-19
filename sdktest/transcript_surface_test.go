package sdktest

// transcript_surface_test.go — compile-time pins for the public tui/transcript
// event-sourced transcript component. Like chat_surface_test.go, these prove an
// external Bubble Tea app can build the polished Sandbox transcript from public
// packages alone: it feeds public client.Event values into transcript.Model and
// renders through tui/chat + tui/list, naming no internal/ type. A breaking
// rename/signature change to the component's surface must fail HERE. The
// event-sourced behavioral conformance (TestTranscriptFromPublicEvents) lands
// with T7; this file pins the surface T3 introduces.

import (
	"time"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/tui/chat"
	"github.com/cullenmcdermott/sandbox/tui/transcript"
)

// --- constructor + options ---------------------------------------------------

var (
	_ func(...transcript.Option) *transcript.Model = transcript.New

	_ func(func() time.Time) transcript.Option       = transcript.WithNow
	_ func(string) transcript.Option                 = transcript.WithBackend
	_ func(bool) transcript.Option                   = transcript.WithMarkdown
	_ func(func(string)) transcript.Option           = transcript.WithSubmit
	_ func(func(id, scope string)) transcript.Option = transcript.WithApprove
	_ func(func(string)) transcript.Option           = transcript.WithDeny
	_ func(func()) transcript.Option                 = transcript.WithInterrupt
	_ func(func(string)) transcript.Option           = transcript.WithSteer
	_ func(func()) transcript.Option                 = transcript.WithDetach
)

// --- the reducer + render/size surface ---------------------------------------

var (
	_ func(*transcript.Model, client.Event) = (*transcript.Model).Apply
	_ func(*transcript.Model, int, int)     = (*transcript.Model).SetSize
	_ func(*transcript.Model) string        = (*transcript.Model).Render
	_ func(*transcript.Model) int           = (*transcript.Model).Width
	_ func(*transcript.Model) int           = (*transcript.Model).Height
	_ func(*transcript.Model) int           = (*transcript.Model).Len
)

// --- scrolling / follow ------------------------------------------------------

var (
	_ func(*transcript.Model, int) = (*transcript.Model).ScrollBy
	_ func(*transcript.Model)      = (*transcript.Model).GotoTop
	_ func(*transcript.Model)      = (*transcript.Model).GotoBottom
	_ func(*transcript.Model) bool = (*transcript.Model).AtBottom
	_ func(*transcript.Model) bool = (*transcript.Model).Following
)

// --- focus / expansion -------------------------------------------------------

var (
	_ func(*transcript.Model)      = (*transcript.Model).FocusNext
	_ func(*transcript.Model)      = (*transcript.Model).FocusPrev
	_ func(*transcript.Model)      = (*transcript.Model).ClearFocus
	_ func(*transcript.Model) bool = (*transcript.Model).ToggleExpand
)

// --- host actions + replay ---------------------------------------------------

var (
	_ func(*transcript.Model, string)          = (*transcript.Model).Submit
	_ func(*transcript.Model, string)          = (*transcript.Model).Approve
	_ func(*transcript.Model)                  = (*transcript.Model).Deny
	_ func(*transcript.Model)                  = (*transcript.Model).Interrupt
	_ func(*transcript.Model, string)          = (*transcript.Model).Steer
	_ func(*transcript.Model)                  = (*transcript.Model).Detach
	_ func(*transcript.Model) *chat.Permission = (*transcript.Model).PendingPermission
	_ func(*transcript.Model, uint64)          = (*transcript.Model).BeginReplay
	_ func(*transcript.Model) bool             = (*transcript.Model).Replaying
	_ func(*transcript.Model)                  = (*transcript.Model).Close
)
