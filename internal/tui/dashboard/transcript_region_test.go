package dashboard

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// §2a: the vertical layout is one declarative region stack (liveLayout /
// previewLayout) that every consumer walks — render, body sizing, and the
// scrollbar hit-test. These tests pin the invariants that make that safe: the
// flex body absorbs exactly the slack the fixed bands leave, the bands tile the
// frame with no gap when there is room, the composited modal content is always
// exactly the frame height, and the body's top/height agree between the region
// model, the list widget, and the hit-test.

// fixedSum totals every non-body band and asserts the body band appears exactly
// once with no negative heights.
func fixedSum(t *testing.T, v vlayout) int {
	t.Helper()
	fixed := 0
	bodies := 0
	for _, r := range v.regions {
		if r.height < 0 {
			t.Errorf("band %q has negative height %d", r.name, r.height)
		}
		if r.name == regionBody {
			bodies++
			continue
		}
		fixed += r.height
	}
	if bodies != 1 {
		t.Errorf("expected exactly one body band, got %d", bodies)
	}
	return fixed
}

func sumRegions(v vlayout) int {
	total := 0
	for _, r := range v.regions {
		total += r.height
	}
	return total
}

type layoutCase struct {
	name  string
	w, h  int
	setup func(m *TranscriptModel)
	roomy bool // fixed bands fit with room to spare, so the bands tile exactly
}

func layoutCases() []layoutCase {
	return []layoutCase{
		{"plain", 80, 24, nil, true},
		{"tiny", 24, 6, nil, false},
		{"degenerate", 10, 3, nil, false},
		{"pending-permission", 80, 24, func(m *TranscriptModel) {
			m.pending = &transcriptPermission{id: "p1", tool: "Edit", arg: "a.go",
				diffLines: []string{"+foo", "−bar", " baz"}, since: nowFunc()}
		}, true},
		{"plan-card", 80, 24, func(m *TranscriptModel) {
			m.pending = &transcriptPermission{id: "p2", tool: "ExitPlanMode", isPlan: true,
				plan: "step one\nstep two\nstep three", since: nowFunc()}
		}, true},
		// The slash palette renders the full command menu, which is taller than a
		// 24-row terminal — the body floors at 1 and fitModal truncates it. This is
		// a "content taller than frame" case, not a roomy one.
		{"palette-open", 80, 24, func(m *TranscriptModel) { m.input.SetValue("/") }, false},
		{"search-open", 80, 24, func(m *TranscriptModel) { m.openSearch() }, true},
		{"everything-roomy", 60, 30, func(m *TranscriptModel) {
			m.pending = &transcriptPermission{id: "p3", tool: "Bash", arg: "ls -la", since: nowFunc()}
			m.openSearch()
			m.input.SetValue("multi\nline\ninput") // grows the composer
		}, true},
	}
}

func newLayoutModel(tc layoutCase) *TranscriptModel {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = tc.w, tc.h
	if tc.setup != nil {
		tc.setup(m)
	}
	m.layout()
	return m
}

// TestLiveLayoutFlexBodyArithmetic is the always-true invariant: the body band
// height is exactly max(1, total - fixedBands), and vlayout.total is the frame
// height. This is the arithmetic every consumer relies on.
func TestLiveLayoutFlexBodyArithmetic(t *testing.T) {
	for _, tc := range layoutCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := newLayoutModel(tc)
			v := m.liveLayout()

			fixed := fixedSum(t, v)
			wantBody := m.height - fixed
			if wantBody < 1 {
				wantBody = 1
			}
			if got := v.heightOf(regionBody); got != wantBody {
				t.Errorf("body band = %d, want max(1, %d-%d) = %d", got, m.height, fixed, wantBody)
			}
			if v.total != m.height {
				t.Errorf("vlayout.total = %d, want %d", v.total, m.height)
			}
			// The list widget layout() sized must match the region model.
			if got := m.body.Height(); got != v.heightOf(regionBody) {
				t.Errorf("list widget height %d != body band %d", got, v.heightOf(regionBody))
			}
		})
	}
}

// TestLiveLayoutTilesWhenRoomy: when the fixed bands fit, the flex body absorbs
// the slack so the bands tile the frame exactly — sum == total, no gap, no
// overflow. (Undersized frames overflow by design and are truncated by fitModal;
// see TestModalContentAlwaysExactHeight.)
func TestLiveLayoutTilesWhenRoomy(t *testing.T) {
	for _, tc := range layoutCases() {
		if !tc.roomy {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			m := newLayoutModel(tc)
			v := m.liveLayout()
			if got := sumRegions(v); got != m.height {
				t.Errorf("roomy frame: bands sum to %d, want %d (a gap/overlap in the stack)", got, m.height)
			}
			// renderTranscript (pre-fitModal) is exactly the frame height here.
			if got := lipgloss.Height(m.renderTranscript(m.width, m.height)); got != m.height {
				t.Errorf("roomy frame renders %d rows, want %d", got, m.height)
			}
		})
	}
}

