package terminal

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func envFromMap(m map[string]string) environ {
	return func(k string) string { return m[k] }
}

func TestDetectGhostty(t *testing.T) {
	c := detect(envFromMap(map[string]string{
		"TERM_PROGRAM":         "ghostty",
		"TERM_PROGRAM_VERSION": "1.2.3",
	}), colorprofile.TrueColor)

	if !c.IsGhostty {
		t.Fatal("expected IsGhostty")
	}
	if c.GhosttyVersion != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", c.GhosttyVersion)
	}
	if !c.KittyGraphics {
		t.Fatal("expected KittyGraphics on Ghostty")
	}
	if !c.TrueColor {
		t.Fatal("expected TrueColor on truecolor profile")
	}
	if c.ReduceMotion {
		t.Fatal("did not expect ReduceMotion")
	}
}

func TestDetectGhosttyCaseInsensitive(t *testing.T) {
	c := detect(envFromMap(map[string]string{"TERM_PROGRAM": "Ghostty"}), colorprofile.TrueColor)
	if !c.IsGhostty {
		t.Fatal("TERM_PROGRAM matching should be case-insensitive")
	}
}

// The notify-capable terminals are detected from TERM_PROGRAM so the desktop
// notification isn't Ghostty-exclusive.
func TestDetectNotifyTerminals(t *testing.T) {
	iterm := detect(envFromMap(map[string]string{"TERM_PROGRAM": "iTerm.app"}), colorprofile.TrueColor)
	if !iterm.IsITerm2 || iterm.IsWezTerm || iterm.IsGhostty {
		t.Fatalf("iTerm.app: got IsITerm2=%v IsWezTerm=%v IsGhostty=%v", iterm.IsITerm2, iterm.IsWezTerm, iterm.IsGhostty)
	}
	wez := detect(envFromMap(map[string]string{"TERM_PROGRAM": "WezTerm"}), colorprofile.TrueColor)
	if !wez.IsWezTerm || wez.IsITerm2 || wez.IsGhostty {
		t.Fatalf("WezTerm: got IsITerm2=%v IsWezTerm=%v IsGhostty=%v", wez.IsITerm2, wez.IsWezTerm, wez.IsGhostty)
	}
	ghostty := detect(envFromMap(map[string]string{"TERM_PROGRAM": "ghostty"}), colorprofile.TrueColor)
	if ghostty.IsITerm2 || ghostty.IsWezTerm {
		t.Fatalf("ghostty must not be flagged iTerm2/WezTerm")
	}
}

func TestDetectNonGhostty(t *testing.T) {
	c := detect(envFromMap(map[string]string{"TERM_PROGRAM": "iTerm.app"}), colorprofile.TrueColor)
	if c.IsGhostty {
		t.Fatal("did not expect IsGhostty for iTerm")
	}
	if c.KittyGraphics {
		t.Fatal("KittyGraphics must be gated on Ghostty")
	}
	if c.GhosttyVersion != "" {
		t.Fatal("version must be empty for non-Ghostty")
	}
	if !c.TrueColor {
		t.Fatal("expected TrueColor on truecolor profile")
	}
}

func TestDetectTrueColorProfiles(t *testing.T) {
	for _, tc := range []struct {
		profile colorprofile.Profile
		want    bool
	}{
		{colorprofile.TrueColor, true},
		{colorprofile.ANSI256, true},
		{colorprofile.ANSI, false},
		{colorprofile.Ascii, false},
		{colorprofile.NoTTY, false},
	} {
		c := detect(envFromMap(nil), tc.profile)
		if c.TrueColor != tc.want {
			t.Errorf("profile %v: TrueColor = %v, want %v", tc.profile, c.TrueColor, tc.want)
		}
	}
}

func TestDetectReduceMotion(t *testing.T) {
	if !detect(envFromMap(map[string]string{"SANDBOX_REDUCE_MOTION": "1"}), colorprofile.TrueColor).ReduceMotion {
		t.Error("SANDBOX_REDUCE_MOTION=1 should set ReduceMotion")
	}
	if !detect(envFromMap(map[string]string{"NO_COLOR": "1"}), colorprofile.TrueColor).ReduceMotion {
		t.Error("NO_COLOR should set ReduceMotion")
	}
	if detect(envFromMap(map[string]string{"SANDBOX_REDUCE_MOTION": "0"}), colorprofile.TrueColor).ReduceMotion {
		t.Error("SANDBOX_REDUCE_MOTION=0 should not set ReduceMotion")
	}
}
