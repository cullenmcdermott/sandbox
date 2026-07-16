package dashboard

// permprompt_component_test.go — §2a permPrompt component contract: the tool box
// and plan card render behind one Render/Height/HandleKey surface with a cached
// static body and a live appear-fade border. These pin the consolidation's load-
// bearing invariants (byte output stays golden'd by golden_test.go /
// golden_adverse_test.go): Height agrees with Render, Height is fade-stable, the
// static body caches by identity, and HandleKey decides the same actions the old
// inline code did.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// toolPrompt returns a transcript with a pending Edit permission (with a diff,
// diff expanded) whose appear fade is fully elapsed.
func toolPrompt(t *testing.T) *TranscriptModel {
	t.Helper()
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.pending = &transcriptPermission{
		id:        "p1",
		tool:      "Edit",
		arg:       "main.go",
		adds:      2,
		dels:      1,
		diffLines: []string{"+ added line", "− removed line", "  context"},
		since:     nowFunc().Add(-time.Hour),
	}
	m.showDiff = true
	return m
}

// planPrompt returns a transcript with a pending ExitPlanMode plan card.
func planPrompt(t *testing.T) *TranscriptModel {
	t.Helper()
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.pending = &transcriptPermission{
		id:     "pl",
		tool:   "ExitPlanMode",
		isPlan: true,
		plan:   "Step 1: do the thing.\nStep 2: verify it.\nStep 3: ship it.",
		since:  nowFunc().Add(-time.Hour),
	}
	return m
}

// planModel drives a plan permission in through the reducer (isPlan set by the
// ExitPlanMode branch) with its grace gate already elapsed, so plan keys act
// immediately through handleKey.
func planModel(t *testing.T) (*TranscriptModel, *fakeRunnerClient) {
	t.Helper()
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	m.handleEvent(session.Event{
		Type:    session.EventPermissionRequested,
		Payload: json.RawMessage(`{"permissionId":"p1","tool":"ExitPlanMode","input":{"plan":"do the thing"}}`),
	})
	if m.pending == nil || !m.pending.isPlan {
		t.Fatal("plan permission not registered as a plan card")
	}
	m.pending.since = nowFunc().Add(-2 * permissionGraceCap)
	return m, fc
}

// Height must equal the rendered box height for both variants across widths —
// the reserved band matches what Render draws.
func TestPermPromptHeightMatchesRender(t *testing.T) {
	for _, variant := range []struct {
		name string
		m    *TranscriptModel
	}{
		{"tool", toolPrompt(t)},
		{"plan", planPrompt(t)},
	} {
		p := variant.m.permComp()
		for _, w := range []int{50, 80, 120} {
			if got, want := p.Height(w), lipgloss.Height(p.Render(w)); got != want {
				t.Errorf("%s Height(%d) = %d, lipgloss.Height(Render) = %d", variant.name, w, got, want)
			}
		}
	}
}

// Height is invariant across the appear fade: the fade changes only the border
// color, never the geometry. Render bytes may differ (border), Height must not.
func TestPermPromptHeightStableAcrossFade(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "0")
	t.Setenv("NO_COLOR", "")
	old := nowFunc
	t.Cleanup(func() { nowFunc = old })
	base := time.Now()
	nowFunc = func() time.Time { return base }

	m := toolPrompt(t)
	m.pending.since = base // fade starts now
	p := m.permComp()

	const w = 80
	want := p.Height(w)
	early := ""
	late := ""
	for _, off := range []time.Duration{0, 40 * time.Millisecond, 80 * time.Millisecond, permissionAppearDur, 3 * permissionAppearDur} {
		nowFunc = func() time.Time { return base.Add(off) }
		if got := p.Height(w); got != want {
			t.Fatalf("Height drifted across the fade: at +%v got %d, want %d", off, got, want)
		}
		if off == 0 {
			early = p.Render(w)
		}
		late = p.Render(w)
	}
	// The border color genuinely animates, so the first and last frames differ
	// (Height stayed constant above — geometry is fade-stable, chrome is live).
	if early == late {
		t.Error("expected the appear-fade border to change the rendered bytes across the window")
	}
}

