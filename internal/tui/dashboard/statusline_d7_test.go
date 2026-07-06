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

// Below 80% there is no warning marker.
func TestStatusLineNoWarnBelowEighty(t *testing.T) {
	out := stripANSI(newStatusModel(1000, 100).renderStatusLine())
	if !strings.Contains(out, "10%") {
		t.Fatalf("status line missing 10%% context: %q", out)
	}
	if strings.Contains(out, "! ") {
		t.Fatalf("status line should not warn at 10%%: %q", out)
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
