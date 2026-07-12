package dashboard

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// redesign_2c_test.go — behavioral locks for the §2c chat redesign: the quiet
// message grammar (no full-height role gutter bars), the working indicator above
// the composer, and the collapsed status line.

// --------------------------------------------------------------------------
// ITEM 1 — message grammar
// --------------------------------------------------------------------------

// The full-height "▌" role gutter bars are gone: an assistant block heads with a
// single ⏺ bullet, a user block with a dim "> " quote, and NEITHER carries the
// old vertical bar down every line.
func TestMessageGrammarNoGutterBars(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200

	user := m.renderBlock(m.newBlockCard(blockUser, "hello there"))
	if strings.Contains(user, "▌") {
		t.Errorf("user block still renders the ▌ gutter bar:\n%q", user)
	}
	if !strings.HasPrefix(stripANSI(user), "> ") {
		t.Errorf("user block must head with a dim \"> \" quote, got %q", stripANSI(user))
	}

	asst := m.renderBlock(m.newBlockCard(blockAssistant, "some reply"))
	if strings.Contains(asst, "▌") {
		t.Errorf("assistant block still renders the ▌ gutter bar:\n%q", asst)
	}
	if !strings.HasPrefix(stripANSI(asst), toolHeadBullet+" ") {
		t.Errorf("assistant block must head with the ⏺ bullet, got %q", stripANSI(asst))
	}
	// Continuation lines of a multi-line user message align under the head (2-space
	// hanging indent), not under a re-drawn prefix.
	multi := stripANSI(m.renderBlock(m.newBlockCard(blockUser, "line one\nline two")))
	lines := strings.Split(multi, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "  line two") {
		t.Errorf("continuation line must hang-indent by 2, got %q", multi)
	}
}

// The user's own words are the quietest element now: styleTUser drops Bold+Guac.
func TestUserTextIsNotBold(t *testing.T) {
	rebuildStyles()
	if styleTUser.GetBold() {
		t.Error("styleTUser must not be bold (§2c: the user's words are the quietest element)")
	}
}

// --------------------------------------------------------------------------
// ITEM 2 — working indicator
// --------------------------------------------------------------------------

func workingModel(t *testing.T) *TranscriptModel {
	t.Helper()
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 100, 30
	m.turnActive = true
	return m
}

func TestWorkingLineIdleIsEmpty(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 100, 30
	if got := m.workingLine(); got != "" {
		t.Errorf("idle working line must be empty, got %q", got)
	}
}

func TestWorkingLineVerbs(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *TranscriptModel)
		want  string
	}{
		{"thinking", func(m *TranscriptModel) { m.reasoning = true }, "Thinking"},
		{"writing", func(m *TranscriptModel) { m.streaming = true }, "Writing"},
		{"tool", func(m *TranscriptModel) {
			b := m.newBlockCard(blockToolCard, "")
			b.tool = &toolCard{tool: "Bash", status: toolRunning}
			m.blocks = append(m.blocks, b)
			m.pendingTools = append(m.pendingTools, 0)
		}, "Running Bash"},
		{"generic", func(m *TranscriptModel) {}, "Working"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := workingModel(t)
			tc.setup(m)
			out := stripANSI(m.workingLine())
			if !strings.Contains(out, tc.want) {
				t.Errorf("working line verb: got %q, want it to contain %q", out, tc.want)
			}
			if !strings.Contains(out, "esc to interrupt") {
				t.Errorf("working line must spell out the interrupt affordance, got %q", out)
			}
		})
	}
}

// A queued prompt flips the esc affordance to "steer" (esc sends it now).
func TestWorkingLineQueuedShowsSteer(t *testing.T) {
	m := workingModel(t)
	m.streaming = true
	m.queuedPrompt = "next thing"
	out := stripANSI(m.workingLine())
	if !strings.Contains(out, "esc to steer") {
		t.Errorf("queued prompt must show \"esc to steer\", got %q", out)
	}
	if strings.Contains(out, "esc to interrupt") {
		t.Errorf("queued prompt must NOT show \"esc to interrupt\", got %q", out)
	}
}

// While replaying history the line honestly says "loading transcript", not a live
// working verb (a replayed turn must not masquerade as the model running).
func TestWorkingLineReplayingShowsLoading(t *testing.T) {
	m := workingModel(t)
	m.replaying = true
	out := stripANSI(m.workingLine())
	if !strings.Contains(out, "loading transcript") {
		t.Errorf("replaying must show the loading indicator, got %q", out)
	}
}

// The working indicator is a single line (it occupies exactly one layout band).
func TestWorkingLineIsSingleLine(t *testing.T) {
	m := workingModel(t)
	m.reasoning = true
	if strings.Contains(m.workingLine(), "\n") {
		t.Error("working line must be exactly one row (regionWorking is 1 row)")
	}
}

// --------------------------------------------------------------------------
// ITEM 3 — collapsed status line
// --------------------------------------------------------------------------

