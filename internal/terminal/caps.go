// Package terminal owns terminal-capability detection and escape-sequence
// emission for the dashboard TUI's opt-in "Ghostty effects" (see
// docs/ghostty-terminal-effects.md). It detects capabilities once at startup
// into a Caps value that is threaded into the dashboard model; every visual
// enhancement is gated behind a Caps field so non-capable terminals (and
// NO_COLOR) get exactly today's output.
//
// The package is deliberately free of any Bubble Tea / lipgloss dependency: it
// reads the environment and returns plain data + plain strings. Callers decide
// where in the composed frame to splice the (zero-width) control strings.
package terminal

import (
	"os"
	"strings"

	"github.com/charmbracelet/colorprofile"
)

// Caps describes the parts of the host terminal we light up features for.
// Detected once via Detect; treat as immutable thereafter.
type Caps struct {
	// IsGhostty is true when TERM_PROGRAM=ghostty. Ghostty is the primary
	// target for the out-of-band OSC/Kitty effects.
	IsGhostty bool

	// GhosttyVersion is TERM_PROGRAM_VERSION when IsGhostty (may be empty).
	GhosttyVersion string

	// KittyGraphics is true when the terminal advertises the Kitty graphics
	// protocol (Ghostty does). Gates the Stage 3 placeholder gauge.
	KittyGraphics bool

	// TrueColor is true on a 24-bit (or 256-color) profile — the single source
	// of truth folded in from colorprofile.Detect, matching the dashboard's
	// existing gradientCapable check.
	TrueColor bool

	// ReduceMotion mirrors anim.ReduceMotion(): SANDBOX_REDUCE_MOTION=1 or
	// NO_COLOR. The global off switch for motion-driven effects.
	ReduceMotion bool
}

// environ abstracts the environment lookup so Detect is unit-testable without
// mutating real process env.
type environ func(string) string

// Detect inspects the process environment (and color profile) and returns the
// terminal capabilities. It performs no terminal I/O — detection is purely
// env-based so it is cheap, race-free, and safe to call before the alt-screen
// is set up. The optional XTVERSION confirmation handshake described in the
// design doc is intentionally omitted: TERM_PROGRAM is sufficient and avoids a
// blocking read on stdin.
func Detect() Caps {
	return detect(os.Getenv, colorprofile.Detect(os.Stderr, os.Environ()))
}

// detect is the testable core: env is the lookup function and profile is the
// already-detected color profile.
func detect(env environ, profile colorprofile.Profile) Caps {
	isGhostty := strings.EqualFold(env("TERM_PROGRAM"), "ghostty")
	trueColor := profile == colorprofile.TrueColor || profile == colorprofile.ANSI256
	reduceMotion := env("SANDBOX_REDUCE_MOTION") == "1" || env("NO_COLOR") != ""

	c := Caps{
		IsGhostty:    isGhostty,
		TrueColor:    trueColor,
		ReduceMotion: reduceMotion,
	}
	if isGhostty {
		c.GhosttyVersion = env("TERM_PROGRAM_VERSION")
		// Ghostty implements the Kitty graphics protocol. We gate the graphics
		// gauge on Ghostty specifically rather than probing, since the probe
		// would require a stdin read we deliberately avoid.
		c.KittyGraphics = true
	}
	return c
}
