package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// TestThemeSwapReskinsThroughList is the render-level (not key-level) proof that
// a theme swap re-skins a committed transcript once the host drops the list
// cache — the fix for the stale-palette bug the reviewers caught. It asserts BOTH
// halves: before InvalidateAll the list re-serves the old-palette ANSI (the bug
// if left unhandled), and after it the ANSI changes while the structure holds.
func TestThemeSwapReskinsThroughList(t *testing.T) {
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	theme.ApplyForBackground(true)

	items := []list.Item{
		NewUserItem("hello"),
		NewToolItem(&ToolCall{Name: "Bash", Arg: "ls", Status: ToolOK, Summary: "ok"}),
		NewSubagentItem(&Subagent{AgentName: "Explore", Prompt: "x", Status: ToolOK}),
		NewNoticeItem(NoticeError, "boom"),
	}
	l := list.New(items...)
	l.SetSize(50, 12)
	dark := l.Render()

	theme.ApplyForBackground(false) // swap: epoch bumps, styles rebuild, NO version bump
	if stale := l.Render(); stale != dark {
		t.Fatal("expected the list to re-serve cached (stale) ANSI before InvalidateAll")
	}
	l.InvalidateAll() // the documented host obligation (chat.OnThemeChange wires this)
	fresh := l.Render()
	if fresh == dark {
		t.Error("InvalidateAll did not re-skin the transcript after a theme swap")
	}
	if plain(fresh) != plain(dark) {
		t.Error("theme reskin changed transcript structure (should be color-only)")
	}
}

// TestOnThemeChangeWiresReskin proves the convenience helper re-skins on swap.
func TestOnThemeChangeWiresReskin(t *testing.T) {
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	theme.ApplyForBackground(true)
	l := list.New(NewNoticeItem(NoticeError, "x"))
	l.SetSize(30, 5)
	unsub := OnThemeChange(l)
	defer unsub()
	before := l.Render()
	theme.ApplyForBackground(false) // hook fires → l.InvalidateAll()
	if after := l.Render(); after == before {
		t.Error("OnThemeChange did not re-skin the list on swap")
	}
}

// TestToolDiffInPlaceMutationInvalidates: a host edits diff lines in place (same
// count) and calls Bump — the render must change (diff is content-hashed, not
// length-hashed). This is the fix for the length-only hash footgun.
func TestToolDiffInPlaceMutationInvalidates(t *testing.T) {
	restoreTheme(t)
	it := NewToolItem(&ToolCall{Name: "Edit", Arg: "x.go", Status: ToolOK, Diff: []string{"+ old line"}})
	it.SetExpanded(true)
	first := plain(it.Render(60))
	// Mutate the diff content in place (same line count) and Bump directly, the
	// way the Call() doc says a host may.
	it.Call().Diff[0] = "+ new line"
	it.Bump()
	second := plain(it.Render(60))
	if first == second {
		t.Fatal("in-place diff edit + Bump did not change the render (length-only hash?)")
	}
	if !strings.Contains(second, "new line") {
		t.Errorf("render did not pick up the new diff content: %q", second)
	}
}

// TestAsciiDiffClassified: a raw `git diff` (ASCII '-') has its deletions
// classified/colored as deletions, not swallowed as context.
func TestAsciiDiffClassified(t *testing.T) {
	restoreTheme(t)
	it := NewToolItem(&ToolCall{Name: "Edit", Arg: "x.go", Status: ToolOK,
		Diff: []string{" ctx", "-removed via ascii", "+added"}})
	it.SetExpanded(true)
	full := it.Render(60)
	// The ASCII '-' line must survive condensing (a change line is always kept).
	if !strings.Contains(plain(full), "removed via ascii") {
		t.Errorf("ASCII '-' deletion was dropped as context: %q", plain(full))
	}
	// It must be colored as a deletion (Coral), distinct from a context line.
	del := styleDiffLine("-removed")
	ctx := styleDiffLine(" ctx")
	if del == ctx || !strings.Contains(del, "removed") {
		t.Error("ASCII '-' not styled as a deletion")
	}
}

