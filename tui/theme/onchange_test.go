package theme

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/tui/kit"
)

// ORACLE: OnChange returns an unsubscribe func — the hook runs once
// immediately, runs on each ApplyTheme while registered, and never runs after
// unsubscribing (double-unsubscribe is harmless).
func TestOnChangeUnsubscribe(t *testing.T) {
	t.Cleanup(func() { ApplyTheme(themes[0]) })

	calls := 0
	off := OnChange(func() { calls++ })
	if calls != 1 {
		t.Fatalf("OnChange did not run the hook immediately (calls=%d)", calls)
	}
	ApplyTheme(themes[1])
	if calls != 2 {
		t.Fatalf("hook did not run on ApplyTheme (calls=%d)", calls)
	}
	off()
	off() // double-unsubscribe must be a no-op
	ApplyTheme(themes[0])
	if calls != 2 {
		t.Fatalf("hook ran after unsubscribe (calls=%d)", calls)
	}
}

// ORACLE: the kit's section-rule and scrollbar-thumb colors follow the active
// theme — Daylight must render them differently from Midnight.
func TestRuleAndThumbReskin(t *testing.T) {
	t.Cleanup(func() { ApplyTheme(themes[0]) })

	ApplyTheme(themes[0]) // Midnight
	midRule := kit.SectionHeader("t", 20)
	midThumb := kit.Scrollbar(4, 100, 10, 0)
	ApplyTheme(themes[1]) // Daylight
	if kit.SectionHeader("t", 20) == midRule {
		t.Fatal("SectionHeader rule color did not reskin on theme swap")
	}
	if kit.Scrollbar(4, 100, 10, 0) == midThumb {
		t.Fatal("Scrollbar thumb color did not reskin on theme swap")
	}
}
