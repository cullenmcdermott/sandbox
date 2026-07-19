package chat

// tool.go — a tool call rendered as the two-line ⏺-head + ⎿-elbow idiom:
//
//	⏺ Bash(npm test)
//	  ⎿  exit 0 · 42 lines (ctrl+o to expand)
//
// The head bullet is colored by status (running=Malibu, ok=Guac, error=Coral);
// the elbow carries the result summary (with an exit code for Bash, or a live
// elapsed clock while running) plus a ctrl+o hint when there is collapsible
// content. Expanded, the card reveals — in priority order — an edit diff (host-
// supplied), captured output (display-capped head+tail, ANSI-remapped), or the
// full argument. Every line is budgeted from the measured width and truncated as
// a backstop, so the card is width-safe at any terminal size. This is the §2c /
// §1c tool-card grammar from the production transcript, made self-contained: the
// host owns the data (ToolCall) and mutates it through the item's methods.

import (
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// ToolStatus is the lifecycle of a tool-call card.
type ToolStatus int

const (
	// ToolRunning is the in-flight state (blue bullet, live elapsed clock).
	ToolRunning ToolStatus = iota
	// ToolOK is a successful completion (green bullet).
	ToolOK
	// ToolError is a failed completion (coral bullet + elbow).
	ToolError
)

// bulletStyle returns the pre-built ⏺ head style for the status.
func (s ToolStatus) bulletStyle() lipgloss.Style {
	switch s {
	case ToolOK:
		return styToolBulletOK
	case ToolError:
		return styToolBulletErr
	default:
		return styToolBulletRunning
	}
}

// ToolCall is the protocol-neutral data behind a ToolItem. A host builds one per
// tool invocation and streams updates into it (arg preview, output, status). No
// field references a session backend or any internal type.
type ToolCall struct {
	// ID is a stable identifier (the tool_use id) for reconciliation.
	ID string
	// Name is the tool's display name ("Bash", "Read", …).
	Name string
	// Arg is the headline argument shown in the head ("Name(arg)"): a command,
	// file path, pattern, or url. Use ToolArg to derive it from raw JSON input.
	Arg string
	// Status is the lifecycle state.
	Status ToolStatus
	// Summary is a short result note shown on the elbow ("42 lines", "7 matches").
	Summary string
	// Output is the captured tool output revealed on expansion (display-capped).
	Output string
	// Diff is an optional pre-computed unified-diff (host-supplied, "+"/"−"
	// prefixed) revealed on expansion for edit-like tools. Kept out of this
	// package so the diff machinery (which needs the tool schema) stays with the
	// host; the card only renders what it is given.
	Diff []string
	// ExitCode, when non-nil, is the Bash exit code shown as "exit N" on the elbow.
	ExitCode *int
	// Elapsed is the running duration, shown as "running… (12s)" once it reaches
	// ~2s. The host updates it on each tick; the package never reads a clock, so
	// the caller keeps full control of time (and goldens stay deterministic).
	Elapsed time.Duration
}

// maxStoredOutput bounds the captured-output buffer a card retains, independent
// of the display cap. AppendOutput keeps only the trailing maxStoredOutput bytes
// (with 2× hysteresis) so a chatty tool cannot grow the buffer without bound —
// the card only ever displays the head+tail window anyway.
const maxStoredOutput = 64 * 1024

// ToolItem renders a ToolCall as a list.Item. The host mutates the call through
// the item's Set* methods (which bump the version), or mutates the *ToolCall in
// place and calls Bump.
type ToolItem struct {
	*list.Versioned

	call     *ToolCall
	expanded bool
	focused  bool

	cache section
}

// NewToolItem builds a tool card for the given call. A nil call renders empty
// (a defensive default; a real card always carries data).
func NewToolItem(call *ToolCall) *ToolItem {
	return &ToolItem{Versioned: list.NewVersioned(), call: call}
}

// Call returns the underlying data for read/in-place mutation. After an
// in-place mutation, call Bump to invalidate the caches.
func (t *ToolItem) Call() *ToolCall { return t.call }

// invalidate drops the render cache and bumps the version.
func (t *ToolItem) invalidate() {
	t.cache.valid = false
	t.Bump()
}

// SetStatus updates the lifecycle state and result summary.
func (t *ToolItem) SetStatus(status ToolStatus, summary string) {
	if t.call == nil {
		return
	}
	t.call.Status = status
	if summary != "" {
		t.call.Summary = summary
	}
	t.invalidate()
}

// SetArg updates the headline argument (e.g. from a streaming input preview).
func (t *ToolItem) SetArg(arg string) {
	if t.call == nil || t.call.Arg == arg {
		return
	}
	t.call.Arg = arg
	t.invalidate()
}

// SetElapsed updates the running elapsed clock. Bumps only when the whole-second
// display would change, so per-tick updates don't churn the list cache.
func (t *ToolItem) SetElapsed(d time.Duration) {
	if t.call == nil {
		return
	}
	if int(t.call.Elapsed.Seconds()) == int(d.Seconds()) {
		t.call.Elapsed = d
		return
	}
	t.call.Elapsed = d
	t.invalidate()
}

// SetExitCode records a Bash exit code shown on the elbow.
func (t *ToolItem) SetExitCode(code int) {
	if t.call == nil {
		return
	}
	t.call.ExitCode = &code
	t.invalidate()
}

// SetOutput replaces the captured output (bounded to maxStoredOutput).
func (t *ToolItem) SetOutput(out string) {
	if t.call == nil {
		return
	}
	t.call.Output = boundTail(out, maxStoredOutput)
	t.invalidate()
}

// AppendOutput appends a streamed output chunk, keeping the buffer bounded.
func (t *ToolItem) AppendOutput(chunk string) {
	if t.call == nil || chunk == "" {
		return
	}
	t.call.Output = boundTail(t.call.Output+chunk, maxStoredOutput)
	t.invalidate()
}

// SetDiff attaches a host-computed edit diff revealed on expansion.
func (t *ToolItem) SetDiff(lines []string) {
	if t.call == nil {
		return
	}
	t.call.Diff = lines
	t.invalidate()
}

// SetExpanded toggles the ctrl+o expansion state.
func (t *ToolItem) SetExpanded(b bool) {
	if t.expanded == b {
		return
	}
	t.expanded = b
	t.invalidate()
}

// Expanded reports the expansion state.
func (t *ToolItem) Expanded() bool { return t.expanded }

// SetFocused marks the card focused (a left gutter bar).
func (t *ToolItem) SetFocused(b bool) {
	if t.focused == b {
		return
	}
	t.focused = b
	t.invalidate()
}

// Focused reports the focus state.
func (t *ToolItem) Focused() bool { return t.focused }

// Expandable reports whether ctrl+o would reveal anything at the given width —
// the same width math and body build Render uses, so a card whose elbow shows no
// hint is also not toggleable.
func (t *ToolItem) Expandable(width int) bool {
	if t.call == nil {
		return false
	}
	w := focusWidth(width, t.focused)
	nameStr := t.name()
	name := truncate(nameStr, max(1, w-2))
	_, argTruncated := headArg(t.call.Arg, w-2-lipgloss.Width(name)-2)
	return len(t.expandBody(w, argTruncated)) > 0
}

func (t *ToolItem) name() string {
	if t.call.Name != "" {
		return t.call.Name
	}
	return "tool"
}

// Render draws the two-line card (plus expanded body) within width columns.
func (t *ToolItem) Render(width int) string {
	if t.call == nil {
		return ""
	}
	if width < 4 {
		width = 4
	}
	// The elapsed clock is folded into the source hash (whole seconds) so a
	// running card re-renders as the clock ticks, but a stable card hits cache.
	// Diff is hashed by CONTENT (not length): a host may mutate the *ToolCall in
	// place and Bump(), and Bump does not clear the section cache — so the hash is
	// the sole change detector and must cover every render-affecting field.
	fields := [][]byte{
		[]byte(t.call.ID),
		[]byte(t.name()),
		[]byte(t.call.Arg),
		[]byte(t.call.Summary),
		[]byte(t.call.Output),
		u64b(uint64(t.call.Status)),
		u64b(exitKey(t.call.ExitCode)),
		u64b(uint64(int64(t.call.Elapsed.Seconds()))),
	}
	for _, d := range t.call.Diff {
		fields = append(fields, []byte(d))
	}
	srcHash := fnvFields(fields...)
	extra := extraKey(theme.Epoch(), flagBits(t.expanded, t.focused))
	if t.cache.hit(width, srcHash, extra) {
		return t.cache.out
	}
	out := clampFocus(t.renderCard(focusWidth(width, t.focused)), t.focused)
	t.cache.store(width, srcHash, extra, out)
	return out
}

func (t *ToolItem) renderCard(width int) string {
	c := t.call

	// Line 1 — head: "⏺ Name(arg)". Bullet colored by status; name muted, arg dim.
	bullet := c.Status.bulletStyle().Render(toolHeadBullet)
	avail := width - 2 // "⏺ "
	name := truncate(t.name(), max(1, avail))
	head := styToolName.Render(name)
	argShown, argTruncated := headArg(c.Arg, avail-lipgloss.Width(name)-2)
	if argShown != "" {
		head += styToolArg.Render("(" + argShown + ")")
	}
	headLine := bullet + " " + head
	if lipgloss.Width(headLine) > width {
		headLine = truncate(headLine, width)
	}

	// Line 2 — elbow: "  ⎿  <result> (hint)".
	elbowText := c.Summary
	switch {
	case c.Status == ToolRunning && elbowText == "":
		elbowText = "running…"
		if c.Elapsed >= elapsedClockMin {
			elbowText = "running… (" + fmtElapsed(c.Elapsed) + ")"
		}
	case c.ExitCode != nil:
		ec := "exit " + formatInt(*c.ExitCode)
		if elbowText != "" {
			elbowText = ec + " · " + elbowText
		} else {
			elbowText = ec
		}
	case elbowText == "":
		if c.Status == ToolError {
			elbowText = "failed"
		} else {
			elbowText = "done"
		}
	}
	resultStyle := styElbowResult
	if c.Status == ToolError {
		resultStyle = styElbowResultErr
	}

	body := t.expandBody(width, argTruncated)
	hint := ""
	if len(body) > 0 {
		if t.expanded {
			hint = "  (" + CollapseHint + ")"
		} else {
			hint = "  (" + ExpandHint + ")"
		}
	}
	elbowAvail := width - elbowChromeW
	if lipgloss.Width(elbowText)+lipgloss.Width(hint) > elbowAvail {
		hint = ""
	}
	if lipgloss.Width(elbowText) > elbowAvail {
		elbowText = truncate(elbowText, elbowAvail)
	}
	elbowLine := styElbowChrome.Render("  "+toolElbow+"  ") +
		resultStyle.Render(elbowText) +
		styElbowChrome.Render(hint)
	if lipgloss.Width(elbowLine) > width {
		elbowLine = truncate(elbowLine, width)
	}

	lines := []string{headLine, elbowLine}
	if t.expanded {
		lines = append(lines, body...)
	}
	return strings.Join(lines, "\n")
}

// tool-card expansion caps: the condensed edit-diff line cap.
const toolExpandDiffMax = 16

// expandBody builds the expanded content lines, aligned under the elbow's result
// column. Priority: host-supplied edit diff, then captured output (capped
// head+tail, ANSI remapped), then the full argument as a fallback. cardWidth is
// the card's full render width; each line is indented under the elbow and then
// clamped to cardWidth so it never overflows even at very narrow widths. Returns
// nil when there is nothing to expand.
func (t *ToolItem) expandBody(cardWidth int, argTruncated bool) []string {
	contentW := cardWidth - elbowChromeW
	if contentW < 1 {
		contentW = 1
	}
	c := t.call
	var content []string
	if len(c.Diff) > 0 {
		for _, l := range condenseDiff(c.Diff, toolExpandDiffMax) {
			content = append(content, styleDiffLine(l))
		}
	}
	if c.Output != "" {
		for _, l := range clampOutputLines(c.Output) {
			content = append(content, kit.RemapANSI(l))
		}
	}
	if len(content) == 0 && argTruncated && c.Arg != "" {
		content = append(content, collapseSpaces(c.Arg))
	}
	if len(content) == 0 {
		return nil
	}
	out := make([]string, len(content))
	for i, l := range content {
		// Indent under the elbow, clamp content to its column, then final-truncate
		// the whole line to the card width as a backstop (handles cardWidth <
		// elbowChromeW).
		out[i] = truncate(strings.Repeat(" ", elbowChromeW)+truncate(l, contentW), cardWidth)
	}
	return out
}

// exitKey folds a *int exit code into the render hash: 0 when nil, else 1<<32 |
// code so nil and code-0 hash differently.
func exitKey(code *int) uint64 {
	if code == nil {
		return 0
	}
	return 1<<32 | uint64(uint32(*code))
}

// boundTail keeps the trailing max bytes of s, with 2× hysteresis so trimming is
// amortized O(1), and never cuts a UTF-8 rune mid-sequence.
func boundTail(s string, max int) string {
	if len(s) <= 2*max {
		return s
	}
	cut := len(s) - max
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return s[cut:]
}
