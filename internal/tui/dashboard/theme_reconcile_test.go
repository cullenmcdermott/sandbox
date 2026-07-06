package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// §1c theme cache invalidation, force-path guard: a /theme swap bumps the
// global epoch, but a committed block's version is otherwise stable, so without
// the epoch-changed force its cached old-palette ANSI would survive until an
// unrelated width change. This locks the full chain WITHOUT a width change:
// epoch bump → forced version bump on every card → tui/list cache miss →
// fresh-palette re-render.
func TestThemeSwapForcesReconcileRerender(t *testing.T) {
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	theme.ApplyForBackground(true)

	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 60, 24
	m.layout()
	m.appendBlock(blockAssistant, "hello **world**")
	m.commitItems()
	if len(m.blocks) == 0 {
		t.Fatal("expected a committed card")
	}
	v1 := m.blocks[0].Version()

	// No-op commit: a committed block must NOT bump (stable version → cache hit).
	m.commitItems()
	if m.blocks[0].Version() != v1 {
		t.Fatalf("no-op commit bumped version %d→%d (committed block should be gated)", v1, m.blocks[0].Version())
	}

	theme.ApplyForBackground(false) // swap → bumps the epoch

	m.commitItems()
	if m.blocks[0].Version() == v1 {
		t.Fatal("theme swap must bump the card version so tui/list re-serves a fresh-palette render")
	}
}
