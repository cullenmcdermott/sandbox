package theme

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// ORACLE: Register appends a new theme so it participates fully — ByName finds
// it, ApplyTheme re-skins from its palette (OnChange fires), and its extended
// tones land in the active tokens. Re-registering the same name replaces in
// place (idempotent) rather than growing the registry.
func TestRegister(t *testing.T) {
	saved := themes
	t.Cleanup(func() {
		themes = saved
		ApplyTheme(themes[0])
	})

	custom := themes[0] // start from a complete theme, then rename + tweak tones
	custom.Name = "TestPlum"
	custom.Denied = lipgloss.Color("#ABCDEF")
	custom.InfoSubtle = lipgloss.Color("#123456")

	Register(custom)

	got, ok := ByName("testplum") // case-insensitive
	if !ok {
		t.Fatal("Register did not make the theme discoverable via ByName")
	}
	if got.Name != "TestPlum" {
		t.Fatalf("ByName returned wrong theme: %q", got.Name)
	}

	// Applying it re-skins: OnChange hooks fire and the extended active tones
	// reflect the registered theme's values.
	fired := 0
	off := OnChange(func() { fired++ })
	t.Cleanup(off)
	fired = 0 // discount the immediate call

	ApplyTheme(custom)
	if fired == 0 {
		t.Fatal("ApplyTheme(registered) did not run OnChange hooks")
	}
	if Denied != custom.Denied {
		t.Fatalf("active Denied = %v, want registered %v", Denied, custom.Denied)
	}
	if InfoSubtle != custom.InfoSubtle {
		t.Fatalf("active InfoSubtle = %v, want registered %v", InfoSubtle, custom.InfoSubtle)
	}

	// Re-registering the same name replaces in place — the registry does not grow.
	before := len(themes)
	custom.Denied = lipgloss.Color("#FEDCBA")
	Register(custom)
	if len(themes) != before {
		t.Fatalf("re-register grew the registry: %d → %d", before, len(themes))
	}
	if got, _ := ByName("TestPlum"); got.Denied != custom.Denied {
		t.Fatalf("re-register did not replace the entry: Denied=%v", got.Denied)
	}
}

// ORACLE: every extended semantic active tone is non-nil after a built-in theme
// is applied (no token left unset by ApplyTheme).
func TestExtendedTonesSet(t *testing.T) {
	t.Cleanup(func() { ApplyTheme(themes[0]) })
	for _, th := range themes {
		ApplyTheme(th)
		for name, c := range map[string]interface{}{
			"Denied": Denied, "Info": Info, "Success": Success, "Warning": Warning,
			"InfoSubtle": InfoSubtle, "SuccessSubtle": SuccessSubtle, "WarningSubtle": WarningSubtle,
		} {
			if c == nil {
				t.Fatalf("theme %q left active tone %s nil", th.Name, name)
			}
		}
	}
}
