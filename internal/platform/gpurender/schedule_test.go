package gpurender

import (
	"image"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
)

// schedulerFixture is a renderer whose Sink methods compile but never submit, so
// these tests need no device: nothing here calls a barrier (Clear, Fog, a model
// commit or Expand).
func schedulerFixture(t *testing.T) (*Renderer, *formats.GAFFrame) {
	t.Helper()
	skipAfterDeviceLoop(t)
	pal := &palette.Tables{}
	r, err := NewChecked(pal, 64, 64)
	if err != nil {
		t.Skipf("GPU shaders unavailable: %v", err)
	}
	return r, &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{7}, Transparent: []bool{false}}
}

// The phase rules of docs/DESIGN_GPU_RENDERER.md §11.2: a destination-reading
// command that overlaps another destination-reading command of the current phase
// opens the next phase, and so does an opaque command that would otherwise be
// drawn before a result it must overwrite. Sharing a 32-pixel cell without
// overlapping does not, because the cell tag names its owner and the two
// rectangles are then compared exactly.
func TestSchedulerOpensPhasesOnRectangleOverlap(t *testing.T) {
	r, _ := schedulerFixture(t)
	frame := &formats.GAFFrame{Width: 8, Height: 8, Pixels: make([]byte, 64), Transparent: make([]bool, 64)}
	r.sched.resetFrame(64, 64)

	r.Fill(drawlist.Fill{Rect: drawlist.Rect{W: 64, H: 64}, Index: 3})
	if got := len(r.sched.phases); got != 1 {
		t.Fatalf("after one opaque fill: %d phases, want 1", got)
	}
	// First destination-reading command: nothing is tagged yet, so it joins.
	r.Sprite(drawlist.Sprite{Frame: frame, X: 0, Y: 0, Kind: drawlist.BlitTinted})
	if got := len(r.sched.phases); got != 1 {
		t.Fatalf("after the first tinted sprite: %d phases, want 1", got)
	}
	// Overlapping the first: it reads that result, so it opens the next phase.
	r.Sprite(drawlist.Sprite{Frame: frame, X: 4, Y: 4, Kind: drawlist.BlitTinted})
	if got := len(r.sched.phases); got != 2 {
		t.Fatalf("after the overlapping tinted sprite: %d phases, want 2", got)
	}
	// Inside the same 32-pixel cell as the previous one but disjoint from it:
	// the exact rectangles decide, so it joins the current phase.
	r.Sprite(drawlist.Sprite{Frame: frame, X: 16, Y: 16, Kind: drawlist.BlitTinted})
	if got := len(r.sched.phases); got != 2 {
		t.Fatalf("after the same-cell disjoint tinted sprite: %d phases, want 2", got)
	}
	// A different cell also rejoins the current phase.
	r.Sprite(drawlist.Sprite{Frame: frame, X: 40, Y: 40, Kind: drawlist.BlitTinted})
	if got := len(r.sched.phases); got != 2 {
		t.Fatalf("after the disjoint tinted sprite: %d phases, want 2", got)
	}
	// An opaque write over a pixel this phase's destination batch covered must
	// overwrite it, so it opens the next phase.
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 5, Y: 5, W: 1, H: 1}, Index: 9})
	if got := len(r.sched.phases); got != 3 {
		t.Fatalf("after the overwriting fill: %d phases, want 3", got)
	}
	last := r.sched.phases[1]
	if !last.hasDest || last.x0 != 4 || last.y0 != 4 || last.x1 != 48 || last.y1 != 48 {
		t.Fatalf("phase 1 snapshot rectangle = %+v, want the union of its three tinted sprites", last)
	}
}

// A lit point batch tags the cells of its points, not of its bounding rectangle,
// and only a repeated pixel opens the next phase (§11.2 "The scheduler").
func TestSchedulerLitPointsSplitOnRepeatedPixel(t *testing.T) {
	r, _ := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	// Four points inside one 32-pixel cell, all distinct: one phase.
	r.Points(drawlist.Points{Kind: drawlist.PointLit, Points: []drawlist.Point{
		{X: 1, Y: 1}, {X: 2, Y: 1}, {X: 3, Y: 1}, {X: 4, Y: 1},
	}})
	if got := len(r.sched.phases); got != 1 {
		t.Fatalf("after four distinct points in one cell: %d phases, want 1", got)
	}
	// A repeat of one of them must observe the earlier write.
	r.Points(drawlist.Points{Kind: drawlist.PointLit, Points: []drawlist.Point{{X: 2, Y: 1}}})
	if got := len(r.sched.phases); got != 2 {
		t.Fatalf("after the repeated pixel: %d phases, want 2", got)
	}
	// An opaque write anywhere in a cell a point tagged still opens a phase: the
	// point set is not enumerable by a rectangle test.
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 20, Y: 20, W: 2, H: 2}, Index: 4})
	if got := len(r.sched.phases); got != 3 {
		t.Fatalf("after the opaque write in a point cell: %d phases, want 3", got)
	}
	// A cell no point touched joins the current phase.
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 40, Y: 40, W: 2, H: 2}, Index: 4})
	if got := len(r.sched.phases); got != 3 {
		t.Fatalf("after the opaque write outside every point cell: %d phases, want 3", got)
	}
}

