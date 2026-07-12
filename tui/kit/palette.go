package kit

// palette.go — the kit's mutable, theme-swappable render colors, held behind a
// single atomic.Pointer so multiple tea.Programs sharing this process never race
// on a plain global map/array while one of them applies a theme.

import (
	"image/color"
	"sync/atomic"
)

// palette is the kit's complete set of mutable, theme-swappable render colors.
// It is held behind activePalette (an atomic.Pointer) and swapped whole on every
// SetANSITable/SetComponentColors, so a concurrent theme swap and render never
// race on shared memory. Readers load a consistent snapshot via pal(); writers
// copy-modify-store and never mutate a live palette in place.
type palette struct {
	// ansi is the ANSI-16 → RGB remap table RemapANSI maps basic SGR colors onto
	// (normal 0–7 then bright 8–15).
	ansi [16]color.RGBA

	// Component colors (see ComponentColors for the exported mirror).
	kbdKey, kbdLabel, kbdSep color.Color
	kvKey, kvVal             color.Color
	errDetail, btnBlur       color.Color
	rule, thumb              color.Color

	// roles maps each Role to its accent color, indexed by Role.
	roles [numRoles]color.Color
}

// activePalette holds the live palette. It is swapped atomically: readers load a
// snapshot via pal(), writers copy-modify-store. Initialized before any theme
// swap by defaultPalette so reads are always valid.
var activePalette atomic.Pointer[palette]

func init() { activePalette.Store(defaultPalette()) }

// pal returns the live palette snapshot. Never mutate the returned value in
// place — copy it, change the copy, and Store it (as the setters do).
func pal() *palette { return activePalette.Load() }

// defaultPalette builds the on-brand default palette used before any theme is
// applied. Values are the xterm-ish on-brand defaults the kit shipped with.
func defaultPalette() *palette {
	p := &palette{
		ansi: [16]color.RGBA{
			{0x1e, 0x1e, 0x1e, 0xff}, {0xd7, 0x4e, 0x4e, 0xff}, {0x4e, 0xd7, 0x6b, 0xff}, {0xd7, 0xc6, 0x4e, 0xff},
			{0x4e, 0x8c, 0xd7, 0xff}, {0xb0, 0x6e, 0xd7, 0xff}, {0x4e, 0xc9, 0xd7, 0xff}, {0xd0, 0xd0, 0xd0, 0xff},
			{0x6b, 0x6b, 0x6b, 0xff}, {0xff, 0x7b, 0x7b, 0xff}, {0x7b, 0xff, 0x9a, 0xff}, {0xff, 0xf0, 0x7b, 0xff},
			{0x7b, 0xb6, 0xff, 0xff}, {0xd9, 0x9c, 0xff, 0xff}, {0x7b, 0xf2, 0xff, 0xff}, {0xff, 0xff, 0xff, 0xff},
		},
		kbdKey:    color.RGBA{R: 0x7b, G: 0xb6, B: 0xff, A: 0xff},
		kbdLabel:  color.RGBA{R: 0xb6, G: 0xaf, B: 0xd2, A: 0xff},
		kbdSep:    color.RGBA{R: 0x6b, G: 0x6b, B: 0x6b, A: 0xff},
		kvKey:     color.RGBA{R: 0x92, G: 0x8a, B: 0xae, A: 0xff},
		kvVal:     color.RGBA{R: 0xb6, G: 0xaf, B: 0xd2, A: 0xff},
		errDetail: color.RGBA{R: 0xb6, G: 0xaf, B: 0xd2, A: 0xff},
		btnBlur:   color.RGBA{R: 0xb6, G: 0xaf, B: 0xd2, A: 0xff},
		rule:      color.RGBA{R: 0x6b, G: 0x6b, B: 0x6b, A: 0xff},
		thumb:     color.RGBA{R: 0xb0, G: 0x6e, B: 0xd7, A: 0xff},
	}
	p.roles = [numRoles]color.Color{
		RoleBrand:   color.RGBA{R: 0x6b, G: 0x50, B: 0xff, A: 0xff},
		RoleBusy:    color.RGBA{R: 0xd9, G: 0xe6, B: 0x4e, A: 0xff},
		RoleWaiting: color.RGBA{R: 0xff, G: 0xc2, B: 0x47, A: 0xff},
		RoleSuccess: color.RGBA{R: 0x2f, G: 0xd9, B: 0x8b, A: 0xff},
		RoleDenied:  color.RGBA{R: 0xe0, G: 0x8a, B: 0x4a, A: 0xff},
		RoleError:   color.RGBA{R: 0xff, G: 0x52, B: 0x77, A: 0xff},
		RoleInfo:    color.RGBA{R: 0x54, G: 0xcb, B: 0xe0, A: 0xff},
		RoleMuted:   color.RGBA{R: 0x80, G: 0x79, B: 0xa0, A: 0xff},
	}
	return p
}
