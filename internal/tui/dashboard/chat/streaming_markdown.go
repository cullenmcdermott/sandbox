// Package chat implements streaming markdown rendering with stable-prefix caching.
package chat

import (
	"strings"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// MarkdownRenderer is satisfied by *glamour.TermRenderer (Render(string)(string,error)).
// Abstracting it lets tests inject a byte-counting wrapper.
type Renderer interface {
	Render(string) (string, error)
}

// StreamingMarkdown caches the render of a provably-safe stable prefix so each
// flush only renders the part that grew, instead of re-running glamour over the
// whole growing buffer per delta (the O(deltas²) cost the streaming design set
// out to kill). It exploits two empirical properties of glamour at blank-line
// block boundaries:
//
//   - Append-only: for any content A ending at a safe boundary, the trimmed
//     render of A is a byte-exact PREFIX of render(A+B) — rendering more
//     complete blocks only appends bytes; earlier bytes never change.
//   - Local junctions: the bytes a run of blocks contributes after A depend
//     only on A's LAST block (the inter-block margin is pairwise), not on
//     anything earlier. So the contribution can be recomputed from just
//     render(lastBlock + newPart) with render(lastBlock)'s prefix stripped,
//     feeding the renderer only ~one block of input per delta.
//
// The result is byte-for-byte identical to a full glamour render with the
// trailing newline trimmed (TestStreamingEqualsFullRenderEveryPrefix) at
// sub-quadratic renderer cost (TestStreamingIsSubQuadratic). All rendering is
// routed through the injected Renderer so the behavioral byte counter in the
// sub-quadratic test actually engages.
type StreamingMarkdown struct {
	width int
	// epoch is the theme epoch the cached stableRender was built under; a swap
	// bumps it and forces a reset so the streamed prefix can't mix palettes.
	epoch uint64
	// stableSource is the content up to the latest safe boundary that has been
	// folded into stableRender; stableRender is its trimmed render.
	stableSource string
	stableRender string
	// junctionCtx is the last complete block of stableSource — the only context
	// that affects how the next blocks render — and junctionCtxLen is the length
	// of its trimmed standalone render (the strip offset when folding deltas).
	junctionCtx    string
	junctionCtxLen int
	// scan is the incremental safe-boundary/link-ref scanner. It processes each
	// newly-arrived complete line exactly once across the turn instead of
	// rescanning the whole growing buffer per delta (the O(N²) the boundary
	// predicates otherwise cost). It is content-driven and independent of the
	// render cache: a width or theme swap leaves it untouched (content is the
	// same); only a non-prefix content rewrite resets it (handled inside sync).
	scan mdScanner
}

// Reset drops all cached state.
func (s *StreamingMarkdown) Reset() {
	*s = StreamingMarkdown{}
}

// resetCache clears the incremental cache (used on width change or a non-prefix
// rewrite).
func (s *StreamingMarkdown) resetCache() {
	s.stableSource, s.stableRender = "", ""
	s.junctionCtx, s.junctionCtxLen = "", 0
}

// trimmedRender runs r over s and trims the single trailing newline, matching
// the renderMarkdown contract the canonical oracle compares against.
func trimmedRender(r Renderer, s string) (string, bool) {
	out, err := r.Render(s)
	if err != nil {
		return "", false
	}
	return strings.TrimSuffix(out, "\n"), true
}

// lastCompleteBlock returns the final block of s (which ends at a safe
// boundary): the substring starting at the latest safe boundary strictly inside
// s, or all of s when s is a single block.
func lastCompleteBlock(s string) string {
	for _, p := range blankLineBoundaries(s) { // latest first
		if p < len(s) && isSafeBoundaryAt(s, p) {
			return s[p:]
		}
	}
	return s
}

// setJunction records block as the junction context and renders it once to cache
// the strip offset for subsequent deltas.
func (s *StreamingMarkdown) setJunction(r Renderer, block string) bool {
	out, ok := trimmedRender(r, block)
	if !ok {
		return false
	}
	s.junctionCtx, s.junctionCtxLen = block, len(out)
	return true
}

// Render returns the render of content at width, byte-for-byte identical to a
// full glamour render with the trailing newline trimmed.
func (s *StreamingMarkdown) Render(content string, width int, r Renderer) string {
	// A theme swap re-palettes the renderer (the pool is invalidated); drop the
	// stale-palette stable prefix so it isn't concatenated with new-palette deltas.
	if e := theme.Epoch(); e != s.epoch {
		s.epoch = e
		s.resetCache()
	}
	if width != s.width {
		s.width = width
		s.resetCache()
	}
	// A non-prefix rewrite (retry) invalidates the incremental cache.
	if !strings.HasPrefix(content, s.stableSource) {
		s.resetCache()
	}
	// Advance the incremental scanner to reflect content (resets itself on a
	// non-prefix rewrite). It feeds both the link-ref-def and safe-boundary
	// predicates below without rescanning the whole buffer.
	s.scan.sync(content)
	if content == "" {
		if out, ok := trimmedRender(r, ""); ok {
			return out
		}
		return content
	}
	// Link reference definitions have document-global scope: a definition can
	// retroactively turn an earlier paragraph's [label] into a hyperlink, which
	// breaks the append-only invariant the cache relies on. They are rare, so
	// fall back to a full render whenever one is present.
	if s.scan.hasLinkRef(content) {
		s.resetCache()
		if out, ok := trimmedRender(r, content); ok {
			return out
		}
		return content
	}

	// Split into the stable boundary-aligned prefix and the trailing partial.
	stable, tail := "", content
	if b := s.scan.findSafeBoundary(content); b >= 0 {
		stable, tail = content[:b], content[b:]
	}

	// Bring the cached stable render up to date with stable.
	if stable != s.stableSource {
		if s.stableSource == "" || !strings.HasPrefix(stable, s.stableSource) {
			// First (or post-reset) stable region: render directly — the first
			// block has no leading inter-block margin.
			out, ok := trimmedRender(r, stable)
			if !ok {
				return content
			}
			s.stableSource, s.stableRender = stable, out
			if !s.setJunction(r, lastCompleteBlock(stable)) {
				return content
			}
		} else {
			// Fold the new blocks: their contribution after stableSource equals
			// their contribution after just stableSource's last block, with that
			// block's (prefix) render stripped.
			newPart := stable[len(s.stableSource):]
			combined, ok := trimmedRender(r, s.junctionCtx+newPart)
			if !ok || len(combined) < s.junctionCtxLen {
				return content
			}
			s.stableRender += combined[s.junctionCtxLen:]
			s.stableSource = stable
			if !s.setJunction(r, lastCompleteBlock(stable)) {
				return content
			}
		}
	}

	// No trailing partial: the stable render is the whole answer.
	if tail == "" {
		return s.stableRender
	}
	// No stable prefix yet (no safe boundary): render content directly.
	if s.stableSource == "" {
		if out, ok := trimmedRender(r, content); ok {
			return out
		}
		return content
	}
	// Stable render + the partial's contribution, computed against the last
	// stable block so the junction is correct without re-rendering the prefix.
	combined, ok := trimmedRender(r, s.junctionCtx+tail)
	if !ok || len(combined) < s.junctionCtxLen {
		return content
	}
	return s.stableRender + combined[s.junctionCtxLen:]
}

// mdScanner is an incremental, line-oriented scan of the streamed content that
// answers the same safe-boundary and link-reference-definition predicates as the
// from-scratch free functions below, but processes each complete line exactly
// once across the turn. Content is append-only within a turn, so content[:p] for
// a fixed line-boundary p is immutable: the follow-independent checks (open
// fence, open hazard, last-line-opens-a-construct) are computed once per boundary
// and cached forever. Only the setext-underline "what follows" check depends on
// later content, so it is (re)evaluated per query against the current tail.
//
// The scanner is validated line-for-line against the reference free functions by
// TestIncrementalScannerMatchesReference across 1-byte, whole, and random
// chunkings, so any divergence — including partial-line deltas — fails the build.
type mdScanner struct {
	// committed is content up to (and including) the last consumed '\n'; it is a
	// slice of content (shared backing array, O(1) to keep) and doubles as the
	// continuity key: if a new content no longer has it as a prefix, the content
	// was rewritten and the scanner resets.
	committed string
	// fence tracking, identical to isInsideOpenFence/fenceInfo semantics.
	fenceChar byte
	fenceLen  int
	// hazardSeen mirrors prefixHasOpenHazard: monotonic once any non-fenced list
	// marker / HTML-block opener / link-ref-def line appears.
	hazardSeen bool
	// linkRefSeen mirrors hasLinkRefDef over committed lines (monotonic); the
	// trailing partial line is re-checked per query (hasLinkRef).
	linkRefSeen bool
	// lastNonBlank is the last committed line with non-whitespace content
	// (fence-agnostic, matching lastNonBlankLine).
	lastNonBlank string
	// bounds are the blank-line boundaries with their follow-independent safety
	// snapshot, in ascending offset order.
	bounds []scanBound
}

type scanBound struct {
	off  int  // start of the line after a blank-line separator
	safe bool // checks (1) fence, (2) hazard, (3) last-line-opens — from content[:off]
}

func (sc *mdScanner) reset() { *sc = mdScanner{} }

// sync advances the scanner to reflect content. Content is append-only within a
// turn; a content that no longer extends the committed prefix is a rewrite and
// resets the scanner. Only newly-arrived complete lines are processed.
func (sc *mdScanner) sync(content string) {
	if !strings.HasPrefix(content, sc.committed) {
		sc.reset()
	}
	for {
		start := len(sc.committed)
		nl := strings.IndexByte(content[start:], '\n')
		if nl < 0 {
			break // the rest is a partial trailing line, not yet committed
		}
		nlIdx := start + nl
		sc.commitLine(content[start:nlIdx], start, nlIdx)
		sc.committed = content[:nlIdx+1]
	}
}

// commitLine folds one complete line (without its trailing '\n') into the scan
// state. start is the line's offset in content; nlIdx is the offset of its '\n'.
func (sc *mdScanner) commitLine(line string, start, nlIdx int) {
	inFence := sc.fenceChar != 0
	if fc, fl := fenceInfo(line); fc != 0 {
		if sc.fenceChar == 0 {
			sc.fenceChar, sc.fenceLen = fc, fl // open
		} else if cc, cl := closingFenceInfo(line); cc == sc.fenceChar && cl >= sc.fenceLen {
			sc.fenceChar, sc.fenceLen = 0, 0 // close
		}
		// wrong char or insufficient length: literal content inside the fence.
	} else if !inFence {
		// Outside any open fence: detect open hazards and link-ref definitions
		// exactly as prefixHasOpenHazard / hasLinkRefDef do per line.
		if t := strings.TrimLeft(line, " \t"); t != "" {
			if isListItemMarker(t) || isHTMLBlockOpener(line) || isLinkRefDefinition(line) {
				sc.hazardSeen = true
			}
		}
		if isLinkRefDefinition(line) {
			sc.linkRefSeen = true
		}
	}
	// lastNonBlankLine is fence-agnostic — every non-whitespace line counts.
	if strings.TrimSpace(line) != "" {
		sc.lastNonBlank = line
	}
	// A blank (space/tab-only) complete line preceded by a newline opens a
	// boundary at the next line — the blank line itself changes none of the
	// state above, so the safety snapshot is taken as-is.
	if start > 0 && isSpaceTabLine(line) {
		safe := sc.fenceChar == 0 && !sc.hazardSeen &&
			(sc.lastNonBlank == "" || !lineOpensConstruct(sc.lastNonBlank))
		sc.bounds = append(sc.bounds, scanBound{off: nlIdx + 1, safe: safe})
	}
}

// findSafeBoundary returns the byte offset of the latest safe boundary in
// content, or -1 — the incremental equivalent of the free findSafeBoundary.
func (sc *mdScanner) findSafeBoundary(content string) int {
	for i := len(sc.bounds) - 1; i >= 0; i-- {
		b := sc.bounds[i]
		if !b.safe {
			continue
		}
		// (4) What follows must not be a setext underline (would retro-headerify
		// the prefix). This depends on the current tail, so it is evaluated here.
		if rest := content[b.off:]; rest != "" && isSetextUnderline(firstNonBlankLine(rest)) {
			continue
		}
		return b.off
	}
	return -1
}

// hasLinkRef reports whether content contains a link reference definition —
// the incremental equivalent of the free hasLinkRefDef. Committed lines are
// tracked in linkRefSeen; the trailing partial line is checked here with the
// current fence state (a delta can land mid link-ref-def line).
func (sc *mdScanner) hasLinkRef(content string) bool {
	if sc.linkRefSeen {
		return true
	}
	tail := content[len(sc.committed):]
	if tail == "" || sc.fenceChar != 0 {
		return false // no partial line, or inside an open fence
	}
	if c, _ := fenceInfo(tail); c != 0 {
		return false // a fence line is never a link-ref definition
	}
	return isLinkRefDefinition(tail)
}

// isSpaceTabLine reports whether line is empty or only spaces/tabs, matching the
// blank-line separator blankLineBoundaries recognizes.
func isSpaceTabLine(line string) bool {
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return false
		}
	}
	return true
}

