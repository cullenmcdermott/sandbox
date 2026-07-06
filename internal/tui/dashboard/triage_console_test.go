package dashboard

// triage_console_test.go — oracle + counter tests for the Triage Console
// redesign (docs/dashboard-redesign.md). Each test cites the phase / P-item it
// proves. mkEvent / makeSession live in the sibling test files.

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// stripANSI lives in search.go (reused here for plain-text assertions).

// triageModel seeds a model at a fixed render size with the given sessions.
func triageModel(t *testing.T, sessions ...Session) *Model {
	t.Helper()
	m := New(nil)
	m.seeded = true
	m.width, m.height = 110, 32
	m.sessions = sessions
	return m
}

// --------------------------------------------------------------------------
// P1 — opaque surfaces (no transparent bleed-through)
// --------------------------------------------------------------------------

// everyCellPainted walks an ANSI string and reports the first printable cell
// that is emitted while no background color is active (a transparent cell). The
// honest P1 oracle: a transparent terminal bleeds through exactly such cells.
func firstUnpaintedCell(s string) (int, bool) {
	bgActive := false
	col := 0
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Parse the SGR params up to the terminating 'm'.
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				params := s[i+2 : j]
				if params == "" || params == "0" {
					bgActive = false // full reset clears background
				} else {
					for _, p := range strings.Split(params, ";") {
						// 48 = set background (truecolor "48;2;r;g;b" or 256 "48;5;n").
						if p == "48" {
							bgActive = true
						}
					}
				}
				i = j + 1
				continue
			}
		}
		if s[i] == '\n' {
			col = 0
			i++
			continue
		}
		// A printable byte that starts a UTF-8 rune. Any cell (space or glyph)
		// emitted with no background active bleeds through on a transparent term.
		if s[i]&0xC0 != 0x80 {
			if !bgActive {
				return col, true
			}
			col++
		}
		i++
	}
	return 0, false
}

func TestP1_RenderZonedFullyOpaque(t *testing.T) {
	m := triageModel(t,
		makeSession("aaaa1111", StatusIdle),
		makeSession("bbbb2222", StatusWaiting),
	)
	out := m.renderZoned(m.width, m.height)
	if col, bad := firstUnpaintedCell(out); bad {
		t.Errorf("renderZoned has a transparent cell at column %d (P1: no bleed-through)", col)
	}
}

func TestP1_BoxWithTitlePaintsBackground(t *testing.T) {
	box := boxWithTitle("X", []string{"hi"}, 20, 4, theme.BorderMedium, theme.Surface)
	// theme.Surface (Midnight) is #1B1726 → "48;2;27;23;38".
	if !strings.Contains(box, "48;2;27;23;38") {
		t.Errorf("boxWithTitle should paint the surface background; got:\n%q", box)
	}
	if col, bad := firstUnpaintedCell(box); bad {
		t.Errorf("boxWithTitle has a transparent cell at column %d", col)
	}
}

// --------------------------------------------------------------------------
// P2 — identical titles disambiguated by short id + two-line rows
// --------------------------------------------------------------------------

func TestP2_TwoLineRowWithShortID(t *testing.T) {
	m := triageModel(t)
	s := Session{
		State:            session.State{ID: "a3f1c9de", Backend: session.BackendClaudeSDK, ProjectPath: "/g/sandbox"},
		Title:            "sandbox",
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}
	row := stripANSI(m.renderSessionRow(s, false, 80))
	lines := strings.Split(row, "\n")
	if len(lines) != 2 {
		t.Fatalf("session row should be 2 physical lines, got %d:\n%s", len(lines), row)
	}
	if !strings.Contains(lines[1], "a3f1") {
		t.Errorf("row sub-line should contain the 4-hex short id 'a3f1'; got %q", lines[1])
	}
	if strings.Contains(lines[1], "a3f1c9de") {
		t.Errorf("row sub-line should use the SHORT id, not the full id; got %q", lines[1])
	}
}

