package main

import (
	"encoding/json"
	"testing"
)

// The payload Claude Code actually writes to this binary's stdin, trimmed to
// the fields the renderer reads. Kept as a raw string rather than built from
// the struct on purpose: the bug this file guards against is a JSON tag that
// stops matching the real wire names, and a round-trip through our own struct
// would agree with itself no matter what the tags say.
const realPayload = `{
  "model": {"display_name": "Opus 5"},
  "context_window": {"context_window_size": 1000000, "used_percentage": 31},
  "cwd": "/session/workspace",
  "cost": {"total_cost_usd": 0.5072566},
  "rate_limits": {
    "five_hour": {"used_percentage": 16, "resets_at": 1754176800},
    "seven_day": {"used_percentage": 88, "resets_at": 1754172000}
  }
}`

func parse(t *testing.T, payload string) *claudeInput {
	t.Helper()
	var in claudeInput
	if err := json.Unmarshal([]byte(payload), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &in
}

// A pod cannot reach /api/oauth/usage, so stdin is the only source of plan
// usage it will ever have. If this breaks, lines 2-3 silently vanish in every
// sandbox session — the exact regression that shipped once already.
func TestUsageFromStdinReadsRealPayload(t *testing.T) {
	u := usageFromStdin(parse(t, realPayload))
	if u == nil {
		t.Fatal("usageFromStdin returned nil for a payload that carries rate_limits")
	}
	if u.FiveHour.Utilization != 16 {
		t.Errorf("five_hour utilization = %v, want 16", u.FiveHour.Utilization)
	}
	if u.SevenDay.Utilization != 88 {
		t.Errorf("seven_day utilization = %v, want 88", u.SevenDay.Utilization)
	}
	// resets_at arrives as an epoch NUMBER but period.ResetsAt is the API's
	// string; fmtReset parses the epoch form, so "N/A" here means the handoff
	// between the two shapes broke.
	if got := fmtReset(u.FiveHour.ResetsAt); got == "N/A" {
		t.Errorf("five_hour resets_at did not survive the epoch->string handoff (got %q)", got)
	}
	if got := fmtReset(u.SevenDay.ResetsAt); got == "N/A" {
		t.Errorf("seven_day resets_at did not survive the epoch->string handoff (got %q)", got)
	}
}

// A window sits at exactly 0% for a while after it resets. Treating that as
// "absent" would blank the line precisely when the user has the most headroom.
func TestUsageFromStdinKeepsZeroPercentWindow(t *testing.T) {
	in := parse(t, `{"rate_limits":{"five_hour":{"used_percentage":0,"resets_at":1754176800},
	                                "seven_day":{"used_percentage":88,"resets_at":1754172000}}}`)
	u := usageFromStdin(in)
	if u == nil {
		t.Fatal("a 0% window was treated as absent")
	}
	if u.FiveHour.Utilization != 0 {
		t.Errorf("five_hour utilization = %v, want 0", u.FiveHour.Utilization)
	}
}

// Nil is the signal that hands control to getUsage(). API-key/Bedrock/Vertex
// sessions never get rate_limits, and neither does any session before its first
// API response, so this path has to stay reachable.
func TestUsageFromStdinNilWithoutRateLimits(t *testing.T) {
	for name, payload := range map[string]string{
		"absent": `{"model":{"display_name":"Opus 5"}}`,
		"empty":  `{"rate_limits":{}}`,
		"null":   `{"rate_limits":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			if u := usageFromStdin(parse(t, payload)); u != nil {
				t.Errorf("usageFromStdin = %+v, want nil so getUsage() runs", u)
			}
		})
	}
}

// One window present and the other missing is a payload we must still render;
// the missing half degrades to a zero window rather than blanking both lines.
func TestUsageFromStdinAcceptsPartialWindows(t *testing.T) {
	u := usageFromStdin(parse(t, `{"rate_limits":{"five_hour":{"used_percentage":42,"resets_at":1754176800}}}`))
	if u == nil {
		t.Fatal("a payload with only five_hour returned nil")
	}
	if u.FiveHour.Utilization != 42 {
		t.Errorf("five_hour utilization = %v, want 42", u.FiveHour.Utilization)
	}
	if got := fmtReset(u.SevenDay.ResetsAt); got != "N/A" {
		t.Errorf("missing seven_day reset = %q, want N/A", got)
	}
}

func TestEpochResetsAt(t *testing.T) {
	if got := epochResetsAt(1754176800); got != "1754176800" {
		t.Errorf("epochResetsAt(1754176800) = %q, want \"1754176800\"", got)
	}
	// Absent/zero/negative must produce the empty string fmtReset renders as
	// "N/A", not a 1970 timestamp.
	for _, epoch := range []float64{0, -1} {
		if got := epochResetsAt(epoch); got != "" {
			t.Errorf("epochResetsAt(%v) = %q, want empty", epoch, got)
		}
	}
	if got := fmtReset(epochResetsAt(0)); got != "N/A" {
		t.Errorf("fmtReset(epochResetsAt(0)) = %q, want N/A", got)
	}
}
