package dashboard

// render_util.go — small ANSI-aware layout helpers shared by the dashboard,
// the activity feed, and the overlays. Relocated here from the deleted
// transcript renderer (claude-pane-first) so the surviving surfaces keep them.

import (
	"encoding/json"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// toolArg extracts the identifying argument for a tool call from its input JSON
// (the field a given tool is keyed on), for the list row's pending-permission
// summary. Relocated from the deleted transcript renderer.
func toolArg(tool string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	get := func(keys ...string) string {
		var raw map[string]any
		if json.Unmarshal(input, &raw) != nil {
			return ""
		}
		for _, k := range keys {
			if v, ok := raw[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch tool {
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		if p := get("file_path", "notebook_path", "path"); p != "" {
			return shortenPath(p)
		}
	case "Bash":
		return collapseSpaces(get("command"))
	case "Grep", "Glob":
		return get("pattern")
	case "WebFetch":
		return get("url")
	case "WebSearch":
		return get("query")
	}
	if p := get("file_path", "path", "command", "pattern", "url", "query"); p != "" {
		return collapseSpaces(p)
	}
	return ""
}

// fitPathTail fits p into maxW display columns by dropping WHOLE leading path
// segments and marking the cut with "…/".
//
// Cutting from the left rather than the right is the point: a worktree path is
// <stateDir>/worktrees/<session-id>, so every session's differs only in its
// final segment. Right-truncation (the plain truncate helper) would render them
// all as the same "~/.local/share/sandbox/remote-ses…" and lose exactly the part
// that identifies the session. Whole segments are dropped rather than characters
// so what remains is still a readable path suffix.
func fitPathTail(p string, maxW int) string {
	if maxW <= 0 || lipgloss.Width(p) <= maxW {
		return truncate(p, maxW)
	}
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		if cand := "…/" + strings.Join(parts[i:], "/"); lipgloss.Width(cand) <= maxW {
			return cand
		}
	}
	// Even the bare final segment overflows the column: fall back to a plain
	// character truncation of it, which is all the width can carry.
	return truncate(parts[len(parts)-1], maxW)
}

// shortenPath trims a long absolute path to its last two segments.
func shortenPath(p string) string {
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// collapseSpaces flattens runs of whitespace (incl. newlines) into single spaces
// so a multi-line command renders on one line.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// fitModal normalizes s to an exact w×h block: truncate over-long lines
// (ANSI-aware), pad short lines to width, and pad/truncate the line count to h.
// It is the cheap fill both the feed body and overlay cards use instead of a
// lipgloss Style.Width().Height().Render() (which is ~830µs/33k allocs per frame
// on tall content), with byte-identical output for in-width lines.
func fitModal(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		// ONE width scan per line, not two. lipgloss.Width parses the whole line
		// for SGR sequences, and the obvious spelling of this loop
		// (`if Width(l) > w { truncate }; padRight(l, w)`) pays for that twice,
		// since padRight measures again.
		//
		// Measured (BenchmarkFitModal, amd64): the tall-feed case (h=120,w=200)
		// went 1.35ms → 1.00ms per call, and 20×80 89µs → 65µs — ~26% off, not
		// the 50% the scan count suggests, because the rest is Split/Join and the
		// padding Repeat rather than width measurement. Worth keeping (that case
		// was ~8% of a 60fps frame budget) but NOT evidence that further
		// micro-optimizing this loop pays; the remaining cost is allocation.
		//
		// An over-wide line is re-measured after truncation because ansi.Truncate
		// lands at most at w — grapheme clusters mean it can land BELOW w, and
		// that shortfall is exactly what the padding has to make up.
		lw := lipgloss.Width(l)
		if lw > w {
			l = ansi.Truncate(l, w, "")
			lw = lipgloss.Width(l)
		}
		if lw < w {
			l += strings.Repeat(" ", w-lw)
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

// padTo right-pads s with spaces to width w (ANSI-aware), leaving an already
// wide-enough string unchanged.
func padTo(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
