package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// plain strips ANSI so a test can assert on structure/text without baking in the
// palette's SGR codes.
func plain(s string) string { return ansi.Strip(s) }

// --- caching & version discipline ------------------------------------------

// TestRenderDoesNotBump: rendering identical state never bumps the version (the
// list keys its cache on version, so a stable item must stay a cache hit).
func TestRenderDoesNotBump(t *testing.T) {
	restoreTheme(t)
	it := NewToolItem(&ToolCall{ID: "t", Name: "Bash", Arg: "ls", Status: ToolOK, Summary: "3 lines"})
	v0 := it.Version()
	_ = it.Render(80)
	_ = it.Render(80)
	_ = it.Render(40)
	if it.Version() != v0 {
		t.Fatalf("Render bumped version: %d -> %d", v0, it.Version())
	}
}

// TestMutationBumps: each Set* that changes state bumps the version and no-ops
// that don't.
func TestMutationBumps(t *testing.T) {
	restoreTheme(t)
	it := NewToolItem(&ToolCall{ID: "t", Name: "Bash", Arg: "ls", Status: ToolRunning})
	v0 := it.Version()
	it.SetStatus(ToolOK, "done")
	if it.Version() == v0 {
		t.Fatal("SetStatus did not bump")
	}
	v1 := it.Version()
	it.SetExpanded(false) // already false → no-op
	if it.Version() != v1 {
		t.Fatal("no-op SetExpanded bumped")
	}
	it.SetExpanded(true)
	if it.Version() == v1 {
		t.Fatal("SetExpanded did not bump")
	}
}

// TestCacheStableOutput: a cache hit returns byte-identical output.
func TestCacheStableOutput(t *testing.T) {
	restoreTheme(t)
	it := NewUserItem("hello there, this is a stable prompt")
	a := it.Render(60)
	b := it.Render(60)
	if a != b {
		t.Fatal("cache hit returned different output")
	}
}

// --- theme-epoch invalidation ----------------------------------------------

// TestThemeSwapReskins: a theme swap must change the rendered ANSI (the epoch is
// folded into every item's cache key, so stale-palette output is never re-served).
func TestThemeSwapReskins(t *testing.T) {
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	items := []interface{ Render(int) string }{
		NewUserItem("hi"),
		NewToolItem(&ToolCall{Name: "Bash", Arg: "ls", Status: ToolOK, Summary: "ok"}),
		NewReasoningItem("thinking", false),
		NewNoticeItem(NoticeError, "boom"),
		NewTodosItem([]Todo{{Content: "x", Status: TodoInProgress}}),
	}
	theme.ApplyForBackground(true)
	before := make([]string, len(items))
	for i, it := range items {
		before[i] = it.Render(60)
	}
	theme.ApplyForBackground(false) // swap → epoch bump + rebuilt styles
	for i, it := range items {
		after := it.Render(60)
		if after == before[i] {
			t.Errorf("item %d: theme swap did not change ANSI (stale palette re-served)", i)
		}
		// Plain text (structure) must be unchanged by a palette swap.
		if plain(after) != plain(before[i]) {
			t.Errorf("item %d: theme swap changed structure: %q vs %q", i, plain(after), plain(before[i]))
		}
	}
}

// --- focus ------------------------------------------------------------------

// TestFocusChangesOutput: focusing an item changes its output and toggling back
// restores it exactly.
func TestFocusChangesOutput(t *testing.T) {
	restoreTheme(t)
	it := NewUserItem("focus me")
	blurred := it.Render(40)
	it.SetFocused(true)
	focused := it.Render(40)
	if focused == blurred {
		t.Fatal("focus did not change output")
	}
	if !strings.Contains(plain(focused), "▌") {
		t.Errorf("focused output missing the gutter bar: %q", plain(focused))
	}
	it.SetFocused(false)
	if it.Render(40) != blurred {
		t.Fatal("un-focus did not restore output")
	}
}

// --- tool card semantics ----------------------------------------------------

func TestToolBulletColorByStatus(t *testing.T) {
	restoreTheme(t)
	run := NewToolItem(&ToolCall{Name: "Bash", Arg: "x", Status: ToolRunning}).Render(60)
	ok := NewToolItem(&ToolCall{Name: "Bash", Arg: "x", Status: ToolOK, Summary: "ok"}).Render(60)
	err := NewToolItem(&ToolCall{Name: "Bash", Arg: "x", Status: ToolError, Summary: "bad"}).Render(60)
	// Plain text head is identical; only the bullet color (ANSI) differs.
	if firstLineOf(run) == firstLineOf(ok) || firstLineOf(ok) == firstLineOf(err) {
		t.Fatal("bullet color does not vary by status")
	}
}

