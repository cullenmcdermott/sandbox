package sdktest

// chat_surface_test.go — compile-time pins + a render conformance test for the
// public tui/chat transcript-item vocabulary. Like tui_surface_test.go, these
// prove an external Bubble Tea app can build the polished Sandbox transcript from
// public packages alone (tui/chat + tui/list + tui/theme) without naming any
// internal/ type. A breaking rename/signature change must fail HERE, and the
// render test proves the items actually compose into a width-safe transcript at
// the terminal sizes the importability plan targets.
//
// Scope is the transcript ITEM layer (the public surface that exists today). The
// interactive host callbacks the importability plan also calls for — prompt
// submission, approval decisions, interruption, steering, detach — belong to the
// not-yet-public tui/transcript + tui/composer components; the item layer models
// their transcript-visible artifacts (a queued/committed UserItem, an elbow
// notice, an error notice), which this test exercises.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/chat"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// --- tui/chat: constructors ------------------------------------------------

var (
	_ func(*chat.AssistantMessage) *chat.AssistantItem = chat.NewAssistantItem
	_ func(string) *chat.UserItem                      = chat.NewUserItem
	_ func(*chat.ToolCall) *chat.ToolItem              = chat.NewToolItem
	_ func(*chat.Subagent) *chat.SubagentItem          = chat.NewSubagentItem
	_ func(chat.NoticeKind, string) *chat.NoticeItem   = chat.NewNoticeItem
	_ func(string) *chat.ShellItem                     = chat.NewShellItem
	_ func(string) *chat.ShellItem                     = chat.NewElbowNotice
	_ func(string, bool) *chat.ReasoningItem           = chat.NewReasoningItem
	_ func([]chat.Todo) *chat.TodosItem                = chat.NewTodosItem
	_ func(*chat.Permission) *chat.PermissionItem      = chat.NewPermissionItem
	_ func(*chat.TurnFooter) *chat.FooterItem          = chat.NewFooterItem
)

// --- tui/chat: free functions ----------------------------------------------

var (
	_ func(string) string                  = chat.Bullet
	_ func(string) string                  = chat.Quote
	_ func([]chat.Citation, int) string    = chat.RenderCitations
	_ func(string, json.RawMessage) string = chat.ToolArg
	_ func(string) string                  = chat.ToolSummary
	_ func(*list.List) func()              = chat.OnThemeChange
	_ func(*list.List)                     = (*list.List).InvalidateAll
	// MarkdownRenderer/InvalidateRenderers + the Renderer interface are the
	// streaming-markdown seam the dashboard injects; pin existence (the concrete
	// return type is glamour's, deliberately not named here).
	_ = chat.MarkdownRenderer
	_ = chat.InvalidateRenderers
	_ chat.Renderer
)

// --- tui/chat: every item satisfies list.Item ------------------------------

var (
	_ list.Item = (*chat.AssistantItem)(nil)
	_ list.Item = (*chat.UserItem)(nil)
	_ list.Item = (*chat.ToolItem)(nil)
	_ list.Item = (*chat.SubagentItem)(nil)
	_ list.Item = (*chat.NoticeItem)(nil)
	_ list.Item = (*chat.ShellItem)(nil)
	_ list.Item = (*chat.ReasoningItem)(nil)
	_ list.Item = (*chat.TodosItem)(nil)
	_ list.Item = (*chat.PermissionItem)(nil)
	_ list.Item = (*chat.FooterItem)(nil)
)

// --- tui/chat: load-bearing methods (receiver + signature) -----------------

