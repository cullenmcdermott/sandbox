package chat

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// widthSafe asserts every rendered line fits within width display columns
// (ANSI-aware, grapheme-aware). This is the core invariant the handoff requires:
// "every rendered frame fits its declared width and height."
func widthSafe(t *testing.T, name string, out string, width int) {
	t.Helper()
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > width {
			t.Errorf("%s @width=%d: line %d overflows (%d cols): %q", name, width, i, w, l)
		}
	}
}

// itemFactories builds one representative instance of every public item type,
// with realistic content, for the width-safety table.
func itemFactories() map[string]func() list.Item {
	ec := 0
	return map[string]func() list.Item{
		"user": func() list.Item {
			return NewUserItem("run the tests and give me a summary of any failures you find in the suite")
		},
		"reasoning-multi": func() list.Item {
			return NewReasoningItem("First I should read the failing test.\nThen reproduce it locally.\nThen bisect the regression across recent commits to find the culprit.", false)
		},
		"reasoning-live": func() list.Item {
			return NewReasoningItem("thinking about the reconnect path and whether the SPDY stream is the culprit here", true)
		},
		"tool-ok": func() list.Item {
			return NewToolItem(&ToolCall{ID: "t", Name: "Bash", Arg: "go test ./... -run TestReconnect", Status: ToolOK, Summary: "42 lines", ExitCode: &ec, Output: "PASS\nok  pkg 0.3s"})
		},
		"tool-running": func() list.Item {
			return NewToolItem(&ToolCall{ID: "t", Name: "Bash", Arg: "npm install", Status: ToolRunning, Elapsed: 12e9})
		},
		"tool-err": func() list.Item {
			return NewToolItem(&ToolCall{ID: "t", Name: "Read", Arg: "/etc/does/not/exist", Status: ToolError, Summary: "no such file"})
		},
		"tool-diff-expanded": func() list.Item {
			it := NewToolItem(&ToolCall{ID: "t", Name: "Edit", Arg: "server.go", Status: ToolOK, Diff: []string{"+ added a line", "− removed a line", "  context"}})
			it.SetExpanded(true)
			return it
		},
		"subagent": func() list.Item {
			return NewSubagentItem(&Subagent{ID: "s", AgentName: "Explore", Prompt: "find where the reconnect backoff is configured", Status: ToolOK,
				Children: []*ToolCall{
					{Name: "Grep", Arg: "backoff", Status: ToolOK, Summary: "12 matches across 4 files"},
					{Name: "Read", Arg: "/pkg/runner/reconnect.go", Status: ToolRunning},
				}, Narration: "the backoff constant lives in reconnect.go and defaults to 250ms"})
		},
		"subagent-collapsed": func() list.Item {
			s := NewSubagentItem(&Subagent{ID: "s", AgentName: "builder", Prompt: "implement the fix", Status: ToolRunning,
				Children: []*ToolCall{{Name: "Edit", Arg: "x.go", Status: ToolOK}}})
			s.SetCollapsed(true)
			return s
		},
		"todos": func() list.Item {
			return NewTodosItem([]Todo{
				{Content: "Reproduce the flake", Status: TodoCompleted},
				{Content: "Bisect the regression", ActiveForm: "Bisecting the regression across recent commits", Status: TodoInProgress},
				{Content: "Write a regression test", Status: TodoPending},
			})
		},
		"todos-empty": func() list.Item { return NewTodosItem(nil) },
		"notice-info": func() list.Item {
			return NewNoticeItem(NoticeInfo, "context compacted to free up room for the rest of the turn")
		},
		"notice-warn": func() list.Item { return NewNoticeItem(NoticeWarn, "pod was rescheduled onto a new node") },
		"notice-err": func() list.Item {
			return NewNoticeItem(NoticeError, "turn failed: the runner reset the connection midway through the response")
		},
		"shell": func() list.Item { return NewShellItem("$ echo hi\nhi") },
		"elbow": func() list.Item { return NewElbowNotice("interrupted by user") },
		"perm-tool": func() list.Item {
			return NewPermissionItem(&Permission{Tool: "Bash", Arg: "rm -rf /tmp/scratch/very/long/path/that/needs/clamping"})
		},
		"perm-diff": func() list.Item {
			return NewPermissionItem(&Permission{Tool: "Edit", Arg: "server.go", Diff: []string{"+ added", "− removed"}})
		},
		"perm-plan": func() list.Item {
			return NewPermissionItem(&Permission{IsPlan: true, Plan: "1. read the failing test\n2. reproduce it\n3. bisect the regression to find the culprit commit"})
		},
		"footer": func() list.Item {
			return NewFooterItem(&TurnFooter{Model: "Opus 4.8", Backend: "anthropic", Elapsed: 3 * time.Minute, InputTokens: 1_250_000, OutputTokens: 84000, CostUSD: 12.34})
		},
	}
}

