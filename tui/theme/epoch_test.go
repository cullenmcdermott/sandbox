package theme

import "testing"

// Epoch must strictly increase on every ApplyTheme so caches keyed on it miss
// after a swap (even re-applying the same theme counts — a redundant flush is
// harmless, a missed one serves stale-palette ANSI).
func TestEpochBumpsOnApply(t *testing.T) {
	t.Cleanup(func() { ApplyTheme(themes[0]) })

	e0 := Epoch()
	ApplyTheme(themes[1])
	if got := Epoch(); got != e0+1 {
		t.Fatalf("Epoch = %d after one apply, want %d", got, e0+1)
	}
	ApplyTheme(themes[0])
	if got := Epoch(); got != e0+2 {
		t.Fatalf("Epoch = %d after two applies, want %d", got, e0+2)
	}
}
