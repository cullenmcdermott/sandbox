package dashboard

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// REGRESSION (D1): the search overlay must allow typing 'n' and 'N' into the
// query. Prior code captured them as navigation keys before the rune fallthrough.
func TestSearchQueryCanTypeN(t *testing.T) {
	m := &TranscriptModel{}
	m.openSearch()

	// Type "function" which contains 'n'.
	for _, r := range "function" {
		msg := tea.KeyPressMsg{Code: r, Text: string(r)}
		m.searchKey(msg)
	}

	if got, want := m.search.query, "function"; got != want {
		t.Fatalf("query = %q, want %q (n was captured as nav key)", got, want)
	}
}

func TestSearchQueryCanTypeUpperN(t *testing.T) {
	m := &TranscriptModel{}
	m.openSearch()

	for _, r := range "NoOp" {
		msg := tea.KeyPressMsg{Code: r, Text: string(r)}
		m.searchKey(msg)
	}

	if got, want := m.search.query, "NoOp"; got != want {
		t.Fatalf("query = %q, want %q (N was captured as nav key)", got, want)
	}
}

// enter consumes the key without adding it to the query.
func TestSearchEnterDoesNotAddToQuery(t *testing.T) {
	m := &TranscriptModel{}
	m.openSearch()

	msg := tea.KeyPressMsg{Code: tea.KeyEnter}
	m.searchKey(msg)

	if m.search.query != "" {
		t.Fatalf("enter should not add to query, got %q", m.search.query)
	}
}

// REGRESSION (NEW-1): freeing n/N for the query (D1) left prevSearchMatch with
// zero callers — prev-match navigation was unreachable while the overlay still
// advertised "n/N next/prev". Navigation now uses enter/ctrl+n (next) and
// ctrl+p (prev). This guards that both directions are wired AND that ctrl+n/p
// are not added to the query.
func TestSearchCtrlNextPrevNavigates(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, Session{State: session.State{ID: "s1"}}, nil)
	m.width = 80
	m.height = 24
	m.blocks = []tblock{{kind: blockInfo, text: "alpha"}, {kind: blockInfo, text: "beta"}, {kind: blockInfo, text: "gamma"}}
	m.syncBody()
	m.openSearch()
	// Three matches across blocks; offset 0 keeps scrollToMatch simple.
	m.search.matches = [][2]int{{0, 0}, {1, 0}, {2, 0}}
	m.search.matchIndex = 0

	ctrlN := tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
	ctrlP := tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}

	if got := ctrlN.String(); got != "ctrl+n" {
		t.Fatalf("test setup: ctrl+n key stringifies as %q, want \"ctrl+n\"", got)
	}

	if _, consumed := m.searchKey(ctrlN); !consumed {
		t.Fatal("ctrl+n should be consumed by the search overlay")
	}
	if m.search.matchIndex != 1 {
		t.Fatalf("after ctrl+n matchIndex = %d, want 1", m.search.matchIndex)
	}
	// ctrl+p must reach prevSearchMatch (was dead code): 1 -> 0.
	if _, consumed := m.searchKey(ctrlP); !consumed {
		t.Fatal("ctrl+p should be consumed by the search overlay")
	}
	if m.search.matchIndex != 0 {
		t.Fatalf("after ctrl+p matchIndex = %d, want 0 (prevSearchMatch not wired?)", m.search.matchIndex)
	}
	// ctrl+p again wraps 0 -> 2.
	m.searchKey(ctrlP)
	if m.search.matchIndex != 2 {
		t.Fatalf("after wrap ctrl+p matchIndex = %d, want 2", m.search.matchIndex)
	}

	// ctrl+n / ctrl+p must NOT be typed into the query.
	if m.search.query != "" {
		t.Fatalf("navigation keys leaked into query: %q", m.search.query)
	}
}

// REGRESSION (NEW-3): match offsets are stored as RUNE indices, not byte
// offsets, so scrollToMatch (which indexes []rune) lands correctly inside
// multibyte blocks. In "café foo" the first 'o' is at rune 6 but byte 7 (é is
// two bytes). The fuzzy matcher records the first matched rune's position.
func TestSearchMatchOffsetIsRuneIndex(t *testing.T) {
	m := &TranscriptModel{}
	m.blocks = []tblock{{kind: blockInfo, text: "café foo"}}
	m.openSearch()
	m.search.query = "oo"
	m.updateSearchMatches()

	if len(m.search.matches) != 1 {
		t.Fatalf("got %d matches, want 1", len(m.search.matches))
	}
	if got := m.search.matches[0][1]; got != 6 {
		t.Fatalf("match offset = %d, want rune index 6 (byte offset would be 7)", got)
	}
}

// Fuzzy search (T3) matches non-contiguous subsequences: "rdme" matches
// "README.md" even though those characters aren't adjacent.
func TestSearchFuzzySubsequence(t *testing.T) {
	m := &TranscriptModel{}
	m.blocks = []tblock{
		{kind: blockInfo, text: "README.md"},
		{kind: blockInfo, text: "unrelated text"},
	}
	m.openSearch()
	m.search.query = "rdme"
	m.updateSearchMatches()

	if len(m.search.matches) != 1 {
		t.Fatalf("got %d matches, want 1 (only README.md)", len(m.search.matches))
	}
	if m.search.matches[0][0] != 0 {
		t.Fatalf("matched block %d, want block 0", m.search.matches[0][0])
	}
}

// fuzzyMatchOffset basics: ordered subsequence matches; out-of-order does not.
func TestFuzzyMatchOffset(t *testing.T) {
	if off, ok := fuzzyMatchOffset("abc", "xaybzc"); !ok || off != 1 {
		t.Fatalf("abc in xaybzc: off=%d ok=%v, want off=1 ok=true", off, ok)
	}
	if _, ok := fuzzyMatchOffset("cba", "abc"); ok {
		t.Fatal("cba should not match abc (out of order)")
	}
	if off, ok := fuzzyMatchOffset("", "anything"); !ok || off != 0 {
		t.Fatalf("empty query: off=%d ok=%v, want off=0 ok=true", off, ok)
	}
	if _, ok := fuzzyMatchOffset("Z", "abc"); ok {
		t.Fatal("Z should not match abc")
	}
}

// esc closes search.
func TestSearchEscCloses(t *testing.T) {
	m := &TranscriptModel{}
	m.openSearch()

	msg := tea.KeyPressMsg{Code: tea.KeyEscape}
	m.searchKey(msg)

	if m.search.open {
		t.Fatal("esc should close search overlay")
	}
}
