// Package list implements a virtualized, cached, freezing list.
package list

import "strings"

// Item is one renderable row-block. Implementations bump their version on every
// change that alters Render's output; the list keys its cache on (item, width,
// version), so a stable version => cache hit => no re-render.
type Item interface {
	Render(width int) string // full styled block; may be multi-line
	Version() uint64         // monotonic; changes when output would change
	Finished() bool          // advisory: true once output is terminal (reserved; see §4)
}

// Versioned is an embeddable counter satisfying Item.Version().
type Versioned struct{ v uint64 }

func NewVersioned() *Versioned       { return &Versioned{} }
func (x *Versioned) Version() uint64 { return x.v }
func (x *Versioned) Bump()           { x.v++ }

type List struct {
	width, height int
	items         []Item
	offsetIdx     int // index of first (partially) visible item
	offsetLine    int // lines of items[offsetIdx] hidden above top (>=0)
	cache         map[Item]*entry
}

type entry struct {
	width   int
	version uint64
	content string
	lines   []string
	height  int
}

func New(items ...Item) *List {
	return &List{
		items: items,
		cache: make(map[Item]*entry),
	}
}

func (l *List) SetSize(width, height int) {
	if width != l.width {
		l.cache = make(map[Item]*entry)
	}
	l.width = width
	l.height = height
}

func (l *List) SetItems(items ...Item) {
	newItems := make([]Item, len(items))
	copy(newItems, items)

	// Build a set of surviving items (pointer-keyed).
	survivors := make(map[Item]struct{}, len(items))
	for _, it := range items {
		survivors[it] = struct{}{}
	}

	// Drop cache entries whose key is not in the new set.
	for key := range l.cache {
		if _, ok := survivors[key]; !ok {
			delete(l.cache, key)
		}
	}

	oldIdx := l.offsetIdx
	l.items = newItems
	if l.offsetIdx > len(l.items)-1 {
		l.offsetIdx = max(len(l.items)-1, 0)
	}
	if oldIdx != l.offsetIdx {
		l.offsetLine = 0
	}
}

func (l *List) AppendItems(items ...Item) {
	l.items = append(l.items, items...)
}

func (l *List) Len() int    { return len(l.items) }
func (l *List) Width() int  { return l.width }
func (l *List) Height() int { return l.height }

// HeightAt returns the rendered height of items[idx] in lines.
func (l *List) HeightAt(idx int) int {
	if idx < 0 || idx >= len(l.items) {
		return 0
	}
	return l.heightAt(idx)
}

func (l *List) Render() string {
	if len(l.items) == 0 {
		return ""
	}
	budget := max(l.height, 0)
	out := make([]string, 0, budget)
	idx, off := l.offsetIdx, l.offsetLine
	for idx < len(l.items) && len(out) < budget {
		il := l.renderItemEntry(idx).lines
		if off >= 0 && off < len(il) {
			vis := il[off:]
			if rem := budget - len(out); len(vis) > rem {
				vis = vis[:rem]
			}
			out = append(out, vis...)
		}
		idx++
		off = 0
	}
	return strings.Join(out, "\n")
}

func (l *List) TotalHeight() int {
	total := 0
	for i := range l.items {
		total += l.heightAt(i)
	}
	return total
}

func (l *List) Offset() int {
	offset := 0
	for i := 0; i < l.offsetIdx; i++ {
		offset += l.heightAt(i)
	}
	return offset + l.offsetLine
}

func (l *List) AtBottom() bool {
	if len(l.items) == 0 {
		return true
	}
	// Sum heights from the first visible item; the portion hidden above the
	// viewport (offsetLine) doesn't count toward what's below. We're at the
	// bottom iff the remaining content fits within the viewport. The early exit
	// must subtract offsetLine — otherwise a first visible item taller than the
	// viewport (with shorter items below) is wrongly judged "not at bottom",
	// which silently breaks the transcript's bottom-pin auto-scroll.
	total := 0
	for i := l.offsetIdx; i < len(l.items); i++ {
		total += l.heightAt(i)
		if total-l.offsetLine > l.height {
			return false
		}
	}
	return true
}

func (l *List) ScrollBy(lines int) {
	if len(l.items) == 0 || lines == 0 {
		return
	}
	if lines > 0 {
		if l.AtBottom() {
			return
		}
		l.offsetLine += lines
		cur := l.heightAt(l.offsetIdx)
		for l.offsetLine >= cur {
			l.offsetLine -= cur
			l.offsetIdx++
			if l.offsetIdx > len(l.items)-1 {
				l.GotoBottom()
				return
			}
			cur = l.heightAt(l.offsetIdx)
		}
		li, ll := l.lastOffsetItem()
		if l.offsetIdx > li || (l.offsetIdx == li && l.offsetLine > ll) {
			l.offsetIdx, l.offsetLine = li, ll
		}
	} else {
		// up
		l.offsetLine += lines // lines is negative
		for l.offsetLine < 0 {
			l.offsetIdx--
			if l.offsetIdx < 0 {
				l.GotoTop()
				return
			}
			l.offsetLine += l.heightAt(l.offsetIdx)
		}
	}
}

func (l *List) GotoTop() {
	l.offsetIdx = 0
	l.offsetLine = 0
}

func (l *List) GotoBottom() {
	l.offsetIdx, l.offsetLine = l.lastOffsetItem()
}

func (l *List) Invalidate(it Item) {
	delete(l.cache, it)
}

// renderItemEntry returns the cached or freshly rendered entry for items[idx].
func (l *List) renderItemEntry(idx int) *entry {
	raw := l.items[idx]
	e := l.cache[raw]
	v := raw.Version()
	if e != nil && e.width == l.width && e.version == v {
		return e
	}
	rendered := strings.TrimRight(raw.Render(l.width), "\n")
	lines := strings.Split(rendered, "\n")
	e2 := &entry{
		width:   l.width,
		version: raw.Version(), // re-read after Render
		content: rendered,
		lines:   lines,
		height:  len(lines),
	}
	l.cache[raw] = e2
	return e2
}

// heightAt returns the height of items[idx] (via cache).
func (l *List) heightAt(idx int) int {
	return l.renderItemEntry(idx).height
}

// lastOffsetItem returns the topmost (idx, lineOffset) that places the last item
// at the bottom of the viewport.
func (l *List) lastOffsetItem() (int, int) {
	total, idx := 0, len(l.items)-1
	for ; idx >= 0; idx-- {
		total += l.heightAt(idx)
		if total > l.height {
			break
		}
	}
	return max(idx, 0), max(total-l.height, 0)
}