// By default the status line is ONE quiet row: no permanent second gauge row.
func TestStatusLineCollapsedToOneRow(t *testing.T) {
	m := newStatusModel(200000, 1000) // ~0.5% ctx, no rate data, no cost
	m.width = 100
	if h := m.statusLineHeight(); h != 1 {
		t.Errorf("default status line height = %d, want 1", h)
	}
	if strings.Contains(m.renderStatusLine(), "\n") {
		t.Errorf("default status line must be a single row: %q", stripANSI(m.renderStatusLine()))
	}
}

// A rate_limit.updated makes the status line transiently grow to a second row,
// which then disappears (it is not permanent).
func TestStatusLineTransientRateRow(t *testing.T) {
	base := goldenFixedNow
	withFixedNow(t, base, func() {
		m := newStatusModel(200000, 1000)
		m.width = 100
		m.rlSeen, m.rlAvailable = true, true
		m.rl5hUtil, m.rl7dUtil = 42, 18
		m.rlUpdatedAt = base // just updated → within the transient window
		if h := m.statusLineHeight(); h != 2 {
			t.Fatalf("status line height right after update = %d, want 2", h)
		}
		if !strings.Contains(m.renderStatusLine(), "\n") {
			t.Error("transient rate-limit row must render as a second line")
		}
		// After the window it collapses back to one row (no permanent gauge block).
		m.rlUpdatedAt = base.Add(-2 * rlTransientWindow)
		if h := m.statusLineHeight(); h != 1 {
			t.Errorf("status line must collapse after the transient window, height = %d, want 1", h)
		}
	})
}

// The ⚠ bypass chip stays visible in the collapsed row even when width is tight
// enough to shed the optional segments (§2d: yolo is never invisible).
func TestStatusLineBypassChipSurvivesNarrowWidth(t *testing.T) {
	m := newStatusModel(200000, 1000)
	m.mode = modeBypass
	m.Model = "claude-opus-4-8"
	m.Branch = "feature/really-long-branch-name"
	m.TotalCostUSD = 5.0
	m.width = 34
	out := stripANSI(m.renderStatusLine())
	if !strings.Contains(out, "⚠ bypass") {
		t.Errorf("bypass chip must survive a narrow width, got %q", out)
	}
}

// The collapsed row is width-safe by construction: it never exceeds the frame
// width, even loaded with every optional segment at a narrow width.
func TestStatusLineWidthSafe(t *testing.T) {
	for _, w := range []int{20, 30, 50, 80} {
		m := newStatusModel(200000, 180000) // high ctx so the gauge is present
		m.mode = modeBypass
		m.Model = "claude-opus-4-8"
		m.Branch = "main"
		m.Dirty = true
		m.TotalCostUSD = 12.34
		m.effortOverride = "max"
		m.syncStatus = "stalled"
		m.width = w
		row1 := strings.SplitN(m.renderStatusLine(), "\n", 2)[0]
		if got := lipgloss.Width(row1); got > w {
			t.Errorf("width=%d: status row width %d exceeds frame (not width-safe)", w, got)
		}
	}
}

// §2c fix (c): the chat status line hides the ctx gauge when the model's limit is
// unknown (matching the dashboard), rather than assuming a 200k window.
func TestStatusLineCtxHiddenWhenLimitUnknown(t *testing.T) {
	m := newStatusModel(0, 180000) // unknown limit, many tokens
	m.width = 100
	if out := stripANSI(m.renderStatusLine()); strings.Contains(out, "%") {
		t.Errorf("unknown ctx limit must hide the gauge (no %%), got %q", out)
	}
}

// The dashboard's matching surface: CtxPercent is 0 (→ gauge hidden by the
// pct>0 gate) when the limit is unknown, so the two surfaces agree.
func TestDashboardCtxHiddenWhenLimitUnknown(t *testing.T) {
	s := Session{}
	s.CtxLimit = 0
	s.InputTokens = 180000
	if pct := s.CtxPercent(); pct != 0 {
		t.Errorf("unknown ctx limit must yield CtxPercent 0 (gauge hidden), got %d", pct)
	}
}

// The transient rate row is fully populated with real data (no fabricated
// values) and driven straight from a rate_limit.updated event.
func TestStatusLineTransientRowFromEvent(t *testing.T) {
	base := goldenFixedNow
	withFixedNow(t, base, func() {
		m := newStatusModel(200000, 1000)
		m.width = 120
		payload := session.RateLimitPayload{
			Available:        true,
			FiveHourUtil:     42,
			FiveHourResetsAt: base.Add(time.Hour).Format(time.RFC3339),
			SevenDayUtil:     18,
		}
		m.handleEvent(mkEvent(session.EventRateLimitUpdated, payload))
		if !m.rateRowVisible() {
			t.Fatal("a rate_limit.updated event must make the transient row visible")
		}
		out := stripANSI(m.renderStatusLine())
		if !strings.Contains(out, "42%") || !strings.Contains(out, "18%") {
			t.Errorf("transient row missing real utilization, got %q", out)
		}
	})
}
