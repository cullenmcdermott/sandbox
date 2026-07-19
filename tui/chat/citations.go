package chat

// citations.go — the sources an assistant reply cites, rendered as a dim numbered
// footnote list under the message body: a "Sources:" header then one
// "N. title — url" line per citation. Lines are truncated (a wrapped URL reads
// worse than a clipped one), so the block is width-safe by construction. Fields
// originate from arbitrary web pages, so each is flattened to one plain-text line
// (every escape sequence dropped, whitespace collapsed) before formatting — a
// cited <title> must not restyle the footnote or smear the frame. This is the
// §2b citation footnote from the production transcript, made self-contained.

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Citation is one cited source.
type Citation struct {
	// Title is the page title (may be empty).
	Title string
	// URL is the source URL (may be empty).
	URL string
	// CitedText is the quoted span (unused in the footnote; a title-/url-less
	// citation renders nothing).
	CitedText string
}

// RenderCitations formats citations as the dim numbered footnote list, or ""
// when every citation is renderless (title- and url-less). Fold the result under
// an assistant body (inside its hanging indent) as the production transcript does.
func RenderCitations(citations []Citation, width int) string {
	lines := make([]string, 0, len(citations)+1)
	n := 0
	for _, c := range citations {
		title, url := sanitizeCitationField(c.Title), sanitizeCitationField(c.URL)
		var src string
		switch {
		case title != "" && url != "":
			src = title + " — " + url
		case title != "":
			src = title
		case url != "":
			src = url
		default:
			continue // schema-legal but renderless (CitedText only, or empty)
		}
		n++
		lines = append(lines, styCitation.Render(truncate("  "+formatInt(n)+". "+src, width)))
	}
	if n == 0 {
		return ""
	}
	return styCitation.Render(truncate("Sources:", width)) + "\n" + strings.Join(lines, "\n")
}

// sanitizeCitationField flattens a web-controlled citation string (title/url) to
// one plain-text line: every ANSI sequence dropped (even SGR — a cited page must
// not restyle the footnote), whitespace incl. \r\n collapsed to single spaces,
// then residual C0 controls removed.
func sanitizeCitationField(s string) string {
	if !strings.ContainsAny(s, "\r\n\t\x1b\a\b\v\f") {
		return s
	}
	return stripNonSGR(collapseSpaces(ansi.Strip(s)))
}
