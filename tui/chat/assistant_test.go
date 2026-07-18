// CANONICAL TEST — do not weaken. internal/tui/dashboard/chat/assistant_test.go
package chat

import (
	"strings"
	"testing"
)

// newTestItem builds an AssistantItem with counting render funcs.
// IMPL: wire to your NewAssistantItem + dependency-injection surface.
func newTestItem(m *AssistantMessage) (*AssistantItem, *int, *int) {
	contentCalls, thinkingCalls := new(int), new(int)
	a := NewAssistantItem(m /* deps */)
	a.renderContentMD = func(text string, w int) string { *contentCalls++; return "C:" + text }
	a.renderThinkingMD = func(text string, w int) string { *thinkingCalls++; return "T:" + text }
	return a, contentCalls, thinkingCalls
}

// COUNTER: mutating only Content must not recompute the thinking section, and vice versa.
func TestSectionIsolation(t *testing.T) {
	m := &AssistantMessage{ID: "x", Thinking: "reasoning here", Content: "hello"}
	a, cc, tc := newTestItem(m)

	_ = a.RawRender(80) // populate both
	if *cc != 1 || *tc != 1 {
		t.Fatalf("initial render: content=%d thinking=%d, want 1/1", *cc, *tc)
	}
	// Change only content (simulate a streaming delta).
	m.Content = "hello world"
	a.SetMessage(m)
	_ = a.RawRender(80)
	if *cc != 2 {
		t.Fatalf("content not recomputed after content change: %d", *cc)
	}
	if *tc != 1 {
		t.Fatalf("thinking RECOMPUTED on a content-only change: %d (sections not isolated)", *tc)
	}
	// Change only thinking.
	m.Thinking = "new reasoning"
	a.SetMessage(m)
	_ = a.RawRender(80)
	if *tc != 2 {
		t.Fatalf("thinking not recomputed after thinking change: %d", *tc)
	}
	if *cc != 2 {
		t.Fatalf("content RECOMPUTED on a thinking-only change: %d", *cc)
	}
}

// COUNTER: re-rendering identical state recomputes nothing.
func TestSectionCacheStable(t *testing.T) {
	m := &AssistantMessage{ID: "x", Content: "stable"}
	a, cc, _ := newTestItem(m)
	_ = a.RawRender(80)
	_ = a.RawRender(80)
	if *cc != 1 {
		t.Fatalf("identical re-render recomputed content %d times, want 1", *cc)
	}
}

// COUNTER: a width change invalidates section caches (wrap depends on width).
func TestSectionWidthInvalidates(t *testing.T) {
	m := &AssistantMessage{ID: "x", Content: "wrap me"}
	a, cc, _ := newTestItem(m)
	_ = a.RawRender(80)
	_ = a.RawRender(40)
	if *cc != 2 {
		t.Fatalf("width change did not invalidate content section: %d", *cc)
	}
}

// ORACLE: length-prefixed framing prevents tuple-collision.
func TestFnvFieldsFraming(t *testing.T) {
	if fnvFields([]byte("a"), []byte("bc")) == fnvFields([]byte("ab"), []byte("c")) {
		t.Fatalf("fnvFields collided on {a,bc} vs {ab,c} — framing missing")
	}
}

// ORACLE: focus flips output; same state is stable.
func TestFocusPrefix(t *testing.T) {
	m := &AssistantMessage{ID: "x", Content: "line one\nline two"}
	a, _, _ := newTestItem(m)
	a.SetFocused(false)
	blurred := a.Render(80)
	a.SetFocused(true)
	focused := a.Render(80)
	if blurred == focused {
		t.Fatalf("focus flip did not change rendered output")
	}
	if strings.Count(focused, "\n") != strings.Count(blurred, "\n") {
		t.Fatalf("focus changed line count; prefix must be per-line, not a wrap")
	}
}

// ORACLE: Finished lifecycle (streaming => not freezable; terminal => freezable).
func TestFinishedLifecycle(t *testing.T) {
	m := &AssistantMessage{ID: "x", Content: "partial", Streaming: true}
	a, _, _ := newTestItem(m)
	if a.Finished() {
		t.Fatalf("streaming item reported Finished")
	}
	m.Streaming = false
	m.Finished = true
	a.SetMessage(m)
	if !a.Finished() {
		t.Fatalf("terminal item not Finished")
	}
}