// One batch keeps its record order across an image change and across the uint16
// index domain: the runs are consecutive and never reordered (C-G3).
func TestSchedulerSplitsRunsWithoutReordering(t *testing.T) {
	r, frame := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	other := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{9}, Transparent: []bool{false}}
	// Fill the first page between the two frames so the second lands on a page of
	// its own and the batch must split at that command, in order.
	r.sceneFrameFor(frame)
	r.scene.allocate(sceneAtlasPageSize, sceneAtlasPageSize-1)
	if e := r.sceneFrameFor(other); e.page == r.sceneFrameFor(frame).page {
		t.Fatalf("both frames packed onto page %d; the split cannot be exercised", e.page)
	}

	r.Sprite(drawlist.Sprite{Frame: frame, X: 1, Y: 1, Kind: drawlist.BlitKeyed})
	r.Sprite(drawlist.Sprite{Frame: other, X: 2, Y: 1, Kind: drawlist.BlitKeyed})
	r.Sprite(drawlist.Sprite{Frame: frame, X: 3, Y: 1, Kind: drawlist.BlitKeyed})
	runs := r.sched.runs[schedOpaque]
	if len(runs) != 3 {
		t.Fatalf("compiled %d opaque runs, want one per source page change", len(runs))
	}
	for i := 1; i < len(runs); i++ {
		if runs[i].vOff != runs[i-1].vOff+runs[i-1].vLen {
			t.Fatalf("run %d starts at %d, want the end of run %d", i, runs[i].vOff, i-1)
		}
	}
	if got := r.sched.verts[schedOpaque][0].DstX; got != 1 {
		t.Fatalf("first compiled vertex at x=%v, want the first recorded sprite", got)
	}
	if got := r.sched.verts[schedOpaque][8].DstX; got != 3 {
		t.Fatalf("third run's first vertex at x=%v, want the last recorded sprite", got)
	}
}

// The compile step allocates nothing once the frame's atlases exist (§11.2
// "Allocation policy"). It is the executor-side half of the benchmark's
// allocation gate, measurable without a device: every 2D family, an FNT text
// run, a lit point batch that has to split a phase on a repeated pixel, and the
// model body and shadow commits.
func TestSchedulerCompileIsAllocationFree(t *testing.T) {
	r, frame := schedulerFixture(t)
	fnt := &formats.FNT{Height: 4}
	for _, c := range []byte("nanolathe ") {
		fnt.Glyphs[c] = &formats.FNTGlyph{Width: 3, Height: 4, Bits: []byte{0b10100000, 0b01000000}}
	}
	pts := make([]drawlist.Point, 32)
	for i := range pts {
		pts[i] = drawlist.Point{X: int32(i), Y: int32(i), Index: uint8(i)}
	}
	// One repeated pixel, so the batch has to open a phase mid-run.
	repeat := append(append([]drawlist.Point{}, pts...), pts[7])
	// The model slot pages exist before the frame compiles; the commits below
	// only read their images, exactly as they do after prepareModelSlots.
	page := ebiten.NewImage(32, 32)
	shadowPage := ebiten.NewImage(32, 32)
	body := modelSlot{page: &modelPage{img: page}, rect: image.Rect(0, 0, 8, 8), box: image.Rect(0, 0, 8, 8)}
	shadow := modelSlot{page: &modelPage{img: shadowPage}, rect: image.Rect(0, 0, 8, 8), box: image.Rect(0, 0, 8, 8)}
	bodyGeom := &drawlist.ModelGeometry{Width: 8, Height: 8, AnchorX: 24, AnchorY: 24}
	shadowGeom := &drawlist.ModelGeometry{Width: 8, Height: 8, AnchorX: 26, AnchorY: 26}
	compile := func() {
		r.sched.resetFrame(64, 64)
		r.Fill(drawlist.Fill{Rect: drawlist.Rect{W: 64, H: 64}, Index: 3})
		r.Sprite(drawlist.Sprite{Frame: frame, X: 1, Y: 1, Kind: drawlist.BlitKeyed})
		r.Sprite(drawlist.Sprite{Frame: frame, X: 40, Y: 1, Kind: drawlist.BlitTinted})
		r.Sprite(drawlist.Sprite{Frame: frame, X: 40, Y: 1, Kind: drawlist.BlitTinted})
		r.Line(drawlist.Line{X0: 0, Y0: 0, X1: 30, Y1: 20, Index: 5})
		r.Points(drawlist.Points{Points: pts})
		r.Points(drawlist.Points{Kind: drawlist.PointLit, Points: repeat})
		r.Glyphs(drawlist.Glyphs{Font: fnt, Text: "nanolathe", X: 2, Y: 30, Color: 12})
		r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 40, W: 20, H: 8}, Style: drawlist.FillShadeRect, Level: -4})
		owner := r.commitModelShadow(shadowGeom, shadow, bodyGeom, body)
		r.commitModelSlot(page, body.box, modelWorldBounds(bodyGeom), owner)
	}
	compile()
	if got := len(r.sched.phases); got < 3 {
		t.Fatalf("compiled %d phases, want the overlapping commands to have opened several", got)
	}
	if r.modelStats.Shadows == 0 {
		t.Fatal("the model shadow commit compiled nothing, so the case is not covered")
	}
	if got := testing.AllocsPerRun(8, compile); got != 0 {
		t.Fatalf("compile allocated %v objects per frame, want none", got)
	}
}
