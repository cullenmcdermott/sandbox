package dashboard

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// lastAssistantBlock returns the last blockAssistant in the transcript, failing
// the test when none exists.
func lastAssistantBlock(t *testing.T, m *TranscriptModel) *blockCard {
	t.Helper()
	var b *blockCard
	for _, blk := range m.blocks {
		if blk.kind == blockAssistant {
			b = blk
		}
	}
	if b == nil {
		t.Fatal("no assistant block appended")
	}
	return b
}

// §2b gap 6: a message.completed carrying citations pins them on the committed
// assistant block, and the block renders a dim numbered "Sources:" footnote
// list under the body.
func TestMessageCompletedCitationsRenderFootnotes(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width = 80
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "assistant",
		Content: "cited answer",
		Citations: []session.Citation{
			{URL: "https://example.com/a", Title: "Example A", CitedText: "quote"},
			{Title: "Doc B"},
			{URL: "https://example.com/c"},
		},
	}))

	b := lastAssistantBlock(t, m)
	if len(b.citations) != 3 {
		t.Fatalf("citations not pinned on the block: got %d, want 3", len(b.citations))
	}
	out := m.renderBlockBody(b)
	for _, want := range []string{
		"Sources:",
		"1. Example A — https://example.com/a", // title+url
		"2. Doc B",                             // title only
		"3. https://example.com/c",             // url only
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footnote render missing %q in:\n%s", want, out)
		}
	}
}

// Regression for the review P1: the footnote must appear through the LIST
// render path (bodyView), not just a direct renderBlockBody call. The list
// eagerly renders and caches the new block during the append's syncItems
// (follow → GotoBottom → renderItemEntry), so citations assigned after that
// render — without a version bump — would be cached away invisibly forever.
func TestCitationFootnoteSurvivesListCache(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 100, 30
	m.layout()
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "assistant",
		Content: "cited answer",
		Citations: []session.Citation{
			{URL: "https://example.com/a", Title: "Example A"},
		},
	}))
	m.layout()
	if body := m.bodyView(); !strings.Contains(body, "Sources:") {
		t.Fatalf("Sources footnote missing from the list-rendered body (stale cache?):\n%s", body)
	}
}

// A schema-legal citation with neither title nor URL (citedText only) is
// skipped — no blank "  N. " line — and numbering stays contiguous. When every
// citation is renderless the footnote (and its header) is absent entirely.
func TestCitationFootnoteSkipsRenderless(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width = 80
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "assistant",
		Content: "cited",
		Citations: []session.Citation{
			{CitedText: "orphan snippet"}, // renderless: skipped
			{Title: "Kept"},
		},
	}))
	out := m.renderBlockBody(lastAssistantBlock(t, m))
	if !strings.Contains(out, "1. Kept") {
		t.Errorf("renderable citation missing or misnumbered in:\n%s", out)
	}
	if strings.Contains(out, "2.") {
		t.Errorf("renderless citation produced a numbered line:\n%s", out)
	}

	// All-renderless: no footnote at all, and no stray trailing blank line.
	m2 := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m2.width = 80
	m2.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:      "assistant",
		Content:   "cited",
		Citations: []session.Citation{{CitedText: "only snippet"}},
	}))
	out2 := m2.renderBlockBody(lastAssistantBlock(t, m2))
	if strings.Contains(out2, "Sources:") {
		t.Errorf("all-renderless citations rendered a Sources header:\n%s", out2)
	}
	if strings.HasSuffix(out2, "\n") {
		t.Errorf("all-renderless citations left a stray trailing newline:\n%q", out2)
	}
}

// A citation-less reply must not grow a Sources footnote.
func TestMessageCompletedWithoutCitationsNoFootnote(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width = 80
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "assistant",
		Content: "plain answer",
	}))

	if out := m.renderBlockBody(lastAssistantBlock(t, m)); strings.Contains(out, "Sources:") {
		t.Fatalf("citation-less block rendered a Sources footnote:\n%s", out)
	}
}

// [V25] The droppedPartialIdx replay path (RV9 in-place replacement) must carry
// citations too: detach mid-stream commits the partial, and the replayed
// message.completed both sets b.citations and re-renders through the list cache
// (the assignment relies on the branch's Bump for cache invalidation).
func TestCitationsSurviveDroppedPartialReplay(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 100, 30
	m.layout()

	m.handleEvent(mkEvent(session.EventMessageStarted, session.MessagePayload{Role: "assistant"}))
	m.handleEvent(mkEvent(session.EventMessageDelta, session.MessagePayload{Content: "Hello, par"}))

	// Simulate a mid-message SSE drop exactly as the tStreamEndedMsg handler
	// does: commit the partial and remember the block for in-place replacement.
	if idx := m.finalizeStreaming(); idx >= 0 {
		m.droppedPartialIdx = idx
	}

	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "assistant",
		Content: "Hello, partial and the rest.",
		Citations: []session.Citation{
			{URL: "https://example.com/a", Title: "Example A"},
		},
	}))

	b := lastAssistantBlock(t, m)
	if len(b.citations) != 1 {
		t.Fatalf("replayed citations not pinned on the replaced block: got %d, want 1", len(b.citations))
	}
	m.layout()
	if body := m.bodyView(); !strings.Contains(body, "Sources:") {
		t.Fatalf("Sources footnote missing from list render after replay (missed Bump?):\n%s", body)
	}
}

// [V6] Citation titles/urls are web-controlled: newlines, carriage returns, and
// non-SGR escape sequences must be flattened before the footnote render (the
// H4 control-sequence defect class), and the line must stay one line.
func TestCitationFieldsSanitized(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width = 80
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "assistant",
		Content: "cited",
		Citations: []session.Citation{
			{Title: "Evil\nTitle\rwith \x1b[2Kcontrols\x1b]0;osc\a", URL: "https://example.com/x"},
		},
	}))
	out := m.renderBlockBody(lastAssistantBlock(t, m))
	if !strings.Contains(out, "1. Evil Title with controls — https://example.com/x") {
		t.Errorf("sanitized footnote line missing (title not flattened?):\n%q", out)
	}
	for _, bad := range []string{"\r", "\x1b[2K", "\x1b]", "\a"} {
		if strings.Contains(out, bad) {
			t.Errorf("control sequence %q survived into the footnote render:\n%q", bad, out)
		}
	}
}

// Footnote lines are truncated at the body wrap width — a pathological URL must
// not overflow the frame (§1c width discipline).
func TestCitationFootnoteWidthSafe(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width = 40
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "assistant",
		Content: "cited",
		Citations: []session.Citation{
			{URL: "https://example.com/" + strings.Repeat("very-long-path/", 20), Title: "A Long Source Title That Goes On"},
		},
	}))

	wrap := m.assistantWrapWidth()
	for _, line := range strings.Split(m.renderBlockBody(lastAssistantBlock(t, m)), "\n") {
		if w := lipgloss.Width(line); w > wrap {
			t.Errorf("footnote line overflows wrap width: %d > %d: %q", w, wrap, line)
		}
	}
}
