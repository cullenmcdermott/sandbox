package chrome

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

func setup(t *testing.T) {
	t.Helper()
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
}

func TestStatusLineKeepsRequiredShedsOptional(t *testing.T) {
	setup(t)
	segs := []Segment{
		Req("model"),
		Seg(" · cwd/very/long/path"),
		Seg(" · branch"),
		Req(" · ⚠ mode"),
	}
	// Roomy: everything fits.
	full := ansi.Strip(StatusLine(200, segs...))
	for _, w := range []string{"model", "cwd/very/long/path", "branch", "⚠ mode"} {
		if !strings.Contains(full, w) {
			t.Errorf("roomy row missing %q: %q", w, full)
		}
	}
	// Tight: optional segments shed tail-first, required kept.
	tight := ansi.Strip(StatusLine(16, segs...))
	if !strings.Contains(tight, "model") || !strings.Contains(tight, "⚠ mode") {
		t.Errorf("required segments shed at narrow width: %q", tight)
	}
	if strings.Contains(tight, "cwd/very/long/path") {
		t.Errorf("optional segment not shed at narrow width: %q", tight)
	}
}

func TestStatusLineWidthSafe(t *testing.T) {
	setup(t)
	segs := []Segment{Req(strings.Repeat("x", 50)), Seg(" · " + strings.Repeat("y", 50))}
	for _, w := range []int{10, 20, 40, 80} {
		out := StatusLine(w, segs...)
		if lw := lipgloss.Width(out); lw > w {
			t.Errorf("width %d: overflow (%d cols): %q", w, lw, out)
		}
	}
}

func TestContextGauge(t *testing.T) {
	setup(t)
	// Unknown limit → hidden.
	if g := ContextGauge(1000, 0); g != "" {
		t.Errorf("gauge shown with unknown limit: %q", g)
	}
	// Roomy (below threshold) → hidden.
	if g := ContextGauge(10, 100); g != "" {
		t.Errorf("gauge shown below threshold: %q", g)
	}
	// At/above threshold → "pct% <bar>".
	g := ansi.Strip(ContextGauge(70, 100))
	if !strings.Contains(g, "70%") {
		t.Errorf("gauge missing pct: %q", g)
	}
	if !strings.Contains(g, "█") {
		t.Errorf("gauge missing bar: %q", g)
	}
	// Past 80% → coral warning prefix.
	warn := ansi.Strip(ContextGauge(90, 100))
	if !strings.Contains(warn, "! ") {
		t.Errorf("gauge missing warn prefix past 80%%: %q", warn)
	}
}

func TestBlockBarFill(t *testing.T) {
	setup(t)
	full := ansi.Strip(BlockBar(1.0, 10))
	if strings.Count(full, "█") != 10 || strings.Contains(full, "░") {
		t.Errorf("full bar wrong: %q", full)
	}
	empty := ansi.Strip(BlockBar(0, 10))
	if strings.Count(empty, "░") != 10 || strings.Contains(empty, "█") {
		t.Errorf("empty bar wrong: %q", empty)
	}
	half := ansi.Strip(BlockBar(0.5, 10))
	if strings.Count(half, "█") != 5 {
		t.Errorf("half bar wrong fill: %q", half)
	}
}

func TestWorkingIndicator(t *testing.T) {
	setup(t)
	out := ansi.Strip(WorkingIndicator(Working{
		Verb: "Thinking", Elapsed: 12 * time.Second, OutputTokens: 820, Hint: "esc to interrupt",
	}))
	for _, want := range []string{"Thinking", "12s", "↓820 tokens", "esc to interrupt"} {
		if !strings.Contains(out, want) {
			t.Errorf("working indicator missing %q: %q", want, out)
		}
	}
	// Zero Working → bare "Working…".
	bare := ansi.Strip(WorkingIndicator(Working{}))
	if !strings.Contains(bare, "Working") {
		t.Errorf("bare working indicator wrong: %q", bare)
	}
}

// The working indicator honors reduced motion: a static "…" (not the animated
// ellipsis) so reduced-motion terminals and goldens stay byte-stable across
// frames.
func TestWorkingIndicatorReducedMotion(t *testing.T) {
	setup(t)
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	a := WorkingIndicator(Working{Verb: "Writing", Frame: 1})
	b := WorkingIndicator(Working{Verb: "Writing", Frame: 7})
	if a != b {
		t.Errorf("reduced motion not stable across frames:\n%q\n%q", a, b)
	}
	if !strings.Contains(ansi.Strip(a), "Writing…") {
		t.Errorf("reduced-motion ellipsis wrong: %q", ansi.Strip(a))
	}
}

func TestNotice(t *testing.T) {
	setup(t)
	for _, k := range []NoticeKind{NoticeInfo, NoticeWarn, NoticeError} {
		out := ansi.Strip(Notice(k, "context compacted", 80))
		if !strings.Contains(out, "context compacted") {
			t.Errorf("notice missing text: %q", out)
		}
	}
	// Width clamp.
	long := Notice(NoticeError, strings.Repeat("boom ", 40), 30)
	if lw := lipgloss.Width(long); lw > 30 {
		t.Errorf("notice overflow: %d cols", lw)
	}
}