var (
	// ToolItem streaming/expansion mutation surface.
	_ func(*chat.ToolItem, chat.ToolStatus, string) = (*chat.ToolItem).SetStatus
	_ func(*chat.ToolItem, string)                  = (*chat.ToolItem).SetArg
	_ func(*chat.ToolItem, time.Duration)           = (*chat.ToolItem).SetElapsed
	_ func(*chat.ToolItem, int)                     = (*chat.ToolItem).SetExitCode
	_ func(*chat.ToolItem, string)                  = (*chat.ToolItem).SetOutput
	_ func(*chat.ToolItem, string)                  = (*chat.ToolItem).AppendOutput
	_ func(*chat.ToolItem, []string)                = (*chat.ToolItem).SetDiff
	_ func(*chat.ToolItem, bool)                    = (*chat.ToolItem).SetExpanded
	_ func(*chat.ToolItem, bool)                    = (*chat.ToolItem).SetFocused
	_ func(*chat.ToolItem, int) bool                = (*chat.ToolItem).Expandable
	_ func(*chat.ToolItem, int) string              = (*chat.ToolItem).Render

	// SubagentItem tree/collapse surface.
	_ func(*chat.SubagentItem, *chat.ToolCall)  = (*chat.SubagentItem).AddChild
	_ func(*chat.SubagentItem, chat.ToolStatus) = (*chat.SubagentItem).SetStatus
	_ func(*chat.SubagentItem, string)          = (*chat.SubagentItem).SetNarration
	_ func(*chat.SubagentItem, bool)            = (*chat.SubagentItem).SetCollapsed
	_ func(*chat.SubagentItem, int)             = (*chat.SubagentItem).SetSpinnerFrame

	// Focus + text mutation across the message-ish items.
	_ func(*chat.UserItem, bool)         = (*chat.UserItem).SetFocused
	_ func(*chat.UserItem, string)       = (*chat.UserItem).SetText
	_ func(*chat.ReasoningItem, bool)    = (*chat.ReasoningItem).SetExpanded
	_ func(*chat.ReasoningItem, bool)    = (*chat.ReasoningItem).SetStreaming
	_ func(*chat.TodosItem, []chat.Todo) = (*chat.TodosItem).SetTodos
	_ func(*chat.NoticeItem, string)     = (*chat.NoticeItem).SetText

	// FooterItem per-turn outcome surface.
	_ func(*chat.FooterItem, *chat.TurnFooter) = (*chat.FooterItem).SetFooter
	_ func(*chat.FooterItem, bool)             = (*chat.FooterItem).SetFocused

	// AssistantItem dependency-injection surface the dashboard dogfoods.
	_ func(*chat.AssistantItem, func(string, int) string) = (*chat.AssistantItem).SetRenderContentMD
	_ func(*chat.AssistantItem, int) string               = (*chat.AssistantItem).RawRender
)

// --- tui/chat: enum + struct field pins ------------------------------------

var (
	_ chat.ToolStatus = chat.ToolRunning
	_ chat.ToolStatus = chat.ToolOK
	_ chat.ToolStatus = chat.ToolError
	_ chat.NoticeKind = chat.NoticeInfo
	_ chat.NoticeKind = chat.NoticeWarn
	_ chat.NoticeKind = chat.NoticeError
	_ chat.TodoStatus = chat.TodoPending
	_ chat.TodoStatus = chat.TodoInProgress
	_ chat.TodoStatus = chat.TodoCompleted
)

// Struct literals pin the exported field set: dropping/renaming a field breaks
// here (a consumer builds these to feed a streaming reducer).
var (
	_ = chat.ToolCall{ID: "", Name: "", Arg: "", Status: chat.ToolOK, Summary: "", Output: "", Diff: nil, ExitCode: nil, Elapsed: 0}
	_ = chat.Subagent{ID: "", AgentName: "", Prompt: "", Children: nil, Status: chat.ToolOK, Narration: "", Elapsed: 0}
	_ = chat.Todo{Content: "", ActiveForm: "", Status: chat.TodoPending}
	_ = chat.Citation{Title: "", URL: "", CitedText: ""}
	_ = chat.AssistantMessage{ID: "", Content: "", Thinking: "", Errored: false, ErrText: "", Streaming: false, Finished: false}
	_ = chat.Permission{ID: "", Tool: "", Arg: "", Diff: nil, IsPlan: false, Plan: ""}
	_ = chat.TurnFooter{Model: "", Backend: "", Elapsed: 0, InputTokens: 0, OutputTokens: 0, CostUSD: 0}
)

// --- render conformance -----------------------------------------------------

// consumerWidthSafe asserts every line of a frame fits the declared width — the
// invariant an external host relies on when it drops these items into any layout.
func consumerWidthSafe(t *testing.T, frame string, width int) {
	t.Helper()
	for i, l := range strings.Split(frame, "\n") {
		if w := lipgloss.Width(l); w > width {
			t.Errorf("frame line %d overflows width %d (%d cols): %q", i, width, w, l)
		}
	}
}