// TestItemsWidthSafe renders every item at the handoff's required widths (and a
// few narrow ones) and asserts no line overflows. Covers blurred + focused.
func TestItemsWidthSafe(t *testing.T) {
	restoreTheme(t)
	widths := []int{8, 20, 40, 80, 100, 140}
	for name, factory := range itemFactories() {
		for _, w := range widths {
			for _, focused := range []bool{false, true} {
				it := factory()
				if f, ok := it.(interface{ SetFocused(bool) }); ok {
					f.SetFocused(focused)
				}
				out := it.Render(w)
				widthSafe(t, name, out, w)
			}
		}
	}
}

// TestUnicodeWidthSafe pushes CJK, emoji, combining marks, a long unbroken token,
// and multiline paste through the wrapped-body items and asserts width-safety.
func TestUnicodeWidthSafe(t *testing.T) {
	restoreTheme(t)
	cases := []string{
		"你好世界这是一个用于测试宽字符换行是否正确的中文字符串它应该在列边界处换行",                                                          // CJK (wide)
		"🎉🎊✨🚀💥🔥🌟⭐️🎈🎁 party emoji run that should wrap by display width not byte count",                   // emoji
		"ééé combining acute accents àb́ĉ more combining marks here",                                // combining
		"https://example.com/a/very/long/unbroken/path/without/any/spaces/that/must/hard/wrap/somewhere", // long unbroken
		"line one\nline two\nline three\n\nline five after a blank",                                      // multiline paste
	}
	widths := []int{10, 24, 40, 80}
	for _, text := range cases {
		for _, w := range widths {
			widthSafe(t, "user", NewUserItem(text).Render(w), w)
			widthSafe(t, "notice", NewNoticeItem(NoticeInfo, text).Render(w), w)
			widthSafe(t, "reasoning", NewReasoningItem(text, false).Render(w), w)
			widthSafe(t, "shell", NewShellItem(text).Render(w), w)
		}
	}
}

// TestLargeToolOutputWidthSafeAndCapped feeds a very large captured output and
// asserts the expanded card is width-safe and display-capped (head+tail window).
func TestLargeToolOutputWidthSafeAndCapped(t *testing.T) {
	restoreTheme(t)
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("output line number ")
		b.WriteString(strings.Repeat("x", i%120)) // some very long lines
		b.WriteByte('\n')
	}
	it := NewToolItem(&ToolCall{ID: "t", Name: "Bash", Arg: "dump", Status: ToolOK, Output: b.String()})
	it.SetExpanded(true)
	out := it.Render(80)
	widthSafe(t, "large-output", out, 80)
	lines := strings.Count(out, "\n") + 1
	if lines > outputHeadLines+outputTailLines+8 {
		t.Fatalf("expanded output not capped: %d lines", lines)
	}
	if !strings.Contains(out, "lines hidden") {
		t.Errorf("expected a hidden-lines marker in capped output")
	}
}

// restoreTheme pins a known dark palette and restores it after the test so a
// theme-swap test can't contaminate the others.
func restoreTheme(t *testing.T) {
	t.Helper()
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
}
