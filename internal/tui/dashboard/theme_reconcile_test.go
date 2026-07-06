package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// §1c theme cache invalidation, force-path guard: a /theme swap bumps the
// global epoch, but an immutable committed block's (fresh|dirty|unread|mutable)
// reconcile gate is false, so without the epoch-changed force its cached
// old-palette ANSI would survive until an unrelated width change. This locks
// the full chain WITHOUT a width change: epoch bump → forced fingerprint
// recompute (epoch is folded into blockFP, so the fp differs) → version bump →
// tui/list cache miss → fresh-palette re-render.
func TestThemeSwapForcesReconcileRerender(t *testing.T) {
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	theme.ApplyForBackground(true)

	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 60, 24
	m.layout()
	m.appendBlock(blockAssistant, "hello **world**")
	m.reconcileItems()
	if len(m.items) == 0 {
		t.Fatal("expected a reconciled item for the committed block")
	}
	it := m.items[0]
	v1, fp1 := it.Version(), it.fp

	// No-op reconcile: the immutable block must NOT re-fingerprint or bump.
	m.reconcileItems()
	if m.items[0].Version() != v1 {
		t.Fatalf("no-op reconcile bumped version %d→%d (immutable block should be gated)", v1, m.items[0].Version())
	}

	theme.ApplyForBackground(false) // swap → bumps the epoch

	m.reconcileItems()
	if m.items[0].fp == fp1 {
		t.Fatal("theme swap must force a fingerprint recompute (epoch folded into blockFP)")
	}
	if m.items[0].Version() == v1 {
		t.Fatal("theme swap must bump the item version so tui/list re-serves a fresh-palette render")
	}
}
