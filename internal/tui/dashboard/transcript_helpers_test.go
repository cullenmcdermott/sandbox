package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// infoCards builds a slice of blockInfo cards wired to m, a test convenience for
// seeding m.blocks directly (the cards are the list items now).
func infoCards(m *TranscriptModel, texts ...string) []*blockCard {
	cards := make([]*blockCard, len(texts))
	for i, t := range texts {
		cards[i] = m.newBlockCard(blockInfo, t)
	}
	return cards
}

func TestDiffOfMinimalContext(t *testing.T) {
	// Changing one line of three keeps the other two as context (" " prefix).
	adds, dels, lines := diffOf("a\nb\nc", "a\nB\nc")
	if adds != 1 || dels != 1 {
		t.Fatalf("got +%d −%d, want +1 −1", adds, dels)
	}
	var ctx, plus, minus int
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "+"):
			plus++
		case strings.HasPrefix(l, "−"):
			minus++
		case strings.HasPrefix(l, " "):
			ctx++
		}
	}
	if plus != 1 || minus != 1 || ctx != 2 {
		t.Errorf("lines: +%d −%d ctx%d, want +1 −1 ctx2 (%q)", plus, minus, ctx, lines)
	}
}

func TestCondenseDiffCollapsesUnchanged(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, " ctx") // long unchanged run
	}
	lines = append(lines, "+added")
	out := condenseDiff(lines, 16)
	// The unchanged run before the change collapses to a single marker.
	var markers, adds int
	for _, l := range out {
		if strings.HasPrefix(l, "…") {
			markers++
		}
		if strings.HasPrefix(l, "+") {
			adds++
		}
	}
	if adds != 1 {
		t.Errorf("want the +added line kept, got %q", out)
	}
	if markers == 0 {
		t.Errorf("want an elision marker for the unchanged run, got %q", out)
	}
	if len(out) > 17 {
		t.Errorf("condensed output not capped: %d lines", len(out))
	}
}

func TestToolArg(t *testing.T) {
	cases := []struct {
		tool, input, want string
	}{
		{"Read", `{"file_path":"/a/b/c/main.go"}`, ".../c/main.go"},
		{"Bash", `{"command":"npm   test\n--watch"}`, "npm test --watch"},
		{"Grep", `{"pattern":"func main"}`, "func main"},
		{"WebFetch", `{"url":"https://x.dev"}`, "https://x.dev"},
		{"Unknown", `{}`, ""},
	}
	for _, c := range cases {
		if got := toolArg(c.tool, json.RawMessage(c.input)); got != c.want {
			t.Errorf("toolArg(%s) = %q, want %q", c.tool, got, c.want)
		}
	}
}

func TestToolSummary(t *testing.T) {
	if got := toolSummary(""); got != "" {
		t.Errorf("empty output should summarise to empty, got %q", got)
	}
	if got := toolSummary("one line"); got != "one line" {
		t.Errorf("single line summary = %q", got)
	}
	if got := toolSummary("a\nb\nc\n"); got != "3 lines" {
		t.Errorf("multiline summary = %q, want %q", got, "3 lines")
	}
}

func TestCompactTokensAndElapsed(t *testing.T) {
	if got := compactTokens(340); got != "340" {
		t.Errorf("compactTokens(340) = %q", got)
	}
	if got := compactTokens(1200); got != "1.2k" {
		t.Errorf("compactTokens(1200) = %q", got)
	}
	if got := fmtElapsed(9 * time.Second); got != "9s" {
		t.Errorf("fmtElapsed(9s) = %q", got)
	}
	if got := fmtElapsed(63 * time.Second); got != "1m03s" {
		t.Errorf("fmtElapsed(63s) = %q, want 1m03s", got)
	}
}

func TestToolCardFIFOMatching(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	// Two tools start, then two complete in order: results match FIFO.
	m.handleEvent(session.Event{Type: session.EventToolStarted, Payload: json.RawMessage(`{"tool":"Read","input":{"file_path":"/x/a.go"}}`)})
	m.handleEvent(session.Event{Type: session.EventToolStarted, Payload: json.RawMessage(`{"tool":"Bash","input":{"command":"go test"}}`)})
	if len(m.pendingTools) != 2 {
		t.Fatalf("want 2 pending tool cards, got %d", len(m.pendingTools))
	}
	m.handleEvent(session.Event{Type: session.EventToolCompleted, Payload: json.RawMessage(`{"output":"ok\nok"}`)})
	m.handleEvent(session.Event{Type: session.EventToolFailed, Payload: json.RawMessage(`{"error":"boom"}`)})
	if len(m.pendingTools) != 0 {
		t.Fatalf("pending tools not drained: %d", len(m.pendingTools))
	}

	var cards []*toolCard
	for _, b := range m.blocks {
		if b.kind == blockToolCard {
			cards = append(cards, b.tool)
		}
	}
	if len(cards) != 2 {
		t.Fatalf("want 2 tool cards, got %d", len(cards))
	}
	if cards[0].tool != "Read" || cards[0].status != toolOK {
		t.Errorf("card0 = %+v, want Read/ok", cards[0])
	}
	if cards[1].tool != "Bash" || cards[1].status != toolErr || cards[1].summary != "boom" {
		t.Errorf("card1 = %+v, want Bash/err/boom", cards[1])
	}
}