// The static body is cached by identity: a second Render at the same key does
// not rebuild it, while a width or selection change invalidates the cache.
func TestPermPromptBodyCache(t *testing.T) {
	m := toolPrompt(t)
	p := m.permComp()
	p.builds = 0

	_ = p.Render(80)
	_ = p.Render(80) // same key → cache hit
	if p.builds != 1 {
		t.Fatalf("body rebuilt %d times for two same-width renders, want 1", p.builds)
	}
	_ = p.Render(100) // width change → miss
	if p.builds != 2 {
		t.Fatalf("width change did not invalidate the body cache (builds=%d)", p.builds)
	}
	m.pending.sel = 1 // selection change → miss
	_ = p.Render(100)
	if p.builds != 3 {
		t.Fatalf("selection change did not invalidate the body cache (builds=%d)", p.builds)
	}
	// The invalidated render reflects the new selection (❯ on option 2).
	if !strings.Contains(stripANSI(p.Render(100)), "› 2.") {
		t.Errorf("re-rendered body missing the moved selection chevron:\n%s", stripANSI(p.Render(100)))
	}
}

// HandleKey decides the same actions the old inline code did — for the tool
// panel (nav / number / accelerators, grace gate) and the plan card
// (r / a / enter, with enter stepping to accept-edits) — driven through the real
// handleKey path.
func TestPermPromptHandleKeyParity(t *testing.T) {
	// Tool panel: HandleKey resolutions.
	m := toolPrompt(t)
	p := m.permComp()

	if act, handled := p.HandleKey("down", true); !handled || act.kind != permActNone || m.pending.sel != 1 {
		t.Errorf("down = (%+v, %v), sel=%d — want nav-only moving sel to 1", act, handled, m.pending.sel)
	}
	if act, handled := p.HandleKey("2", true); !handled || act.kind != permActResolve || !act.allow || act.scope != "session" {
		t.Errorf(`"2" = (%+v, %v), want resolve allow session`, act, handled)
	}
	if act, handled := p.HandleKey("a", true); !handled || act.kind != permActResolve || !act.allow || act.scope != "once" {
		t.Errorf(`"a" = (%+v, %v), want resolve allow once`, act, handled)
	}
	if act, handled := p.HandleKey("d", true); !handled || act.kind != permActResolve || act.allow {
		t.Errorf(`"d" = (%+v, %v), want resolve deny`, act, handled)
	}
	// Grace-gated resolving key is consumed but swallowed (no decision).
	if act, handled := p.HandleKey("1", false); !handled || act.kind != permActNone {
		t.Errorf(`"1" (grace-gated) = (%+v, %v), want consumed no-op`, act, handled)
	}
	// ctrl+o toggles the diff (Edit has a diff); j falls through to scrolling.
	if act, handled := p.HandleKey("ctrl+o", true); !handled || act.kind != permActToggleDiff {
		t.Errorf(`"ctrl+o" = (%+v, %v), want toggle diff`, act, handled)
	}
	if _, handled := p.HandleKey("j", true); handled {
		t.Error(`"j" should fall through to transcript scrolling, not the panel`)
	}

	// Plan card: r rejects, a approves staying in mode, enter approves & switches
	// to accept-edits — driven end-to-end through handleKey.
	mp, fc := planModel(t)
	drain(t, mp, "r")
	if len(fc.resolved) != 1 || fc.resolved[0].Allow {
		t.Errorf("plan r → %+v, want deny", fc.resolved)
	}

	mp, fc = planModel(t)
	drain(t, mp, "a")
	if len(fc.resolved) != 1 || !fc.resolved[0].Allow || fc.resolved[0].Scope != "once" {
		t.Errorf("plan a → %+v, want allow once", fc.resolved)
	}
	if mp.mode == modeAcceptEdits {
		t.Error("plan a (approve · stay) must not switch to accept-edits")
	}

	mp, fc = planModel(t)
	drain(t, mp, "enter")
	if len(fc.resolved) != 1 || !fc.resolved[0].Allow {
		t.Errorf("plan enter → %+v, want allow", fc.resolved)
	}
	if mp.mode != modeAcceptEdits {
		t.Errorf("plan enter (approve & build) must switch to accept-edits, mode=%v", mp.mode)
	}
}