// findSafeBoundary returns the byte offset of the latest safe boundary, or -1.
func findSafeBoundary(content string) int {
	for _, p := range blankLineBoundaries(content) { // latest first
		if isSafeBoundaryAt(content, p) {
			return p
		}
	}
	return -1
}

// blankLineBoundaries returns offsets just after each blank-line separator
// ("\n" + optional spaces/tabs + "\n"), latest first. The offset is the start of
// the first line following the separator.
func blankLineBoundaries(content string) []int {
	var res []int
	for i := 0; i+1 < len(content); i++ {
		if content[i] != '\n' {
			continue
		}
		j := i + 1
		for j < len(content) && (content[j] == ' ' || content[j] == '\t') {
			j++
		}
		if j < len(content) && content[j] == '\n' {
			res = append(res, j+1)
		}
	}
	// reverse to latest-first
	for a, b := 0, len(res)-1; a < b; a, b = a+1, b-1 {
		res[a], res[b] = res[b], res[a]
	}
	return res
}

func isSafeBoundaryAt(content string, p int) bool {
	prefix := content[:p]

	// (1) No open fenced code block (L2: uses char-aware check so ``` can
	// only be closed by `` ` ``, not ~~~).
	if isInsideOpenFence(prefix) {
		return false
	}
	// (2) No open-able hazard anywhere outside fences: list, HTML block, link ref def.
	if prefixHasOpenHazard(prefix) {
		return false
	}
	// (3) Last non-blank line must not keep a construct open.
	if last := lastNonBlankLine(prefix); last != "" && lineOpensConstruct(last) {
		return false
	}
	// (4) What follows must not be a setext underline (would retro-headerify the prefix).
	if rest := content[p:]; rest != "" {
		if isSetextUnderline(firstNonBlankLine(rest)) {
			return false
		}
	}
	return true
}
func prefixHasOpenHazard(prefix string) bool {
	var openerChar byte
	var openerLen int
	for _, line := range strings.Split(prefix, "\n") {
		fc, fl := fenceInfo(line)
		if fc != 0 {
			if openerChar == 0 {
				openerChar, openerLen = fc, fl
			} else if cc, cl := closingFenceInfo(line); cc == openerChar && cl >= openerLen {
				openerChar, openerLen = 0, 0 // closed
			}
			// wrong char or insufficient length: literal content inside fence
			continue
		}
		if openerChar != 0 {
			continue // inside open fence
		}
		t := strings.TrimLeft(line, " \t")
		if t == "" {
			continue
		}
		if isListItemMarker(t) || isHTMLBlockOpener(line) || isLinkRefDefinition(line) {
			return true
		}
	}
	return false
}