// TestModalContentAlwaysExactHeight: the real rendered contract — modalContent
// (renderTranscript + fitModal) is exactly the frame height in EVERY state,
// including undersized frames and an overflowing palette.
func TestModalContentAlwaysExactHeight(t *testing.T) {
	for _, tc := range layoutCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := newLayoutModel(tc)
			out := m.modalContent(m.width, m.height)
			if got := lipgloss.Height(out); got != m.height {
				t.Errorf("modalContent is %d rows, want exactly %d", got, m.height)
			}
		})
	}
}

// TestLiveLayoutBodyAgreesWithHitTest pins the cross-consumer invariant that
// motivated §2a: the region model's body top, the shared bodyTop() the scrollbar
// hit-test uses, and the list widget height are all the same numbers.
func TestLiveLayoutBodyAgreesWithHitTest(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	v := m.liveLayout()
	// §2c dropped the persistent header/divider in the normal state, so the body
	// now starts at row 0. The invariant this test guards is cross-consumer
	// agreement (bodyTop() == region top == list height), not the literal value.
	if got := v.top(regionBody); got != 0 {
		t.Errorf("body top = %d, want 0 (no persistent header)", got)
	}
	if got := m.bodyTop(); got != v.top(regionBody) {
		t.Errorf("bodyTop() = %d disagrees with region top %d", got, v.top(regionBody))
	}
	if got, want := m.body.Height(), v.heightOf(regionBody); got != want {
		t.Errorf("list widget height = %d, region body height = %d", got, want)
	}
}

// TestPermissionBandShiftsBodyNotFrame: opening a permission band steals rows
// from the flex body, never from the frame total, and never moves the body top
// (bands below the body must not shift it) — the regression the old hand-counted
// bodyTop=2 could silently break.
func TestPermissionBandShiftsBodyNotFrame(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	beforeBody := m.body.Height()

	m.pending = &transcriptPermission{id: "p1", tool: "Bash", arg: "make", since: nowFunc()}
	m.layout()
	afterBody := m.body.Height()

	if afterBody >= beforeBody {
		t.Errorf("permission band did not shrink the body: before=%d after=%d", beforeBody, afterBody)
	}
	v := m.liveLayout()
	if v.top(regionBody) != 0 {
		t.Errorf("body top drifted to %d with a permission open", v.top(regionBody))
	}
	if got := sumRegions(v); got != m.height {
		t.Errorf("with permission open, bands sum to %d, want %d", got, m.height)
	}
}

// TestPreviewLayoutRegionsTileFrame: the connect-preview stack (banner instead
// of composer) tiles the frame across banner heights, and its body top matches
// the live view (shared headerBands).
func TestPreviewLayoutRegionsTileFrame(t *testing.T) {
	for _, banner := range []string{
		"reconnecting…",
		"line one\nline two\nline three",
		strings.Repeat("x\n", 10) + "tail",
	} {
		m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
		m.width, m.height = 80, 24
		v := m.previewLayout(banner)
		if got := sumRegions(v); got != m.height {
			t.Errorf("preview bands sum to %d, want %d (banner %dh)", got, m.height, lipgloss.Height(banner))
		}
		if v.top(regionBody) != 0 {
			t.Errorf("preview body top = %d, want 0", v.top(regionBody))
		}
		if got := v.heightOf(regionBanner); got != lipgloss.Height(banner) {
			t.Errorf("banner band height = %d, want %d", got, lipgloss.Height(banner))
		}
		if got := lipgloss.Height(m.previewView(m.width, m.height, banner)); got != m.height {
			t.Errorf("preview view is %d rows, want %d", got, m.height)
		}
	}
}

// TestRegionAccessorsAbsentBand: heightOf/top return the zero/absent sentinels
// for a band that isn't in the frame, so consumers can probe optional bands.
func TestRegionAccessorsAbsentBand(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	v := m.liveLayout() // no permission, no palette, no search
	if got := v.heightOf(regionPerm); got != 0 {
		t.Errorf("absent perm band height = %d, want 0", got)
	}
	if got := v.top(regionPalette); got != -1 {
		t.Errorf("absent palette band top = %d, want -1", got)
	}
}