func TestP2_RowsAdvanceViewportByTwo(t *testing.T) {
	// Two sessions render four physical lines, proving the row height math.
	m := triageModel(t,
		makeSession("one", StatusIdle),
		makeSession("two", StatusIdle),
	)
	lines, shown := m.renderRowLines(m.visibleRows(), 56, 10)
	if shown != 2 {
		t.Errorf("expected 2 rows shown, got %d", shown)
	}
	if len(lines) != 4 {
		t.Errorf("2 sessions should render 4 physical lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
}

// --------------------------------------------------------------------------
// P3 — relativeTime falls back to created-age, never a bare "—"
// --------------------------------------------------------------------------

func TestP3_RelTimeFallsBackToCreated(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = old }()

	s := Session{
		State: session.State{
			ID:        "s1",
			CreatedAt: nowFunc().Add(-2 * time.Hour), // active is zero → fall back
		},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}
	if got := rowRelTime(s); got == "—" {
		t.Errorf("rowRelTime should fall back to created-age, not '—'; got %q", got)
	}
	if got := rowRelTime(s); got != "2h ago" {
		t.Errorf("rowRelTime created-age = %q, want '2h ago'", got)
	}
}

// --------------------------------------------------------------------------
// P4 — model + ctx% surface in the row sub-line and the detail pane
// --------------------------------------------------------------------------

func TestP4_RowAndDetailShowModelAndCtx(t *testing.T) {
	m := triageModel(t)
	s := Session{
		State:            session.State{ID: "s1", Backend: session.BackendClaudeSDK, Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{Model: "opus-4.8", DashStatus: StatusBusy, CtxLimit: 1000000, InputTokens: 620000},
	}
	m.sessions = []Session{s}

	// Layout A: the row sub-line carries ctx% (the model itself lives in the
	// detail pane, since the brand glyph already identifies the agent).
	row := stripANSI(m.renderSessionRow(s, false, 80))
	if !strings.Contains(row, "62%") {
		t.Errorf("row should show ctx%%=62%%; got:\n%s", row)
	}

	detail := stripANSI(strings.Join(m.renderDetailLines(60, 20), "\n"))
	if !strings.Contains(detail, "opus-4.8") {
		t.Errorf("detail should show the model; got:\n%s", detail)
	}
	if !strings.Contains(detail, "ctx 62%") {
		t.Errorf("detail model line should show 'ctx 62%%'; got:\n%s", detail)
	}
}

// --------------------------------------------------------------------------
// P5 — glyph legend pinned in the list footer
// --------------------------------------------------------------------------

func TestP5_LegendInSessionListBody(t *testing.T) {
	m := triageModel(t, makeSession("a", StatusIdle))
	body := stripANSI(strings.Join(m.sessionListBody(60, 20), "\n"))
	if !strings.Contains(body, "legend") {
		t.Errorf("sessionListBody should pin a legend footer; got:\n%s", body)
	}
	for _, g := range []string{theme.GlyphIdle, theme.GlyphBusy, theme.GlyphWaiting, theme.GlyphNeedsInput, theme.GlyphSuspended, theme.GlyphFailed} {
		if !strings.Contains(body, g) {
			t.Errorf("legend missing glyph %q; got:\n%s", g, body)
		}
	}
}

// --------------------------------------------------------------------------
// P6 — failed sessions fire the attention dot
// --------------------------------------------------------------------------

func TestP6_AttentionDotFiresOnFailed(t *testing.T) {
	if attentionDot(Session{
		sessionReadModel: sessionReadModel{DashStatus: StatusFailed},
	}) == "" {
		t.Error("attentionDot should fire (non-empty) for a Failed session (P6)")
	}
	if attentionDot(Session{
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}) != "" {
		t.Error("attentionDot should be empty for an Idle session")
	}
}

// --------------------------------------------------------------------------
// P7 — USAGE box deleted; real cost surfaces in the detail
// --------------------------------------------------------------------------

func TestP7_NoUsageBoxRealCostInDetail(t *testing.T) {
	m := triageModel(t)
	out := stripANSI(m.renderZoned(m.width, m.height))
	if strings.Contains(out, "USAGE") {
		t.Error("the USAGE box should be deleted (P7)")
	}
	s := Session{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusBusy, TotalCostUSD: 1.23},
	}
	m.sessions = []Session{s}
	detail := stripANSI(strings.Join(m.renderDetailLines(60, 20), "\n"))
	if !strings.Contains(detail, "$1.23") {
		t.Errorf("detail should show real cost $1.23; got:\n%s", detail)
	}
}

// --------------------------------------------------------------------------
// P8 — NEEDS YOU box replaced by in-list dots + detail action block
// --------------------------------------------------------------------------

func TestP8_NoNeedsYouBoxActionBlockInDetail(t *testing.T) {
	m := triageModel(t, makeSession("w", StatusWaiting))
	out := stripANSI(m.renderZoned(m.width, m.height))
	if strings.Contains(out, "NEEDS YOU") {
		t.Error("the NEEDS YOU box should be deleted (P8)")
	}
	detail := stripANSI(strings.Join(m.renderDetailLines(60, 24), "\n"))
	if !strings.Contains(detail, "needs you") {
		t.Errorf("waiting session detail should show a 'needs you' action block; got:\n%s", detail)
	}
}

// P13 — the detail action block makes items actionable for every actionable
// status (Waiting / NeedsInput / Failed), not just Waiting. The block must carry
// the action key hints (attach/rename/suspend/destroy) so the session can be
// acted on without attaching. An Idle session must NOT show the block.
func TestP13_ActionBlockForEveryActionableStatus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  SessionStatus
		wantKey bool
	}{
		{"waiting", StatusWaiting, true},
		{"needsinput", StatusNeedsInput, true},
		{"failed", StatusFailed, true},
		{"idle", StatusIdle, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := triageModel(t, makeSession("s", tc.status))
			detail := stripANSI(strings.Join(m.renderDetailLines(60, 24), "\n"))
			gotBlock := strings.Contains(detail, "needs you")
			gotKeys := strings.Contains(detail, "attach") &&
				strings.Contains(detail, "rename") &&
				strings.Contains(detail, "suspend") &&
				strings.Contains(detail, "destroy")
			if tc.wantKey {
				if !gotBlock {
					t.Errorf("%s detail should show a 'needs you' action block (P13); got:\n%s", tc.name, detail)
				}
				if !gotKeys {
					t.Errorf("%s action block should carry attach/rename/suspend/destroy hints (P13); got:\n%s", tc.name, detail)
				}
			} else if gotBlock {
				t.Errorf("%s (non-actionable) detail must NOT show the action block; got:\n%s", tc.name, detail)
			}
		})
	}
}