func lineOpensConstruct(line string) bool {
	if len(line) > 0 && line[0] == '\t' {
		return true
	}
	if strings.HasPrefix(line, "    ") { // indented code
		return true
	}
	t := strings.TrimLeft(line, " \t")
	if t == "" {
		return false
	}
	if t[0] == '>' { // block quote
		return true
	}
	if isListItemMarker(t) {
		return true
	}
	if strings.ContainsRune(line, '|') { // table (conservative)
		return true
	}
	if isSetextUnderline(t) {
		return true
	}
	return false
}

// isInsideOpenFence reports whether the string ends with an open fenced code
// block. It tracks the opener character (` or ~) and its run length, closing
// only when the same char is seen with run >= opener length (L2 fix: char;
// L3 fix: length).
func isInsideOpenFence(s string) bool {
	var openerChar byte
	var openerLen int
	for _, line := range strings.Split(s, "\n") {
		fc, fl := fenceInfo(line)
		if fc == 0 {
			continue
		}
		if openerChar == 0 {
			openerChar, openerLen = fc, fl // open a block
		} else if cc, cl := closingFenceInfo(line); cc == openerChar && cl >= openerLen {
			openerChar, openerLen = 0, 0 // closed
		}
		// wrong char or insufficient length: literal content inside fence
	}
	return openerChar != 0
}

