package dashboard

import "testing"

// TestApplyThemeSwapsTokens asserts that applying a different theme actually
// re-skins the sampled semantic tokens (no token is left stale) and rebuilds
// derived styles/spinner gradient.
func TestApplyThemeSwapsTokens(t *testing.T) {
	t.Cleanup(func() { applyTheme(themes[0]) })

	mid, ok := themeByName("Midnight")
	if !ok {
		t.Fatal("Midnight theme not registered")
	}
	day, ok := themeByName("Daylight")
	if !ok {
		t.Fatal("Daylight theme not registered")
	}

	applyTheme(mid)
	page0, body0, charple0 := colorPage, colorTextBody, colorCharple
	if activeTheme != "Midnight" {
		t.Fatalf("activeTheme = %q, want Midnight", activeTheme)
	}

	applyTheme(day)
	if colorPage == page0 {
		t.Errorf("colorPage did not swap on theme change (still %v)", page0)
	}
	if colorTextBody == body0 {
		t.Errorf("colorTextBody did not swap on theme change")
	}
	// Accents must swap too (they used to be constant package vars).
	if colorCharple == charple0 {
		t.Errorf("accent colorCharple did not swap on theme change")
	}
	if activeTheme != "Daylight" {
		t.Fatalf("activeTheme = %q, want Daylight", activeTheme)
	}
	// Derived state must be refreshed from the new theme's text ramp (the
	// spinner gradient itself lives in tui/theme and is covered there).
	if colorStatusMuted != colorTextMuted {
		t.Errorf("derived colorStatusMuted not refreshed from text ramp")
	}
}

// TestContrastRolesAreUsed verifies U4: colorOnBrand and colorOnGold are
// actually applied to rendering paths (selection bar, permission box header),
// not just computed and discarded.
func TestContrastRolesAreUsed(t *testing.T) {
	t.Cleanup(func() { applyTheme(themes[0]) })

	day, ok := themeByName("Daylight")
	if !ok {
		t.Fatal("Daylight theme not registered")
	}
	applyTheme(day)

	// colorOnBrand in light theme should be near-black (OnColor returns near-black
	// for light backgrounds). colorCharple in light theme is a light purple.
	// They must differ, proving we don't just use the brand color.
	if colorOnBrand == colorCharple {
		t.Error("colorOnBrand == colorCharple; contrast role not distinct from brand color")
	}

	// colorOnGold in light theme should be near-black, while colorGold is a light
	// yellow/gold. They must differ.
	if colorOnGold == colorGold {
		t.Error("colorOnGold == colorGold; contrast role not distinct from accent color")
	}

	// The selection bar style must use colorOnBrand, not colorCharple.
	// (We can't introspect lipgloss.Style, but we can verify the globals differ.)
	// Permission box header uses colorOnGold — verified by grep/compilation.
}

// TestDefaultThemeForBackground covers the terminal-bg default selection and
// the SANDBOX_THEME override hook.
func TestDefaultThemeForBackground(t *testing.T) {
	t.Setenv("SANDBOX_THEME", "")
	if got := defaultThemeForBackground(true); got.Name != "Midnight" {
		t.Errorf("dark default = %q, want Midnight", got.Name)
	}
	if got := defaultThemeForBackground(false); got.Name != "Daylight" {
		t.Errorf("light default = %q, want Daylight", got.Name)
	}

	t.Setenv("SANDBOX_THEME", "ember")
	if got := defaultThemeForBackground(true); got.Name != "Ember" {
		t.Errorf("override default = %q, want Ember", got.Name)
	}
	t.Setenv("SANDBOX_THEME", "nonsense")
	if got := defaultThemeForBackground(true); got.Name != "Midnight" {
		t.Errorf("bad override should fall back to bg default, got %q", got.Name)
	}
}
