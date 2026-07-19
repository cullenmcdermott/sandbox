package chat

import (
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// OnThemeChange wires a transcript list to re-skin on every theme swap. It
// registers a theme.OnChange hook that drops the list's render cache (honoring
// the THEME-SWAP CONTRACT below), and returns the unsubscribe func. Call it once
// when the host builds its list:
//
//	l := list.New(items...)
//	chat.OnThemeChange(l)
//
// Without this (or a manual list.InvalidateAll() on swap) a /theme change
// re-skins only newly-rendered items and leaves the committed transcript stale.
func OnThemeChange(l *list.List) func() {
	return theme.OnChange(func() { l.InvalidateAll() })
}

// cache.go — the per-item render cache primitive. Each transcript item memoizes
// its last rendered string keyed on (width, content-hash, theme-epoch, display
// flags). The list widget already caches on (item, width, version), so this is a
// second, finer layer: it lets an item's Render recompute nothing when the list
// re-measures it at an unchanged width/version, and it folds the theme epoch into
// the key so an item asked to Render after a /theme swap rebuilds against the
// current palette (its old-epoch cache entry misses).
//
// THEME-SWAP CONTRACT: the epoch fold above only fires if Render is actually
// called. The outer list cache is keyed on (item, width, version) and a theme
// swap bumps no version, so the list would short-circuit and re-serve stale ANSI.
// A host must drop the list cache on swap — call list.InvalidateAll() from a
// theme.OnChange hook (see chat.OnThemeChange for a ready-made wiring). Then the
// list re-renders every item, each item's epoch slot misses, and the whole
// transcript re-skins.

// u64b encodes a uint64 as 8 little-endian bytes for length-framed hashing.
func u64b(x uint64) []byte {
	var b [8]byte
	for i := 0; i < 8; i++ {
		b[i] = byte(x >> (8 * i))
	}
	return b[:]
}

// extraKey folds the theme epoch and a display-flags bitset into one hash slot,
// length-framed so distinct (epoch, flags) tuples can never collide.
func extraKey(epoch, flags uint64) uint64 {
	return fnvFields(u64b(epoch), u64b(flags))
}

// flagBits packs a small set of booleans into a flags bitset for extraKey.
func flagBits(bits ...bool) uint64 {
	var f uint64
	for i, b := range bits {
		if b {
			f |= 1 << uint(i)
		}
	}
	return f
}
