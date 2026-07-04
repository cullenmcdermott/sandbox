package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// statusline_ratelimit_test.go — behavioral counters for TODO.md ② (real
// usage/reset data) and ① (in-session /model). The old status line fabricated
// the 5h/weekly windows (const fiveH,week=0.30,0.17) and projected reset times
// off time.Now(); these tests pin that the windows are now driven by real
// rate_limit.updated data and hidden (never fabricated) when unavailable.

// withFixedNow pins the injectable clock so reset countdowns are deterministic.
func withFixedNow(t *testing.T, base time.Time, fn func()) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return base }
	defer func() { nowFunc = old }()
	fn()
}

// ORACLE: a rate_limit.updated event populates the model's window state, so the
// status line can render real utilization + reset instants.
func TestRateLimitEventPopulatesModel(t *testing.T) {
	m := &TranscriptModel{}
	payload, err := json.Marshal(session.RateLimitPayload{
		Available:        true,
		FiveHourUtil:     42,
		FiveHourResetsAt: "2030-06-21T14:30:00Z",
		SevenDayUtil:     18,
		SevenDayResetsAt: "2030-06-24T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	m.handleEvent(session.Event{Type: session.EventRateLimitUpdated, Payload: payload})

	if !m.rlSeen || !m.rlAvailable {
		t.Fatalf("rlSeen=%v rlAvailable=%v, want both true", m.rlSeen, m.rlAvailable)
	}
	if m.rl5hUtil != 42 || m.rl7dUtil != 18 {
		t.Errorf("util 5h=%v 7d=%v, want 42/18", m.rl5hUtil, m.rl7dUtil)
	}
	want := time.Date(2030, 6, 21, 14, 30, 0, 0, time.UTC)
	if !m.rl5hReset.Equal(want) {
		t.Errorf("rl5hReset=%v, want %v", m.rl5hReset, want)
	}
}

// COUNTER: with real data, the status line shows the actual utilization and a
// reset countdown — NOT the old 30%/17% mock or a projected wall-clock date.
func TestStatusLineShowsRealRateLimits(t *testing.T) {
	base := time.Date(2030, 6, 21, 12, 0, 0, 0, time.UTC)
	withFixedNow(t, base, func() {
		m := &TranscriptModel{}
		m.rlSeen, m.rlAvailable = true, true
		m.rl5hUtil, m.rl7dUtil = 42, 18
		m.rl5hReset = base.Add(2*time.Hour + 13*time.Minute) // -> "2h13m"
		m.rl7dReset = base.Add(3 * 24 * time.Hour)           // -> "3d0h"
		out := stripANSI(m.renderStatusLine())

		if !strings.Contains(out, "42%") || !strings.Contains(out, "18%") {
			t.Errorf("status line missing real utilization 42%%/18%%: %q", out)
		}
		if !strings.Contains(out, "2h13m") {
			t.Errorf("status line missing 5h reset countdown 2h13m: %q", out)
		}
		if !strings.Contains(out, "3d0h") {
			t.Errorf("status line missing weekly reset countdown 3d0h: %q", out)
		}
		// The old mock constants must never appear again.
		if strings.Contains(out, "30%") || strings.Contains(out, "17%") {
			t.Errorf("status line still shows the old mock 30%%/17%%: %q", out)
		}
	})
}

// COUNTER: when plan limits don't apply, the windows are never shown as
// fabricated percentages. A headless (empty subscription) session names the
// reason — "usage n/a (headless auth)" — so the missing windows read as an
// auth-mode limitation rather than the bug-sounding bare blank / "unavailable".
func TestStatusLineHidesUnavailableRateLimits(t *testing.T) {
	m := &TranscriptModel{}
	m.rlSeen, m.rlAvailable = true, false // headless: rlSubscription stays ""
	out := stripANSI(m.renderStatusLine())
	if strings.Contains(out, "5h:") || strings.Contains(out, "weekly:") {
		t.Errorf("unavailable rate limits must not render a window row: %q", out)
	}
	if strings.Contains(out, "30%") || strings.Contains(out, "17%") {
		t.Errorf("unavailable rate limits must not fabricate percentages: %q", out)
	}
	if !strings.Contains(out, "n/a (headless auth)") {
		t.Errorf("headless session should explain the missing usage windows: %q", out)
	}
}

// ORACLE + COUNTER: subscriptionType rides through rate_limit.updated into the
// model, and the rendered reason distinguishes headless (empty subscription →
// "headless auth") from an unavailable-but-known-plan session (plain "n/a", no
// "headless" qualifier).
func TestStatusLineUnavailableReasonFromSubscription(t *testing.T) {
	// Headless setup-token: subscription empty → labelled "(headless auth)".
	headless, _ := json.Marshal(session.RateLimitPayload{Available: false})
	mh := &TranscriptModel{}
	mh.handleEvent(session.Event{Type: session.EventRateLimitUpdated, Payload: headless})
	if mh.rlSubscription != "" {
		t.Fatalf("headless rlSubscription = %q, want empty", mh.rlSubscription)
	}
	if out := stripANSI(mh.renderStatusLine()); !strings.Contains(out, "n/a (headless auth)") {
		t.Errorf("empty subscription should render '(headless auth)': %q", out)
	}

	// Unavailable but a plan is known (e.g. missing profile scope): plain "n/a".
	known, _ := json.Marshal(session.RateLimitPayload{Available: false, SubscriptionType: "max"})
	mk := &TranscriptModel{}
	mk.handleEvent(session.Event{Type: session.EventRateLimitUpdated, Payload: known})
	if mk.rlSubscription != "max" {
		t.Fatalf("rlSubscription = %q, want max", mk.rlSubscription)
	}
	out := stripANSI(mk.renderStatusLine())
	if !strings.Contains(out, "usage: n/a") || strings.Contains(out, "headless") {
		t.Errorf("known-plan-but-unavailable should render plain 'n/a' (no 'headless'): %q", out)
	}
}

// fptr is a *float64 literal helper for per-model window payloads.
func fptr(f float64) *float64 { return &f }

// ORACLE: per-model weekly windows populate the model state, and a non-nil
// pointer to 0 (present at 0%) is distinct from an absent window (nil).
func TestRateLimitPerModelWindowsPopulateModel(t *testing.T) {
	m := &TranscriptModel{}
	payload, err := json.Marshal(session.RateLimitPayload{
		Available:            true,
		SevenDayUtil:         18,
		SevenDayOpusUtil:     fptr(62),
		SevenDayOpusResetsAt: "2030-06-24T12:00:00Z",
		SevenDaySonnetUtil:   fptr(0), // present, but 0% used
		// no Sonnet reset
	})
	if err != nil {
		t.Fatal(err)
	}
	m.handleEvent(session.Event{Type: session.EventRateLimitUpdated, Payload: payload})

	if !m.rlOpusSeen || m.rlOpusUtil != 62 {
		t.Errorf("opus seen=%v util=%v, want true/62", m.rlOpusSeen, m.rlOpusUtil)
	}
	if !m.rlSonnetSeen || m.rlSonnetUtil != 0 {
		t.Errorf("sonnet seen=%v util=%v, want true/0 (present at 0%%)", m.rlSonnetSeen, m.rlSonnetUtil)
	}
	wantReset := time.Date(2030, 6, 24, 12, 0, 0, 0, time.UTC)
	if !m.rlOpusReset.Equal(wantReset) {
		t.Errorf("rlOpusReset=%v, want %v", m.rlOpusReset, wantReset)
	}

	// An event WITHOUT the per-model fields leaves them absent (nil pointer).
	bare, _ := json.Marshal(session.RateLimitPayload{Available: true, SevenDayUtil: 5})
	m2 := &TranscriptModel{}
	m2.handleEvent(session.Event{Type: session.EventRateLimitUpdated, Payload: bare})
	if m2.rlOpusSeen || m2.rlSonnetSeen {
		t.Errorf("absent per-model windows should leave seen=false, got opus=%v sonnet=%v", m2.rlOpusSeen, m2.rlSonnetSeen)
	}
}

// COUNTER: the status line surfaces the per-model weekly cap matching the
// attached model (Opus here) and never the other family's window. Switching the
// attached model to one without a per-model cap (Haiku) hides it entirely.
func TestStatusLineShowsActiveModelWindow(t *testing.T) {
	base := time.Date(2030, 6, 21, 12, 0, 0, 0, time.UTC)
	withFixedNow(t, base, func() {
		m := &TranscriptModel{}
		m.model = "claude-opus-4-8"
		m.rlSeen, m.rlAvailable = true, true
		m.rl5hUtil, m.rl7dUtil = 42, 18
		m.rlOpusSeen, m.rlOpusUtil = true, 62
		m.rlOpusReset = base.Add(2 * 24 * time.Hour) // -> "2d0h"
		m.rlSonnetSeen, m.rlSonnetUtil = true, 91    // present but must NOT show for an Opus session
		out := stripANSI(m.renderStatusLine())

		if !strings.Contains(out, "opus: 62%") {
			t.Errorf("status line missing active opus window 'opus: 62%%': %q", out)
		}
		if !strings.Contains(out, "opus in 2d0h") {
			t.Errorf("status line missing opus reset countdown 'opus in 2d0h': %q", out)
		}
		if strings.Contains(out, "91%") || strings.Contains(out, "sonnet") {
			t.Errorf("status line must not show the non-active sonnet window: %q", out)
		}

		// A model with no per-model cap (Haiku) hides the per-model row entirely.
		m.model = "claude-haiku-4-5"
		out = stripANSI(m.renderStatusLine())
		if strings.Contains(out, "opus:") || strings.Contains(out, "sonnet:") {
			t.Errorf("Haiku session must not show any per-model window: %q", out)
		}
	})
}

// COUNTER (symmetric to the Opus case): a Sonnet session shows the Sonnet
// per-model window — exercising activeModelWindow's sonnet branch — and never
// the Opus window. rlSonnetUtil=0 also pins that a window present at 0% renders
// as a visible row (the nil-vs-pointer-to-0 presence gate, at the render
// boundary rather than only in the reducer).
func TestStatusLineShowsSonnetActiveWindow(t *testing.T) {
	base := time.Date(2030, 6, 21, 12, 0, 0, 0, time.UTC)
	withFixedNow(t, base, func() {
		m := &TranscriptModel{}
		m.model = "claude-sonnet-4-6"
		m.rlSeen, m.rlAvailable = true, true
		m.rl5hUtil, m.rl7dUtil = 42, 18
		m.rlOpusSeen, m.rlOpusUtil = true, 62          // present but must NOT show for a Sonnet session
		m.rlSonnetSeen, m.rlSonnetUtil = true, 0       // present at 0% — must still render
		m.rlSonnetReset = base.Add(2 * 24 * time.Hour) // -> "2d0h"
		out := stripANSI(m.renderStatusLine())

		if !strings.Contains(out, "sonnet: 0%") {
			t.Errorf("sonnet-active session must show 'sonnet: 0%%' (present at 0%%): %q", out)
		}
		if !strings.Contains(out, "sonnet in 2d0h") {
			t.Errorf("missing sonnet reset countdown 'sonnet in 2d0h': %q", out)
		}
		if strings.Contains(out, "opus") || strings.Contains(out, "62%") {
			t.Errorf("sonnet session must not show the non-active opus window: %q", out)
		}
	})
}

// fmtReset formats a future countdown, the zero time, and a past instant.
func TestFmtReset(t *testing.T) {
	base := time.Date(2030, 6, 21, 12, 0, 0, 0, time.UTC)
	withFixedNow(t, base, func() {
		cases := []struct {
			in   time.Time
			want string
		}{
			{time.Time{}, "—"},
			{base.Add(-time.Hour), "now"},
			{base.Add(45 * time.Minute), "45m"},
			{base.Add(2*time.Hour + 13*time.Minute), "2h13m"},
			{base.Add(3*24*time.Hour + 4*time.Hour), "3d4h"},
		}
		for _, c := range cases {
			if got := fmtReset(c.in); got != c.want {
				t.Errorf("fmtReset(%v) = %q, want %q", c.in, got, c.want)
			}
		}
	})
}

// parseResetTime tolerates empty and malformed input by returning the zero time.
func TestParseResetTime(t *testing.T) {
	if got := parseResetTime(""); !got.IsZero() {
		t.Errorf("empty reset = %v, want zero", got)
	}
	if got := parseResetTime("not-a-time"); !got.IsZero() {
		t.Errorf("malformed reset = %v, want zero", got)
	}
	if got := parseResetTime("2030-06-21T14:30:00Z"); got.IsZero() {
		t.Errorf("valid reset parsed to zero")
	}
}

// ORACLE (① in-session /model): selecting /sonnet records a model override that
// is sent as TurnInput.Model on the next turn, while plain prompts send "".
func TestModelOverrideThreadedToTurn(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24

	// A prompt before any /model selection sends an empty model.
	startTurnCmd(fc, m.ref, "first", m.mode.apiValue(), m.modelOverride, m.effortOverride, false)()
	if len(fc.startedModels) != 1 || fc.startedModels[0] != "" {
		t.Fatalf("default model = %v, want one empty entry", fc.startedModels)
	}

	// /sonnet sets the override.
	m.input.SetValue("/sonnet")
	m.handleKey(keyMsg("enter"))
	if m.modelOverride != "sonnet" {
		t.Fatalf("modelOverride = %q, want sonnet", m.modelOverride)
	}

	// The next turn carries the selected model.
	startTurnCmd(fc, m.ref, "second", m.mode.apiValue(), m.modelOverride, m.effortOverride, false)()
	if got := fc.startedModels[len(fc.startedModels)-1]; got != "sonnet" {
		t.Errorf("turn model = %q, want sonnet", got)
	}

	// /model-default reverts to the account default.
	m.input.SetValue("/model-default")
	m.handleKey(keyMsg("enter"))
	if m.modelOverride != "" {
		t.Errorf("modelOverride after /model-default = %q, want empty", m.modelOverride)
	}
}
