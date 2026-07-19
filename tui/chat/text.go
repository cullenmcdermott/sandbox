package chat

// text.go — pure, style-free text/ANSI helpers shared by the transcript items.
// These are ported verbatim (behavior-for-behavior) from the dashboard's
// transcript renderer so the public chat items produce byte-identical geometry
// to the production transcript, without importing anything under internal/.
// Every function here is width/ANSI/grapheme aware via lipgloss + x/ansi, the
// same primitives the dashboard uses.

import (
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// truncate shortens s to at most maxW display columns, appending an ellipsis
// when it clips. Width is measured ANSI-aware and grapheme-aware (a wide rune
// counts as two columns; a combining mark as zero), so a CJK/emoji string is
// never cut mid-cell.
func truncate(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	if maxW == 1 {
		return "…"
	}
	return ansi.Truncate(s, maxW, "…")
}

// wrapPlain word-wraps text to at most w display columns, hard-breaking any word
// longer than w (a URL, a base64 blob, an unbroken CJK run). Output lines carry
// no trailing padding — unlike lipgloss .Width, which pads each line to w with
// spaces baked inside the SGR run (untrimmable afterward). Width is measured
// grapheme- and wide-rune-aware by the ansi package, so a CJK/emoji line never
// exceeds w. Callers color the result with a Width-less style so no re-wrap or
// padding is reintroduced.
func wrapPlain(text string, w int) string {
	if w < 1 {
		w = 1
	}
	// Expand tabs first: lipgloss/ansi measure "\t" as width 0, so a surviving tab
	// would let a line overflow the real terminal column budget.
	text = expandTabs(text)
	// Word-wrap first (keeps words intact), then hard-wrap the residue so an
	// over-long single word can't overflow w. preserveSpace=true keeps intentional
	// leading indentation on wrapped code-ish lines.
	wrapped := ansi.Hardwrap(ansi.Wordwrap(text, w, ""), w, true)
	// Backstop: at w==1 a width-2 grapheme (wide CJK, a ZWJ emoji) can't be placed
	// and hardwrap leaves it on its own 2-col line. Clamp each line so the "every
	// line fits w" invariant holds even at degenerate widths.
	if w < 2 {
		lines := strings.Split(wrapped, "\n")
		for i, l := range lines {
			lines[i] = truncate(l, w)
		}
		wrapped = strings.Join(lines, "\n")
	}
	return wrapped
}

// fitWidth clamps every line of s to at most width display columns — a final
// ANSI/grapheme-aware backstop for blocks whose chrome (a "> " quote or "∴"
// label prefix) can push a line past width at pathological small widths after the
// body itself was already wrapped to fit.
func fitWidth(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if lipgloss.Width(l) > width {
			lines[i] = truncate(l, width)
		}
	}
	return strings.Join(lines, "\n")
}

// collapseSpaces flattens runs of whitespace (incl. newlines) into single
// spaces so a multi-line command renders on one card line.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// firstLine returns everything before the first newline (or all of s).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// lastNonEmptyLine returns the trailing non-blank line of s, trimmed.
func lastNonEmptyLine(s string) string {
	for len(s) > 0 {
		i := strings.LastIndexByte(s, '\n')
		if line := strings.TrimSpace(s[i+1:]); line != "" {
			return line
		}
		if i < 0 {
			return ""
		}
		s = s[:i]
	}
	return ""
}

// formatInt converts an int to a string without importing strconv, matching the
// dashboard helper of the same name (so ported callers stay identical).
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if negative {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// fmtElapsed renders a duration as a compact clock (e.g. "12s", "1m03s"),
// matching the dashboard's tool/subagent elapsed format.
func fmtElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return formatInt(s) + "s"
	}
	mn := s / 60
	sec := s % 60
	pad := ""
	if sec < 10 {
		pad = "0"
	}
	return formatInt(mn) + "m" + pad + formatInt(sec) + "s"
}

// shortenPath trims a long absolute path to its last two segments.
func shortenPath(p string) string {
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// hangingPrefix applies a head prefix to the first line of a block and a
// 2-space indent to every continuation line, so a wrapped message aligns under
// its bullet/quote rather than back at column 0.
func hangingPrefix(s, firstPrefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = firstPrefix + lines[i]
		} else {
			lines[i] = "  " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// trimLeadingBlankLines drops leading whitespace-only lines from s (glamour
// emits a blank document-margin line that would otherwise orphan a bullet).
func trimLeadingBlankLines(s string) string {
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 || strings.TrimSpace(s[:i]) != "" {
			return s
		}
		s = s[i+1:]
	}
}

// --------------------------------------------------------------------------
// Captured-output sanitizers (H4/H5): make arbitrary terminal output safe to
// composite into a framed transcript without smearing it.
// --------------------------------------------------------------------------

// sanitizeToolOutput normalizes captured terminal output for in-frame display:
// CRLF becomes LF, a lone CR keeps only the text after the last one on its line
// (the final state a terminal shows for progress-bar rewrites), and every
// escape sequence except SGR color runs is dropped — cursor movement and
// erase-line controls would otherwise execute inside the composited frame. SGR
// survives so kit.RemapANSI can map it onto the palette.
func sanitizeToolOutput(s string) string {
	if !strings.ContainsAny(s, "\r\x1b\b") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = stripNonSGR(l)
	}
	return strings.Join(lines, "\n")
}