func TestToolExitCodeAndElapsed(t *testing.T) {
	restoreTheme(t)
	ec := 2
	it := NewToolItem(&ToolCall{Name: "Bash", Arg: "make", Status: ToolError, Summary: "build failed", ExitCode: &ec})
	if !strings.Contains(plain(it.Render(80)), "exit 2 · build failed") {
		t.Errorf("missing exit code: %q", plain(it.Render(80)))
	}
	// Running under the clock threshold: bare "running…".
	young := NewToolItem(&ToolCall{Name: "Bash", Arg: "x", Status: ToolRunning, Elapsed: 1e9})
	if got := plain(young.Render(80)); !strings.Contains(got, "running…") || strings.Contains(got, "running… (") {
		t.Errorf("young running card should show bare running… (no clock): %q", got)
	}
	// Past the threshold: "running… (12s)".
	old := NewToolItem(&ToolCall{Name: "Bash", Arg: "x", Status: ToolRunning, Elapsed: 12e9})
	if !strings.Contains(plain(old.Render(80)), "running… (12s)") {
		t.Errorf("old running card should show elapsed clock: %q", plain(old.Render(80)))
	}
}

func TestToolExpansionRevealsContent(t *testing.T) {
	restoreTheme(t)
	it := NewToolItem(&ToolCall{Name: "Bash", Arg: "cat f", Status: ToolOK, Summary: "2 lines", Output: "alpha\nbravo"})
	if !it.Expandable(80) {
		t.Fatal("card with output should be expandable")
	}
	collapsed := plain(it.Render(80))
	if strings.Contains(collapsed, "alpha") {
		t.Fatal("collapsed card leaked output")
	}
	if !strings.Contains(collapsed, "(ctrl+o to expand)") {
		t.Errorf("collapsed card missing expand hint: %q", collapsed)
	}
	it.SetExpanded(true)
	ex := plain(it.Render(80))
	if !strings.Contains(ex, "alpha") || !strings.Contains(ex, "bravo") {
		t.Errorf("expanded card missing output: %q", ex)
	}
	if !strings.Contains(ex, "(ctrl+o to collapse)") {
		t.Errorf("expanded card missing collapse hint: %q", ex)
	}
}

func TestToolNoExpandNoHint(t *testing.T) {
	restoreTheme(t)
	it := NewToolItem(&ToolCall{Name: "Grep", Arg: "x", Status: ToolOK, Summary: "1 match"})
	if it.Expandable(80) {
		t.Fatal("card with no collapsible content should not be expandable")
	}
	if strings.Contains(plain(it.Render(80)), "ctrl+o") {
		t.Errorf("non-expandable card should show no ctrl+o hint")
	}
}

func TestToolBoundedOutput(t *testing.T) {
	restoreTheme(t)
	it := NewToolItem(&ToolCall{Name: "Bash", Status: ToolRunning})
	big := strings.Repeat("x", 10*maxStoredOutput)
	it.SetOutput(big)
	if len(it.Call().Output) > 2*maxStoredOutput {
		t.Fatalf("output not bounded: %d bytes", len(it.Call().Output))
	}
	for i := 0; i < 100; i++ {
		it.AppendOutput(strings.Repeat("y", 2000))
	}
	if len(it.Call().Output) > 2*maxStoredOutput {
		t.Fatalf("appended output not bounded: %d bytes", len(it.Call().Output))
	}
}

// --- subagent semantics -----------------------------------------------------

func TestSubagentTreeAndCollapse(t *testing.T) {
	restoreTheme(t)
	s := NewSubagentItem(&Subagent{AgentName: "Explore", Prompt: "look", Status: ToolOK,
		Children: []*ToolCall{
			{Name: "Grep", Arg: "a", Status: ToolOK, Summary: "1"},
			{Name: "Read", Arg: "b", Status: ToolOK, Summary: "2"},
		}, Narration: "found it"})
	full := plain(s.Render(80))
	if !strings.Contains(full, "⊟ Task") || !strings.Contains(full, "· Explore · 2 tools") {
		t.Errorf("header wrong: %q", firstLineOf(plain(s.Render(80))))
	}
	if !strings.Contains(full, "├") || !strings.Contains(full, "found it") {
		t.Errorf("tree/narration missing: %q", full)
	}
	s.SetCollapsed(true)
	col := plain(s.Render(80))
	if strings.Contains(col, "Grep") || strings.Contains(col, "found it") {
		t.Errorf("collapsed subagent leaked children: %q", col)
	}
	if !strings.Contains(col, "⊞ Task") {
		t.Errorf("collapsed glyph wrong: %q", col)
	}
}

