package dashboard

import (
	"strings"
	"testing"
)

// Regression for D7: the cited statusline test only exercised blockBar, never
// renderStatusLine, so the pct clamp and the ≥80% warning branch were untested.
// These drive renderStatusLine directly.

func newStatusModel(limit, used int) *TranscriptModel {
	m := &TranscriptModel{}
	m.CtxLimit = limit
	m.InputTokens = used // ctxTokens() = inTok + cacheReadTok + cacheWriteTok
	return m
}

// At ≥80% usage the status line shows the coral "!" warning and the pct.
func TestStatusLineWarnsAtEightyPercent(t *testing.T) {
	out := stripANSI(newStatusModel(1000, 850).renderStatusLine())
	if !strings.Contains(out, "85%") {
		t.Fatalf("status line missing 85%% context: %q", out)
	}
	if !strings.Contains(out, "!") {
		t.Fatalf("status line missing the >=80%% warning marker: %q", out)
	}
}

// Between the ≥60% gauge threshold and the 80% warning there is no warning
// marker. (§2c: below 60% the gauge is hidden entirely, so this probes 65% —
// visible, un-warned — rather than the old always-visible 10% case.)
func TestStatusLineNoWarnBelowEighty(t *testing.T) {
	out := stripANSI(newStatusModel(1000, 650).renderStatusLine())
	if !strings.Contains(out, "65%") {
		t.Fatalf("status line missing 65%% context: %q", out)
	}
	if strings.Contains(out, "! ") {
		t.Fatalf("status line should not warn at 65%%: %q", out)
	}
}

// §2c: below the 60% gauge threshold the ctx gauge is hidden entirely so a roomy
// context stays quiet — no percentage in the row at all.
func TestStatusLineHidesCtxBelowThreshold(t *testing.T) {
	out := stripANSI(newStatusModel(1000, 100).renderStatusLine())
	if strings.Contains(out, "10%") || strings.Contains(out, "%") {
		t.Fatalf("ctx gauge must be hidden below 60%%, got %q", out)
	}
}

// §2c fix (c): when the model's context limit is unknown the chat status line
// HIDES the gauge (matching the dashboard), rather than assuming a 200k window.
func TestStatusLineHidesCtxWhenLimitUnknown(t *testing.T) {
	m := newStatusModel(0, 180000) // limit unknown, lots of tokens
	if out := stripANSI(m.renderStatusLine()); strings.Contains(out, "%") {
		t.Fatalf("unknown ctx limit must hide the gauge (no %%), got %q", out)
	}
}

// Over-limit usage clamps the percentage to 100 (no 250% etc.).
func TestStatusLinePctClampedTo100(t *testing.T) {
	out := stripANSI(newStatusModel(1000, 5000).renderStatusLine())
	if !strings.Contains(out, "100%") {
		t.Fatalf("over-limit usage should clamp to 100%%: %q", out)
	}
	if strings.Contains(out, "500%") {
		t.Fatalf("pct not clamped: %q", out)
	}
}
