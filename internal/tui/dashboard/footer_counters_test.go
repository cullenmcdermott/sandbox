package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// The header's attention count and the footer's live-stream count measure
// unrelated things — "how many sessions want you" vs "how many sessions are
// connected" — but both used to render as a lone adjective + number, so equal
// values read as the same figure printed twice. They must not share a word.
func TestFooterStreamCountIsDistinctFromAttentionCount(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{State: session.State{ID: "s1", Status: session.StatusRunning}, sessionReadModel: sessionReadModel{DashStatus: StatusNeedsInput}},
		{State: session.State{ID: "s2", Status: session.StatusRunning}, sessionReadModel: sessionReadModel{DashStatus: StatusNeedsInput}},
	}
	// Same value as the attention count, which is exactly the case that looked
	// like duplication.
	m.warmSet = map[session.ID]struct{}{"s1": {}, "s2": {}}

	footer := m.bottomBar(120)
	header := m.topBar(120, m.partition())

	if !strings.Contains(footer, "2 streaming") {
		t.Errorf("footer should name what it counts, got:\n%s", footer)
	}
	if strings.Contains(footer, "warm") {
		t.Errorf("footer still leaks the internal term 'warm':\n%s", footer)
	}
	if !strings.Contains(header, "2 ready") {
		t.Fatalf("header lost its attention count:\n%s", header)
	}
	// The load-bearing assertion: no word carries both meanings.
	if strings.Contains(footer, "ready") {
		t.Errorf("footer reuses the header's attention word:\n%s", footer)
	}
}

// A fleet with nothing streaming shows no badge at all — the counter is a
// signal, not permanent chrome.
func TestFooterStreamCountHiddenWhenNoneWarm(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{{State: session.State{ID: "s1", Status: session.StatusRunning}}}

	if got := m.bottomBar(120); strings.Contains(got, "streaming") {
		t.Errorf("no warm sessions should render no badge, got:\n%s", got)
	}
}
