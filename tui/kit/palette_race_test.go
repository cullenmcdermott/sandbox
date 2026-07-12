package kit

import (
	"image/color"
	"sync"
	"testing"
)

// COUNTER: the palette is swapped behind an atomic.Pointer, so two goroutines —
// one applying themes (SetANSITable/SetComponentColors), the others rendering
// through the palette (RemapANSI, Badge, Kbd, KV, Scrollbar, SectionHeader) —
// never race on shared memory. Run with -race: before the atomic.Pointer swap
// this hammered a plain global array + map and the detector flagged it (a
// concurrent map read/write would also panic outright). [S2]
func TestPaletteConcurrentSwapAndRead(t *testing.T) {
	t.Cleanup(func() {
		// Restore the on-brand defaults for any later test in this package.
		activePalette.Store(defaultPalette())
	})

	const esc = "\x1b["
	tableA := [16]color.RGBA{}
	tableB := [16]color.RGBA{}
	for i := range tableA {
		tableA[i] = color.RGBA{R: uint8(i), G: 0x10, B: 0x20, A: 0xff}
		tableB[i] = color.RGBA{R: 0x20, G: uint8(i), B: 0x30, A: 0xff}
	}
	colsA := ComponentColors{
		KbdKey: color.RGBA{R: 1, A: 0xff}, KbdLabel: color.RGBA{R: 2, A: 0xff},
		KbdSep: color.RGBA{R: 3, A: 0xff}, KVKey: color.RGBA{R: 4, A: 0xff},
		KVVal: color.RGBA{R: 5, A: 0xff}, ErrDetail: color.RGBA{R: 6, A: 0xff},
		ButtonBlur: color.RGBA{R: 7, A: 0xff}, Rule: color.RGBA{R: 8, A: 0xff},
		ScrollThumb: color.RGBA{R: 9, A: 0xff},
		Roles:       map[Role]color.Color{RoleBrand: color.RGBA{R: 10, A: 0xff}, RoleError: color.RGBA{R: 11, A: 0xff}},
	}
	colsB := ComponentColors{
		KbdKey: color.RGBA{G: 1, A: 0xff}, Rule: color.RGBA{G: 8, A: 0xff},
		ScrollThumb: color.RGBA{G: 9, A: 0xff},
		Roles:       map[Role]color.Color{RoleBrand: color.RGBA{G: 10, A: 0xff}, RoleInfo: color.RGBA{G: 12, A: 0xff}},
	}

	const iters = 2000
	var wg sync.WaitGroup

	// Writers: swap the ANSI table and the component palette back and forth.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if i%2 == 0 {
				SetANSITable(tableA)
				SetComponentColors(colsA)
			} else {
				SetANSITable(tableB)
				SetComponentColors(colsB)
			}
		}
	}()

	// Readers: exercise every palette-reading render path concurrently.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sink := 0
			for i := 0; i < iters; i++ {
				sink += len(RemapANSI(esc + "31mred" + esc + "0m"))
				sink += len(Badge("x", RoleBrand))
				sink += len(Kbd("a", "approve"))
				sink += len(KbdRow([2]string{"a", "b"}, [2]string{"c", "d"}))
				sink += len(KV("key", "val", 6))
				sink += len(Button("go", i%2 == 0))
				sink += len(ErrorBlock("t", "detail", "action"))
				sink += len(Scrollbar(10, 100, 10, i%90))
				sink += len(SectionHeader("h", 30, "info"))
			}
			_ = sink
		}()
	}

	wg.Wait()
}