// buildTranscript assembles a representative transcript from public chat items,
// deriving tool args/summaries via the public helpers the way a reducer would.
func buildTranscript() ([]list.Item, *chat.ToolItem) {
	ec := 0
	toolInput := json.RawMessage(`{"command":"go test ./..."}`)
	tool := chat.NewToolItem(&chat.ToolCall{
		ID:       "b1",
		Name:     "Bash",
		Arg:      chat.ToolArg("Bash", toolInput),
		Status:   chat.ToolOK,
		Summary:  chat.ToolSummary("l1\nl2\nl3"),
		Output:   "l1\nl2\nl3",
		ExitCode: &ec,
	})
	sub := chat.NewSubagentItem(&chat.Subagent{
		ID: "t1", AgentName: "Explore", Prompt: "find the flake", Status: chat.ToolRunning,
		Children:  []*chat.ToolCall{{Name: "Grep", Arg: "flake", Status: chat.ToolOK, Summary: "7 matches"}},
		Narration: "the flake is in the reconnect path",
	})
	items := []list.Item{
		chat.NewUserItem("run the tests and find the flake"),
		chat.NewReasoningItem("Let me run the suite first.\nThen bisect.", false),
		tool,
		sub,
		chat.NewTodosItem([]chat.Todo{
			{Content: "Run tests", Status: chat.TodoCompleted},
			{Content: "Fix flake", ActiveForm: "Fixing the flake", Status: chat.TodoInProgress},
		}),
		chat.NewNoticeItem(chat.NoticeInfo, "context compacted"),
		chat.NewFooterItem(&chat.TurnFooter{Model: "Opus 4.8", Backend: "anthropic", Elapsed: 12 * time.Second, InputTokens: 3100, OutputTokens: 820, CostUSD: 0.04}),
	}
	return items, tool
}

// TestChatTranscriptRenderConformance proves the public surface renders a real
// transcript and survives the interactions a host drives: multi-size render,
// scroll off the bottom + follow-mode recovery, arbitrary tool expansion, resize,
// and a theme swap — all width-safe, all from public packages only.
func TestChatTranscriptRenderConformance(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	items, tool := buildTranscript()
	l := list.New(items...)

	sizes := [][2]int{{80, 24}, {100, 30}, {140, 40}}
	for _, s := range sizes {
		l.SetSize(s[0], s[1])
		l.GotoBottom()
		frame := l.Render()
		if strings.TrimSpace(frame) == "" {
			t.Fatalf("empty frame at %dx%d", s[0], s[1])
		}
		consumerWidthSafe(t, frame, s[0])
	}

	// Follow-mode escape + recovery: scroll up leaves the bottom, GotoBottom
	// re-pins.
	l.SetSize(80, 12)
	l.GotoBottom()
	if !l.AtBottom() {
		t.Error("GotoBottom did not pin to the bottom")
	}
	l.ScrollBy(-3)
	if l.Following() {
		t.Error("scrolling up should leave follow mode")
	}
	l.GotoBottom()
	if !l.Following() || !l.AtBottom() {
		t.Error("GotoBottom did not recover follow mode")
	}

	// Arbitrary tool expansion changes the frame and stays width-safe.
	before := l.Render()
	tool.SetExpanded(true)
	after := l.Render()
	if before == after {
		t.Error("tool expansion did not change the frame")
	}
	consumerWidthSafe(t, after, 80)

	// "Send" a follow-up: append a user + assistant turn, still following.
	l.AppendItems(chat.NewUserItem("now write it up"))
	if !l.AtBottom() {
		t.Error("append while following did not stay at the bottom")
	}

	// Interruption / detach artifacts (host callbacks live in tui/transcript;
	// here we render their transcript-visible notices).
	l.AppendItems(chat.NewElbowNotice("interrupted"))
	l.AppendItems(chat.NewNoticeItem(chat.NoticeError, "connection reset — detached"))
	consumerWidthSafe(t, l.Render(), 80)

	// Theme swap: wiring OnThemeChange must re-skin the committed transcript (real
	// ANSI changes), keep structure identical, and stay width-safe. Comparing the
	// raw frames (not stripped) is deliberate — a stripped compare can't see stale
	// colors.
	chat.OnThemeChange(l)
	darkFrame := l.Render()
	theme.ApplyForBackground(false) // fires the hook → l.InvalidateAll()
	lightFrame := l.Render()
	consumerWidthSafe(t, lightFrame, 80)
	if lightFrame == darkFrame {
		t.Error("theme swap did not re-skin the transcript (stale palette re-served)")
	}
	if ansiStrip(lightFrame) != ansiStrip(darkFrame) {
		t.Error("theme swap changed transcript structure (should be color-only)")
	}
}

// ansiStrip removes SGR so structure can be compared across palettes.
func ansiStrip(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++ // skip the 'm'
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