// fenceInfo returns the fence character and run length of a fence line, or
// (0, 0) if the line is not a fence line.
func fenceInfo(line string) (c byte, n int) {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) {
		return 0, 0
	}
	ch := line[i]
	if ch != '`' && ch != '~' {
		return 0, 0
	}
	run := 0
	for i < len(line) && line[i] == ch {
		i++
		run++
	}
	if run < 3 {
		return 0, 0
	}
	return ch, run
}

// closingFenceInfo returns a fence only when the line is valid as a closing
// fence: after the fence run, only spaces or tabs may follow.
func closingFenceInfo(line string) (c byte, n int) {
	c, n = fenceInfo(line)
	if c == 0 {
		return 0, 0
	}
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	for i < len(line) && line[i] == c {
		i++
	}
	for i < len(line) {
		if line[i] != ' ' && line[i] != '\t' {
			return 0, 0
		}
		i++
	}
	return c, n
}

func isListItemMarker(t string) bool { // t is left-trimmed
	if t == "" {
		return false
	}
	if c := t[0]; c == '-' || c == '*' || c == '+' {
		// Must be followed by a space/tab — but also must not be a thematic break
		// (L4 fix). A thematic break looks like "* * *", "- - -", "___", etc.:
		// 3+ repetitions of the same char with optional spaces and nothing else.
		if !isThematicBreak(t) {
			return len(t) >= 2 && (t[1] == ' ' || t[1] == '\t')
		}
		return false
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == 0 || i > 9 || i+1 >= len(t) {
		return false
	}
	if t[i] != '.' && t[i] != ')' {
		return false
	}
	return t[i+1] == ' ' || t[i+1] == '\t'
}

// isThematicBreak reports whether a left-trimmed line is a thematic break
// (CommonMark §4.1): 3+ occurrences of *, -, or _ optionally separated by spaces/tabs.
func isThematicBreak(t string) bool {
	if t == "" {
		return false
	}
	c := t[0]
	if c != '*' && c != '-' && c != '_' {
		return false
	}
	n := 0
	for _, b := range []byte(t) {
		if b == c {
			n++
		} else if b != ' ' && b != '\t' {
			return false // contains other chars
		}
	}
	return n >= 3
}

func isSetextUnderline(line string) bool {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i == len(line) {
		return false
	}
	c := line[i]
	if c != '=' && c != '-' {
		return false
	}
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	for ; j < len(line); j++ {
		if line[j] != ' ' && line[j] != '\t' {
			return false
		}
	}
	return true
}

func isHTMLBlockOpener(line string) bool {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	rest := line[i:]
	if len(rest) < 2 || rest[0] != '<' {
		return false
	}
	switch {
	case strings.HasPrefix(rest, "<!--"), strings.HasPrefix(rest, "<?"),
		strings.HasPrefix(rest, "<![CDATA["):
		return true
	case len(rest) >= 3 && rest[1] == '!' && isASCIILetter(rest[2]):
		return true
	}
	low := strings.ToLower(rest)
	for _, tag := range []string{"<script", "<pre", "<style", "<textarea"} {
		if strings.HasPrefix(low, tag) {
			var next byte
			if len(low) > len(tag) {
				next = low[len(tag)]
			}
			if next == 0 || next == ' ' || next == '\t' || next == '>' {
				return true
			}
		}
	}
	j := 1
	if j < len(rest) && rest[j] == '/' {
		j++
	}
	return j < len(rest) && isASCIILetter(rest[j])
}

// hasLinkRefDef reports whether content contains a link reference definition
// line ("[label]: dest"), whose document-global scope can retroactively change
// how earlier blocks render.
func hasLinkRefDef(content string) bool {
	var openerChar byte
	var openerLen int
	for _, line := range strings.Split(content, "\n") {
		fc, fl := fenceInfo(line)
		if fc != 0 {
			if openerChar == 0 {
				openerChar, openerLen = fc, fl
			} else if cc, cl := closingFenceInfo(line); cc == openerChar && cl >= openerLen {
				openerChar, openerLen = 0, 0
			}
			continue
		}
		if openerChar != 0 {
			continue
		}
		if isLinkRefDefinition(line) {
			return true
		}
	}
	return false
}

func isLinkRefDefinition(line string) bool {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || line[i] != '[' {
		if i < len(line) && line[i] == '>' {
			return isLinkRefDefinition(strings.TrimLeft(line[i+1:], " \t"))
		}
		return false
	}
	i++
	start := i
	escaped := false
	for i < len(line) {
		if escaped {
			escaped = false
			i++
			continue
		}
		if line[i] == '\\' {
			escaped = true
			i++
			continue
		}
		if line[i] == ']' {
			break
		}
		i++
	}
	if i >= len(line) || i == start {
		return false
	}
	i++ // past ']'
	if i >= len(line) || line[i] != ':' {
		return false
	}
	return true
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func lastNonBlankLine(s string) string {
	last := ""
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			last = l
		}
	}
	return last
}

func firstNonBlankLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}