// TestSubagentNilChildNoPanic: a host appends a nil to the exported Children
// slice; Render must not panic and must render the non-nil children.
func TestSubagentNilChildNoPanic(t *testing.T) {
	restoreTheme(t)
	s := NewSubagentItem(&Subagent{AgentName: "Explore", Prompt: "x", Status: ToolRunning})
	s.Subagent().Children = []*ToolCall{
		{Name: "Grep", Arg: "a", Status: ToolOK, Summary: "1"},
		nil,
		{Name: "Read", Arg: "b", Status: ToolOK, Summary: "2"},
	}
	s.Bump()
	out := plain(s.Render(80)) // must not panic
	if !strings.Contains(out, "Grep") || !strings.Contains(out, "Read") {
		t.Errorf("non-nil children missing: %q", out)
	}
}

// TestNilDataRendersEmpty: items built with nil data render "" and Set* are safe.
func TestNilDataRendersEmpty(t *testing.T) {
	restoreTheme(t)
	tool := NewToolItem(nil)
	sub := NewSubagentItem(nil)
	perm := NewPermissionItem(nil)
	for _, it := range []interface{ Render(int) string }{tool, sub, perm} {
		if got := it.Render(80); got != "" {
			t.Errorf("nil-data item rendered %q, want empty", got)
		}
	}
	// Set* on nil-data items must be safe no-ops.
	tool.SetStatus(ToolOK, "x")
	tool.SetExpanded(true)
	sub.SetStatus(ToolOK)
	sub.AddChild(&ToolCall{Name: "x"})
}

// TestUnicodeThroughTruncatePathItems pushes wide/emoji/combining content through
// the TRUNCATE-path items (tool, subagent, todos, citations, permission) that the
// original unicode table skipped — the truncate helper must never cut a wide rune
// mid-cell and overflow.
func TestUnicodeThroughTruncatePathItems(t *testing.T) {
	restoreTheme(t)
	cjk := "配置文件读取器实现宽字符宽度测试用例"
	emoji := "🎉🚀🔥🌟⭐️🎈 progress"
	widths := []int{8, 10, 16, 24, 40}
	for _, w := range widths {
		tool := NewToolItem(&ToolCall{Name: "Read", Arg: cjk, Status: ToolOK, Summary: emoji, Output: cjk + "\n" + emoji})
		tool.SetExpanded(true)
		widthSafe(t, "tool-cjk", tool.Render(w), w)

		sub := NewSubagentItem(&Subagent{AgentName: emoji, Prompt: cjk, Status: ToolOK,
			Children:  []*ToolCall{{Name: "Grep", Arg: cjk, Status: ToolOK, Summary: emoji}},
			Narration: cjk})
		widthSafe(t, "sub-cjk", sub.Render(w), w)

		widthSafe(t, "todos-cjk", NewTodosItem([]Todo{{Content: cjk, Status: TodoInProgress, ActiveForm: emoji}}).Render(w), w)
		widthSafe(t, "cites-cjk", RenderCitations([]Citation{{Title: emoji, URL: cjk}}, w), w)
		widthSafe(t, "perm-cjk", NewPermissionItem(&Permission{Tool: "Bash", Arg: cjk}).Render(w), w)
	}
}

// TestTabExpansionWidthSafe: tabs in captured output / shell text are expanded so
// the rendered line's real column width is honored (lipgloss.Width measures a raw
// tab as 0).
func TestTabExpansionWidthSafe(t *testing.T) {
	restoreTheme(t)
	tabbed := "a\tb\tc\tdddddddd\teeeeeeee"
	tool := NewToolItem(&ToolCall{Name: "Bash", Arg: "x", Status: ToolOK, Output: tabbed})
	tool.SetExpanded(true)
	out := tool.Render(30)
	widthSafe(t, "tool-tabs", out, 30)
	if strings.Contains(out, "\t") {
		t.Error("tab survived into tool output (should be expanded)")
	}
	shell := NewShellItem(tabbed).Render(20)
	widthSafe(t, "shell-tabs", shell, 20)
	if strings.Contains(shell, "\t") {
		t.Error("tab survived into shell render")
	}
}

