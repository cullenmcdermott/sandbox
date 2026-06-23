package theme

// brand.go — custom brand glyphs for the agent backends. The dashboard renders a
// neutral status glyph per session (GlyphBusy etc., see styles.go); these marks
// are the orthogonal *identity* axis — they say *which agent* a row is, the way a
// favicon does, independent of whether it is busy/idle/waiting.
//
// Two registers are provided:
//
//   - One-cell MARKS (MarkClaude / MarkOpenCode) for inline use in list rows,
//     the backend picker, and the agent label. They are exactly one terminal cell
//     wide so columns stay aligned on every terminal.
//   - Multi-line MASCOTS (MascotClaude / MascotOpenCode) — chunky block-pixel
//     sprites for roomy surfaces (the connecting splash), drawn in Unicode
//     quadrant blocks the way the Claude Code CLI draws its little robot.
//
// The two agents are deliberately styled to feel like themselves: Claude in its
// warm Peach→Coral gradient spark, opencode in the cool, monochrome pixel-block
// register of its own brand (opencode.ai is high-contrast charcoal/off-white
// blocks — terminal-native, not ornamental).

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// --- One-cell identity marks ---------------------------------------------

// MarkClaude is the Anthropic sunburst distilled to a single cell. U+2733 (EIGHT
// SPOKED ASTERISK) is the closest stock glyph to the radial spark and, unlike a
// plain `*`, renders as a filled star on every modern terminal font. Rendered in
// the warm BrandClaude tone (see MarkClaudeStyled).
const MarkClaude = "✳"

// MarkOpenCode echoes opencode's pixel-block logo in one cell. U+25A6 (SQUARE
// WITH HORIZONTAL FILL) reads as a tiny grid of pixels — the modular-block motif
// of opencode.ai — and is exactly one cell wide. Rendered in the neutral
// BrandOpenCode tone, matching opencode's monochrome identity.
const MarkOpenCode = "▦"

// --- Active brand tones ---------------------------------------------------
//
// Functions (not vars) because the palette vars they read are swapped in place
// by ApplyTheme; reading at call time tracks the active theme.

// BrandClaude is the Claude mark's tone — Peach, the palette's warm orange, the
// nearest match to Anthropic's rust-orange spark across all three themes.
func BrandClaude() color.Color { return Peach }

// BrandOpenCode is the opencode mark's tone — a bright neutral (TextBright), in
// keeping with opencode's monochrome, high-contrast brand. It reads as cool/grey
// next to Claude's warm spark, so the two agents never blur together at a glance.
func BrandOpenCode() color.Color { return TextBright }

// MarkClaudeStyled returns the Claude mark pre-colored in its brand tone.
func MarkClaudeStyled() string { return styledMark(MarkClaude, BrandClaude()) }

// MarkOpenCodeStyled returns the opencode mark pre-colored in its brand tone.
func MarkOpenCodeStyled() string { return styledMark(MarkOpenCode, BrandOpenCode()) }

func styledMark(glyph string, c color.Color) string {
	return lipgloss.NewStyle().Foreground(c).Render(glyph)
}

// --- Block-pixel mascots (the "Claude Code guy" aesthetic) ----------------
//
// Drawn in Unicode quadrant block elements (▀▄▌▐▖▗▘▝▙▛▜▟█) — the same
// 2×2-subpixel technique the Claude Code CLI uses for its startup robot. They
// read as chunky pixel sprites rather than line art. Every line is the same
// display width so the sprite stays rectangular when centered.

// MascotClaude is the "Claude Code guy": a rounded block head with two little
// feet/eyes below. Render through ClaudeMascot for the warm Peach→Coral glow.
const MascotClaude = ` ▐▛███▜▌
▝▜█████▛▘
  ▘▘ ▝▝`

// MascotOpenCode is opencode's pixel-block "OC" monogram — a blocky, terminal-era
// wordmark that mirrors opencode.ai's modular-block logo. Rendered in a cool
// monochrome gradient (see OpenCodeMascot), never the warm Claude palette.
const MascotOpenCode = `▟▀▙ ▟▀▘
█ █ █
▜▄▛ ▜▄▖`

// ClaudeMascot renders the block robot in the Peach→Coral brand gradient (which
// degrades to a solid brand tone on low-color terminals via GradientText).
func ClaudeMascot() string { return gradientBlock(MascotClaude, true, Peach, Coral) }

// OpenCodeMascot renders the "OC" monogram in opencode's monochrome register — a
// bright-to-muted grey gradient, echoing the off-white/charcoal of opencode.ai.
func OpenCodeMascot() string {
	return gradientBlock(MascotOpenCode, true, TextBright, TextMuted)
}

// gradientBlock applies GradientText line-by-line so a multi-line block keeps its
// shape (GradientText spans one logical line; mapping per line preserves the
// newlines and column alignment).
func gradientBlock(block string, bold bool, stops ...color.Color) string {
	lines := strings.Split(block, "\n")
	for i, ln := range lines {
		lines[i] = GradientText(ln, bold, stops...)
	}
	return strings.Join(lines, "\n")
}
