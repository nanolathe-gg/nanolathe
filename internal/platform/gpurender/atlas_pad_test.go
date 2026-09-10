package gpurender

import (
	"image"
	"testing"
)

// Every atlas the world transform samples reserves a border around each entry,
// so a source coordinate that floors one texel past an entry reads that entry's
// own edge (or, on the model page, the composition background) instead of the
// neighbour's pixels (docs/DESIGN_GPU_RENDERER.md §16.3 "Sampling"). Captures at
// 0.75x, 0.78x, 0.8x, 0.82x, 0.92x, 1.25x and 1.75x on the seeded Ashap Plateau
// scene showed exactly that read before these borders existed, and none after;
// the rest steps were byte-identical either way.

// padTileCell fills the ring outside a cell with the cell's own edge texels,
// corners included.
func TestPadTileCellCopiesTheCellEdge(t *testing.T) {
	const side, stride = 4, 4 + 2*tileAtlasPad
	const atlasW = 2 * stride
	buf := make([]byte, atlasW*stride*4)
	// One cell at the first grid position, filled with a value per texel, and a
	// neighbouring cell filled with a marker so a bleed is visible.
	gx, gy := tileAtlasPad, tileAtlasPad
	at := func(x, y int) int { return (y*atlasW + x) * 4 }
	for ty := 0; ty < side; ty++ {
		for tx := 0; tx < side; tx++ {
			buf[at(gx+tx, gy+ty)] = byte(10 + ty*side + tx)
			buf[at(gx+tx, gy+ty)+3] = 255
		}
	}
	for y := 0; y < stride; y++ {
		for x := stride; x < atlasW; x++ {
			buf[at(x, y)] = 200
		}
	}
	padTileCell(buf, atlasW, gx, gy, side)

	for k := 0; k < side; k++ {
		if got, want := buf[at(gx+k, gy-1)], buf[at(gx+k, gy)]; got != want {
			t.Fatalf("top border texel %d = %d, want the cell's own %d", k, got, want)
		}
		if got, want := buf[at(gx+k, gy+side)], buf[at(gx+k, gy+side-1)]; got != want {
			t.Fatalf("bottom border texel %d = %d, want the cell's own %d", k, got, want)
		}
		if got, want := buf[at(gx-1, gy+k)], buf[at(gx, gy+k)]; got != want {
			t.Fatalf("left border texel %d = %d, want the cell's own %d", k, got, want)
		}
		// The right border is the seam the play test saw: without it this texel
		// belongs to the next tile in the atlas row.
		if got, want := buf[at(gx+side, gy+k)], buf[at(gx+side-1, gy+k)]; got != want {
			t.Fatalf("right border texel %d = %d, want the cell's own %d (a neighbouring tile bled through)", k, got, want)
		}
	}
	if got, want := buf[at(gx-1, gy-1)], buf[at(gx, gy)]; got != want {
		t.Fatalf("corner texel = %d, want the cell's own %d", got, want)
	}
}

// The scene packer leaves at least one texel between entries, on every side, and
// an entry's own placement is still its first inner texel so no recorded source
// rectangle changes.
func TestSceneAtlasEntriesDoNotTouch(t *testing.T) {
	a := &sceneAtlas{}
	var rects []image.Rectangle
	for _, size := range [][2]int{{8, 8}, {3, 17}, {40, 2}, {8, 8}, {1, 1}} {
		e := a.allocate(size[0], size[1])
		if !e.ok {
			t.Fatalf("%v was not placed", size)
		}
		if int(e.w) != size[0] || int(e.h) != size[1] {
			t.Fatalf("%v was placed as %dx%d", size, e.w, e.h)
		}
		if e.x < sceneAtlasPad || e.y < sceneAtlasPad {
			t.Fatalf("%v was placed at (%d,%d), inside its own border", size, e.x, e.y)
		}
		rects = append(rects, image.Rect(int(e.x)-sceneAtlasPad, int(e.y)-sceneAtlasPad,
			int(e.x+e.w)+sceneAtlasPad, int(e.y+e.h)+sceneAtlasPad))
	}
	for i := range rects {
		for j := i + 1; j < len(rects); j++ {
			if rects[i].Overlaps(rects[j]) {
				t.Fatalf("entry %d %v overlaps entry %d %v with their borders", i, rects[i], j, rects[j])
			}
		}
	}
}

// The model page leaves a gutter around every slot. The page is cleared to the
// composition background over its whole used extent, so a commit that reads into
// the gutter finds index 1 and skips, which is what a clamp would do.
func TestModelSlotsKeepAGutter(t *testing.T) {
	p := &modelPage{maxH: 512}
	var rects []image.Rectangle
	for _, size := range [][2]int{{16, 16}, {9, 33}, {64, 4}, {16, 16}} {
		rect, ok := p.alloc(size[0], size[1])
		if !ok {
			t.Fatalf("%v was not placed", size)
		}
		if rect.Min.X%modelSlotAlign != 0 || rect.Min.Y%modelSlotAlign != 0 {
			t.Fatalf("%v landed on the odd origin %v", size, rect.Min)
		}
		rects = append(rects, rect.Inset(-modelSlotGutter))
	}
	for i := range rects {
		for j := i + 1; j < len(rects); j++ {
			if rects[i].Overlaps(rects[j]) {
				t.Fatalf("slot %d %v overlaps slot %d %v with their gutters", i, rects[i], j, rects[j])
			}
		}
	}
	// The cleared extent covers every gutter, so no slot's margin is stale.
	for i, r := range rects {
		if r.Max.X > p.usedW || r.Max.Y > p.usedH {
			t.Fatalf("slot %d's gutter %v reaches past the cleared extent %dx%d", i, r, p.usedW, p.usedH)
		}
	}
}