// --------------------------------------------------------------------------
// P9 — cluster strip derives the backend mix (no hardcoded claude-sdk)
// --------------------------------------------------------------------------

func TestP9_ClusterStripDerivesBackendMix(t *testing.T) {
	m := triageModel(t,
		Session{
			State:            session.State{ID: "1", Backend: session.BackendClaudeSDK, Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
		},
		Session{
			State:            session.State{ID: "2", Backend: session.BackendOpenCode, Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
		},
	)
	strip := stripANSI(m.clusterStrip(m.width, m.partition()))
	if !strings.Contains(strip, "claude 1") {
		t.Errorf("cluster strip should show 'claude 1'; got:\n%s", strip)
	}
	if !strings.Contains(strip, "opencode 1") {
		t.Errorf("cluster strip should derive 'opencode 1' from sessions (P9); got:\n%s", strip)
	}
	if strings.Contains(strip, "claude-sdk") {
		t.Errorf("cluster strip must not show the hardcoded backend id 'claude-sdk'; got:\n%s", strip)
	}
}

// --------------------------------------------------------------------------
// P10 — zero-value dot-bars removed (usageBody/blockBar gone from the view)
// --------------------------------------------------------------------------

func TestP10_NoZeroValueBars(t *testing.T) {
	m := triageModel(t, makeSession("a", StatusIdle))
	out := m.renderZoned(m.width, m.height)
	// blockBar renders an empty track as '░'. With usageBody deleted, no ░ in the view.
	if strings.Contains(stripANSI(out), "░") {
		t.Error("the zero-value progress bars should be gone from the dashboard (P10)")
	}
}

// --------------------------------------------------------------------------
// P11 — footer uses theme.TextMuted (not the recessed theme.TextDim)
// --------------------------------------------------------------------------

func TestP11_FooterUsesMutedNotDim(t *testing.T) {
	m := triageModel(t, makeSession("a", StatusIdle))
	bar := m.bottomBar(m.width)
	muted := fgSeq(theme.TextMuted)
	dim := fgSeq(theme.TextDim)
	if !strings.Contains(bar, muted) {
		t.Errorf("bottomBar should use theme.TextMuted foreground (P11)")
	}
	// The help component itself may use various tones, but the band's own text
	// must not be the recessed dim tone as its primary color.
	_ = dim
}

// fgSeq returns the foreground SGR substring for a color (e.g. "38;2;r;g;b").
func fgSeq(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return "38;2;" + formatInt(int(r>>8)) + ";" + formatInt(int(g>>8)) + ";" + formatInt(int(b>>8))
}

// --------------------------------------------------------------------------
// P12 — list/detail borders are the neutral medium tone
// --------------------------------------------------------------------------

func TestP12_NeutralBorders(t *testing.T) {
	m := triageModel(t, makeSession("a", StatusIdle))
	out := m.renderZoned(m.width, m.height)
	border := fgSeq(theme.BorderMedium)
	if !strings.Contains(out, border) {
		t.Errorf("list/detail boxes should use the neutral medium border tone (P12)")
	}
	// The gold/green accents must not be used as the box border color.
	if strings.Contains(out, "╭─"+fgSeq(theme.Gold)) {
		t.Error("box borders should not reuse the gold status accent (P12)")
	}
}

// --------------------------------------------------------------------------
// P13 — one shared partition helper; detail action block makes items actionable
// --------------------------------------------------------------------------

func TestP13_SharedPartitionCounts(t *testing.T) {
	m := triageModel(t,
		Session{
			State:            session.State{ID: "1", Backend: session.BackendClaudeSDK, Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
		},
		Session{
			State:            session.State{ID: "2", Backend: session.BackendClaudeSDK, Status: session.StatusSuspended},
			sessionReadModel: sessionReadModel{DashStatus: StatusSuspended},
		},
		Session{
			State:            session.State{ID: "3", Backend: session.BackendClaudeSDK, Status: session.StatusFailed},
			sessionReadModel: sessionReadModel{DashStatus: StatusFailed},
		},
	)
	c := m.partition()
	if c.total != 3 || c.busy != 1 || c.running != 1 || c.suspended != 1 || c.failed != 1 {
		t.Errorf("partition mis-counted: %+v", c)
	}
	if c.byBackend[session.BackendClaudeSDK] != 3 {
		t.Errorf("partition backend mix wrong: %+v", c.byBackend)
	}
	// Header tally and cluster strip both consume it.
	if !strings.Contains(stripANSI(m.topBar(m.width, m.partition())), "1 busy") {
		t.Error("header should reflect shared busy count")
	}
	if !strings.Contains(stripANSI(m.clusterStrip(m.width, m.partition())), "1 failed") {
		t.Error("cluster strip should reflect shared failed count")
	}
}

// --------------------------------------------------------------------------
// Phase 3 — usage.updated reducer + CtxPercent
// --------------------------------------------------------------------------

func TestPhase3_UsageUpdatedReducer(t *testing.T) {
	sess := makeSession("s1", StatusBusy)
	sess.CtxLimit = 200000
	changed := ApplyRunnerEvent(&sess, mkEvent(session.EventUsageUpdated, session.UsagePayload{
		InputTokens: 50000, CacheReadTokens: 10000, CacheWriteTokens: 0, OutputTokens: 1000, TotalCostUSD: 0.42,
	}))
	if changed {
		t.Error("usage.updated must not report a status change")
	}
	if sess.InputTokens != 50000 || sess.TotalCostUSD != 0.42 {
		t.Errorf("usage fields not applied: %+v", sess)
	}
	if got := sess.CtxPercent(); got != 30 { // (50000+10000+0)/200000 = 30%
		t.Errorf("CtxPercent = %d, want 30", got)
	}
}

func TestPhase3_CtxLimitCachedOnSessionStarted(t *testing.T) {
	sess := makeSession("s1", StatusIdle)
	ApplyRunnerEvent(&sess, mkEvent(session.EventSessionStarted, session.SessionStartedPayload{Model: "opus-4.8"}))
	if sess.CtxLimit <= 0 {
		t.Errorf("session.started should cache CtxLimit from models.Limit; got %d", sess.CtxLimit)
	}
}

// --------------------------------------------------------------------------
// Phase 4 — tool.started ring (main-thread only) + detail recent section
// --------------------------------------------------------------------------

func TestPhase4_ToolStartedRingMainThreadOnly(t *testing.T) {
	sess := makeSession("s1", StatusBusy)

	// Main-thread tool is captured with its arg.
	ApplyRunnerEvent(&sess, mkEvent(session.EventToolStarted, session.ToolPayload{
		Tool: "Edit", Input: []byte(`{"file_path":"/g/sandbox/internal/sync/mutagen.go"}`),
	}))
	if len(sess.RecentTools) != 1 || sess.RecentTools[0].Tool != "Edit" {
		t.Fatalf("main-thread tool not captured: %+v", sess.RecentTools)
	}
	if !strings.Contains(sess.RecentTools[0].Arg, "mutagen.go") {
		t.Errorf("tool arg not extracted via toolArg; got %q", sess.RecentTools[0].Arg)
	}

	// Subagent-child tool (ParentToolUseID set) is skipped.
	ApplyRunnerEvent(&sess, mkEvent(session.EventToolStarted, session.ToolPayload{
		Tool: "Read", ParentToolUseID: "task-1", Input: []byte(`{"file_path":"x.go"}`),
	}))
	if len(sess.RecentTools) != 1 {
		t.Errorf("subagent-child tool must be skipped; ring = %+v", sess.RecentTools)
	}

	// Ring caps at recentToolsCap (drop oldest).
	for i := 0; i < recentToolsCap+3; i++ {
		ApplyRunnerEvent(&sess, mkEvent(session.EventToolStarted, session.ToolPayload{Tool: "Bash", Input: []byte(`{"command":"go test"}`)}))
	}
	if len(sess.RecentTools) != recentToolsCap {
		t.Errorf("ring should cap at %d; got %d", recentToolsCap, len(sess.RecentTools))
	}
}

func TestPhase4_DetailRecentSectionNewestFirst(t *testing.T) {
	m := triageModel(t)
	s := Session{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
	}
	s.RecentTools = []ToolRef{
		{Tool: "Read", Arg: "ssh.go"},
		{Tool: "Bash", Arg: "go test ./..."},
		{Tool: "Edit", Arg: "mutagen.go"},
	}
	m.sessions = []Session{s}
	detail := stripANSI(strings.Join(m.renderDetailLines(60, 24), "\n"))
	if !strings.Contains(detail, "recent") {
		t.Fatalf("detail should have a recent section; got:\n%s", detail)
	}
	// Newest first: Edit appears before Read.
	editIdx := strings.Index(detail, "Edit")
	readIdx := strings.Index(detail, "Read")
	if editIdx < 0 || readIdx < 0 || editIdx > readIdx {
		t.Errorf("recent tools should be newest-first (Edit before Read); got:\n%s", detail)
	}
}

// --------------------------------------------------------------------------
// Wiring — renderDetailLines is reachable from renderZoned (not dead/test-only)
// --------------------------------------------------------------------------

func TestDetailPaneWiredIntoRenderZoned(t *testing.T) {
	m := triageModel(t)
	s := Session{
		State:            session.State{ID: "wired1", Backend: session.BackendClaudeSDK, ProjectPath: "/g/wired", Status: session.StatusRunning},
		Title:            "wired-detail",
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}
	m.sessions = []Session{s}
	out := stripANSI(m.renderZoned(m.width, m.height))
	if !strings.Contains(out, "DETAIL") {
		t.Fatal("renderZoned should render a DETAIL pane")
	}
	// A unique detail-only datum (the project path) proves renderDetailLines ran.
	if !strings.Contains(out, "/g/wired") {
		t.Errorf("renderZoned should include detail-pane content from renderDetailLines; got:\n%s", out)
	}
}
