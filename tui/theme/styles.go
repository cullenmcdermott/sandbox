package theme

import (
	"image/color"
	"os"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
)

// GradientCapable reports whether the terminal can render a multi-stop gradient
// legibly — true only for 256-color and truecolor profiles. On 16-color / ascii
// terminals (or NO_COLOR) a blended ramp turns muddy, so callers fall back to a
// solid brand color instead. Detected once at startup.
var GradientCapable = func() bool {
	p := colorprofile.Detect(os.Stderr, os.Environ())
	return p == colorprofile.TrueColor || p == colorprofile.ANSI256
}()

// --- Gradient + spinner helpers (charm touches) --------------------------

// GradientText renders s with a per-grapheme-cluster perceptual gradient across
// the given color stops (the branded Charple→Dolly wordmark flourish), carrying
// bold through the gradient. Grapheme-correct via kit so emoji/wide glyphs keep
// their width.
func GradientText(s string, bold bool, stops ...color.Color) string {
	base := lipgloss.NewStyle().Bold(bold)
	// Solid-color fallback when a gradient would degrade poorly (16-color/ascii
	// terminal or NO_COLOR) — a clean brand color beats a muddy blend.
	if len(stops) < 2 || !GradientCapable {
		if len(stops) > 0 {
			base = base.Foreground(stops[0])
		}
		return base.Render(s)
	}
	return kit.GradientClusters(base, s, stops...)
}

// spinnerColors holds the brand gradient for the busy glyph. Rebuilt by
// rebuildSpinner on every theme change; used to (re-)build dashSpinner.
var spinnerColors []color.Color

// dashSpinner is the package-level pre-rendered gradient spinner. Initialized
// once with default braille frames; rebuilt by rebuildSpinner so it tracks the
// current theme colors.
var dashSpinner = anim.NewSpinner()

// spinnerFrames is the busy-glyph frame source for dashSpinner — quarter-block
// rotations that read as a spinner while resembling the spec's half-filled `◐`.
// It is the anim spinner's frame DATA (consumed only by dashSpinner.Rebuild),
// not a parallel spinner implementation; the glyph itself is produced by
// anim.Spinner via SpinnerFrame (A5).
var spinnerFrames = []string{"◐", "◓", "◑", "◒"}

// rebuildSpinner re-derives the brand-gradient spinner from the active palette.
// Called by ApplyTheme on every theme swap.
func rebuildSpinner() {
	spinnerColors = lipgloss.Blend1D(12, Charple, Hazy, Dolly, Hazy, Charple)
	dashSpinner.Rebuild(spinnerFrames, spinnerColors)
}

// SpinnerFrame returns the pre-colored spinner glyph for the given frame index.
// Replaces the old busyGlyph+spinnerColor pair; handles reduce-motion internally.
func SpinnerFrame(frame int) string {
	if anim.ReduceMotion() {
		return dashSpinner.FrameAt(0)
	}
	return dashSpinner.FrameAt(frame)
}

// FadeDuration is the window over which a row's glyph fades in after its status
// changes (the fresh-data charm touch).
const FadeDuration = 220 * time.Millisecond

// FadeColor blends from a dim tone toward target over FadeDuration since the
// given change time, so a freshly-changed glyph fades in rather than popping.
// It rides the motion engine: the eased fraction comes from a Transition
// (ease-out, collapsing to the end state under reduce-motion) and the blend
// from anim.LerpColor, so every time-based color shares one primitive.
func FadeColor(target color.Color, since time.Time) color.Color {
	if since.IsZero() {
		return target
	}
	el := time.Since(since)
	if el >= FadeDuration {
		return target
	}
	tr := anim.Transition{Total: FadeDuration}
	return anim.LerpColor(TextDim, target, tr.At(el))
}

// --- Per-status glyphs (from the Status system in the handoff) -----------

// Glyph strings (copy exactly from the handoff glyph reference).
const (
	GlyphBusy       = "◐" // animated spinner; hue cycles Charple→Dolly
	GlyphWaiting    = "◆" // gold approval pending
	GlyphNeedsInput = "❯" // guac green, turn done
	GlyphIdle       = "○" // muted, pod up nothing pending
	GlyphSuspended  = "⏾" // dim sleep crescent, pod scaled to zero (distinct from idle's ○)
	GlyphFailed     = "✕" // coral red
	GlyphSelBar     = "▌" // selection bar
)