// TestReasoningStreamingVsCommitted: the live tail vs committed head windowing.
func TestReasoningStreamingVsCommitted(t *testing.T) {
	restoreTheme(t)
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("reasoning line ")
		b.WriteString(formatInt(i))
		b.WriteByte('\n')
	}
	text := b.String()

	committed := NewReasoningItem(text, false)
	if !committed.Expandable(60) {
		t.Fatal("multi-line committed think should be expandable")
	}
	cOut := plain(committed.Render(60))
	if !strings.Contains(cOut, "lines (ctrl+o)") {
		t.Errorf("committed think missing head-cap trailer: %q", cOut)
	}
	if strings.Contains(cOut, "line 19") {
		t.Error("committed think should cap the FIRST lines, not show the tail")
	}
	committed.SetExpanded(true)
	if !strings.Contains(plain(committed.Render(60)), "line 19") {
		t.Error("expanded committed think should reveal all lines")
	}

	live := NewReasoningItem(text, true)
	if live.Expandable(60) {
		t.Error("a live think should not report Expandable")
	}
	lOut := plain(live.Render(60))
	if !strings.Contains(lOut, "earlier lines") {
		t.Errorf("live think missing tail marker: %q", lOut)
	}
	if !strings.Contains(lOut, "line 19") {
		t.Error("live think should TAIL the newest lines")
	}

	// Empty live think renders the label only.
	if got := plain(NewReasoningItem("", true).Render(60)); got != "∴ Thinking" {
		t.Errorf("empty live think: %q", got)
	}
}

// TestSetElapsedNoOpWithinSecond: SetElapsed must not bump the version when the
// whole-second display is unchanged, but must bump when it changes.
func TestSetElapsedNoOpWithinSecond(t *testing.T) {
	restoreTheme(t)
	it := NewToolItem(&ToolCall{Name: "Bash", Arg: "x", Status: ToolRunning})
	it.SetElapsed(1400e6) // 1.4s
	v := it.Version()
	it.SetElapsed(1600e6) // 1.6s — same whole second → no bump
	if it.Version() != v {
		t.Error("SetElapsed within the same second bumped the version")
	}
	it.SetElapsed(2000e6) // 2s — display changes → bump
	if it.Version() == v {
		t.Error("SetElapsed across a second boundary did not bump")
	}
}

// TestToolArgAndSummary pins the public reducer helpers.
func TestToolArgAndSummary(t *testing.T) {
	cases := []struct {
		tool, input, want string
	}{
		{"Read", `{"file_path":"/a/b/c/d.go"}`, ".../c/d.go"},
		{"Bash", `{"command":"go   test\n./..."}`, "go test ./..."},
		{"Grep", `{"pattern":"foo.*bar"}`, "foo.*bar"},
		{"WebFetch", `{"url":"https://x"}`, "https://x"},
		{"Unknown", `{"query":"hi"}`, "hi"},
		{"Read", `{}`, ""},
	}
	for _, c := range cases {
		if got := ToolArg(c.tool, json.RawMessage(c.input)); got != c.want {
			t.Errorf("ToolArg(%q,%s)=%q want %q", c.tool, c.input, got, c.want)
		}
	}
	sums := map[string]string{
		"l1\nl2\nl3": "3 lines",
		"l1\nl2\n":   "2 lines", // trailing newline decrement
		"single":     "single",
		"":           "",
	}
	for in, want := range sums {
		if got := ToolSummary(in); got != want {
			t.Errorf("ToolSummary(%q)=%q want %q", in, got, want)
		}
	}
}

// TestBoundTailValidUTF8: a huge multibyte output is bounded without cutting a
// rune mid-sequence.
func TestBoundTailValidUTF8(t *testing.T) {
	restoreTheme(t)
	it := NewToolItem(&ToolCall{Name: "Bash", Status: ToolRunning})
	it.SetOutput(strings.Repeat("世界", 100_000)) // ~600KB of 3-byte runes
	got := it.Call().Output
	if len(got) > 2*maxStoredOutput {
		t.Fatalf("output not bounded: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Error("bounded output is not valid UTF-8 (cut mid-rune)")
	}
}

// TestPermissionVariants pins the three permission renderings.
func TestPermissionVariants(t *testing.T) {
	restoreTheme(t)
	tool := plain(NewPermissionItem(&Permission{Tool: "Bash", Arg: "rm -rf x"}).Render(60))
	if !strings.Contains(tool, "◆ Bash(rm -rf x)") || !strings.Contains(tool, "approve") || !strings.Contains(tool, "deny") {
		t.Errorf("tool permission: %q", tool)
	}
	plan := plain(NewPermissionItem(&Permission{IsPlan: true, Plan: "1. step one\n2. step two"}).Render(60))
	if !strings.Contains(plan, "◆ Plan") || !strings.Contains(plan, "keep planning") {
		t.Errorf("plan permission: %q", plan)
	}
	diff := plain(NewPermissionItem(&Permission{Tool: "Edit", Arg: "x.go", Diff: []string{"+add", "-del"}}).Render(60))
	if !strings.Contains(diff, "+add") || !strings.Contains(diff, "-del") {
		t.Errorf("diff permission: %q", diff)
	}
}