// --- todos ------------------------------------------------------------------

func TestTodosStatuses(t *testing.T) {
	restoreTheme(t)
	out := plain(NewTodosItem([]Todo{
		{Content: "done thing", Status: TodoCompleted},
		{Content: "do thing", ActiveForm: "Doing thing", Status: TodoInProgress},
		{Content: "later", Status: TodoPending},
	}).Render(80))
	for _, want := range []string{"✓ done thing", "▸ Doing thing", "○ later"} {
		if !strings.Contains(out, want) {
			t.Errorf("todos missing %q in %q", want, out)
		}
	}
	if got := plain(NewTodosItem(nil).Render(80)); got != "todos cleared" {
		t.Errorf("empty todos: %q", got)
	}
}

// --- citations --------------------------------------------------------------

func TestCitations(t *testing.T) {
	restoreTheme(t)
	out := plain(RenderCitations([]Citation{
		{Title: "SPDY", URL: "https://a"},
		{URL: "https://b"},
		{Title: "OnlyTitle"},
		{CitedText: "quote only — renderless"},
	}, 80))
	if !strings.Contains(out, "Sources:") {
		t.Fatalf("missing header: %q", out)
	}
	for _, want := range []string{"1. SPDY — https://a", "2. https://b", "3. OnlyTitle"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "4.") {
		t.Errorf("renderless citation should be skipped: %q", out)
	}
	if RenderCitations(nil, 80) != "" {
		t.Error("no citations should render empty")
	}
	// A web-controlled title with newlines/escapes is flattened to one line.
	dirty := plain(RenderCitations([]Citation{{Title: "line1\nline2\x1b[31mred", URL: "https://c"}}, 120))
	if strings.Count(dirty, "\n") != 1 { // "Sources:" + one entry
		t.Errorf("dirty title not flattened: %q", dirty)
	}
}

// --- notice / shell ---------------------------------------------------------

func TestNoticeTones(t *testing.T) {
	restoreTheme(t)
	info := NewNoticeItem(NoticeInfo, "same text").Render(60)
	warn := NewNoticeItem(NoticeWarn, "same text").Render(60)
	err := NewNoticeItem(NoticeError, "same text").Render(60)
	if info == warn || warn == err || info == err {
		t.Fatal("notice tones do not differ by color")
	}
	if plain(info) != "same text" {
		t.Errorf("notice text wrong: %q", plain(info))
	}
}

func TestElbowNoticeAndSanitize(t *testing.T) {
	restoreTheme(t)
	if !strings.Contains(plain(NewElbowNotice("interrupted").Render(60)), "⎿  interrupted") {
		t.Error("elbow notice missing glyph")
	}
	// A control-laden shell result is sanitized (cursor moves dropped) and clamped.
	dirty := NewShellItem("progress\x1b[2K\rdone\nnext\x07line").Render(40)
	widthSafe(t, "shell-dirty", dirty, 40)
	if strings.Contains(dirty, "\x1b[2K") || strings.Contains(dirty, "\x07") {
		t.Errorf("shell output not sanitized: %q", dirty)
	}
}

// --- framing helpers --------------------------------------------------------

func TestBulletAndQuote(t *testing.T) {
	restoreTheme(t)
	b := plain(Bullet("line one\nline two"))
	if !strings.HasPrefix(b, "⏺ line one") {
		t.Errorf("Bullet head wrong: %q", b)
	}
	if !strings.Contains(b, "\n  line two") {
		t.Errorf("Bullet continuation not hanging-indented: %q", b)
	}
	q := plain(Quote("a\nb"))
	if !strings.HasPrefix(q, "> a") || !strings.Contains(q, "\n  b") {
		t.Errorf("Quote framing wrong: %q", q)
	}
}

// firstLineOf returns the first line of s.
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