// stripNonSGR removes every ESC-introduced sequence except SGR (ESC[…m) from a
// single line, plus any stray C0 control bytes other than tab.
func stripNonSGR(s string) string {
	if !strings.ContainsAny(s, "\x1b\a\b\v\f") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\x1b' {
			j := ansiSeqEnd(s, i)
			if j-i >= 3 && s[i+1] == '[' && s[j-1] == 'm' {
				b.WriteString(s[i:j]) // SGR survives
			}
			i = j
			continue
		}
		if c < 0x20 && c != '\t' {
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// ansiSeqEnd returns the index just past the escape sequence starting at
// s[i] == ESC: CSI runs to its final byte (0x40–0x7e), string-introducer
// sequences (OSC/DCS/APC/PM/SOS) to BEL or ST, anything else as a 2-byte pair.
func ansiSeqEnd(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return j
	}
	switch s[j] {
	case '[':
		j++
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			j++
		}
		return j
	case ']', 'P', '_', '^', 'X':
		j++
		for j < len(s) {
			if s[j] == '\a' {
				return j + 1
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	default:
		return j + 1
	}
}

// expandTabs replaces tabs with spaces to the next 8-column stop. lipgloss.Width
// measures "\t" as 0 but terminals expand it, so a surviving tab renders up to 8
// columns wider than every downstream width budget believes.
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	const tabStop = 8
	var b strings.Builder
	b.Grow(len(s) + (tabStop-1)*strings.Count(s, "\t"))
	col := 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '\x1b': // zero-width escape: copy through
			j := ansiSeqEnd(s, i)
			b.WriteString(s[i:j])
			i = j
		case '\t':
			n := tabStop - col%tabStop
			for k := 0; k < n; k++ {
				b.WriteByte(' ')
			}
			col += n
			i++
		default:
			_, size := utf8.DecodeRuneInString(s[i:])
			b.WriteString(s[i : i+size])
			col++
			i += size
		}
	}
	return b.String()
}

// output-cap constants for the expanded captured-output window.
const (
	outputHeadLines = 20
	outputTailLines = 6
)

// clampOutputLines splits captured tool output into display lines, keeping the
// first outputHeadLines and last outputTailLines with a "… N lines hidden …"
// marker between them when longer. Trailing blank lines are trimmed; output is
// sanitized for in-frame display and tabs are expanded so the downstream
// truncation backstop sees the real display width.
func clampOutputLines(out string) []string {
	out = strings.TrimRight(sanitizeToolOutput(out), "\n")
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	if len(lines) > outputHeadLines+outputTailLines+1 {
		hidden := len(lines) - outputHeadLines - outputTailLines
		res := make([]string, 0, outputHeadLines+outputTailLines+1)
		res = append(res, lines[:outputHeadLines]...)
		res = append(res, "… "+formatInt(hidden)+" lines hidden …")
		res = append(res, lines[len(lines)-outputTailLines:]...)
		lines = res
	}
	for i, l := range lines {
		lines[i] = expandTabs(l)
	}
	return lines
}

// isDiffChange reports whether a unified-diff line is an addition or deletion.
// It accepts both an ASCII "-" (a raw `git diff` a host feeds directly) and the
// U+2212 "−" the internal permission-diff machinery emits, so a public consumer
// isn't forced onto one glyph.
func isDiffChange(l string) bool {
	return strings.HasPrefix(l, "+") || strings.HasPrefix(l, "−") || strings.HasPrefix(l, "-")
}

// condenseDiff keeps changed lines plus one line of surrounding context and
// collapses long unchanged runs into a "… N unchanged" marker, capping the
// result at maxLines (with a trailing "… more" when it overflows). Diff lines use
// "+" for additions and either ASCII "-" or U+2212 "−" for deletions.
func condenseDiff(lines []string, maxLines int) []string {
	n := len(lines)
	keep := make([]bool, n)
	for i, l := range lines {
		if isDiffChange(l) {
			keep[i] = true
			if i > 0 {
				keep[i-1] = true
			}
			if i+1 < n {
				keep[i+1] = true
			}
		}
	}
	var out []string
	for i := 0; i < n; {
		if keep[i] {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < n && !keep[j] {
			j++
		}
		if skipped := j - i; skipped > 0 {
			out = append(out, "… "+formatInt(skipped)+" unchanged")
		}
		i = j
	}
	if maxLines > 0 && len(out) > maxLines {
		out = append(out[:maxLines], "… more")
	}
	return out
}

// headArg budgets a tool card's argument for the "Name(arg)" head line: the
// collapsed arg ellipsized to budget columns, or "" with truncated=true when
// budget < 3 leaves no room to show it at all. truncated signals expansion has
// more of the argument to reveal.
func headArg(arg string, budget int) (shown string, truncated bool) {
	if arg == "" {
		return "", false
	}
	if budget < 3 {
		return "", true
	}
	full := collapseSpaces(arg)
	shown = truncate(full, budget)
	return shown, shown != full
}
