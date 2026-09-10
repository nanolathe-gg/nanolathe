package gpurender

import (
	"image"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
)

// schedulerFixture is a renderer whose Sink methods compile but never submit, so
// these tests need no device: nothing here calls a barrier (Clear, a model
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

// classVerts collects one class's compiled vertices across the segment's phases,
// in phase order and, inside a phase, in record order. It is the compiled
// geometry a test wants to look at now that each phase owns its own storage.
func (s *scheduler) classVerts(class int) []ebiten.Vertex {
	var out []ebiten.Vertex
	for i := 0; i < s.nphase; i++ {
		out = append(out, s.phases[i].batch[class].verts...)
	}
	return out
}

// Critical-path placement (docs/DESIGN_GPU_RENDERER.md §11.5): a command lands in
// the EARLIEST phase its overlaps permit — one phase after every earlier
// destination read it overlaps, and no earlier than every earlier opaque write it
// overlaps. A command that overlaps nothing goes back to phase zero however many
// phases the segment has already opened, which is what the older latest-open-phase
// rule could not do. Sharing a 32-pixel cell without overlapping constrains
// nothing, because the cell names its owners and the rectangles are compared.
func TestSchedulerPlacesCommandsOnTheCriticalPath(t *testing.T) {
	r, _ := schedulerFixture(t)
	frame := &formats.GAFFrame{Width: 8, Height: 8, Pixels: make([]byte, 64), Transparent: make([]bool, 64)}
	r.sched.resetFrame(64, 64)

	r.Fill(drawlist.Fill{Rect: drawlist.Rect{W: 64, H: 64}, Index: 3})
	if got := r.sched.curPhase; got != 0 {
		t.Fatalf("the opening opaque fill landed in phase %d, want 0", got)
	}
	// First destination-reading command: nothing overlapping is recorded yet.
	r.Sprite(drawlist.Sprite{Frame: frame, X: 0, Y: 0, Kind: drawlist.BlitTinted})
	if got := r.sched.curPhase; got != 0 {
		t.Fatalf("the first tinted sprite landed in phase %d, want 0", got)
	}
	// Overlapping the first: it reads that result, so it follows it by a phase.
	r.Sprite(drawlist.Sprite{Frame: frame, X: 4, Y: 4, Kind: drawlist.BlitTinted})
	if got := r.sched.curPhase; got != 1 {
		t.Fatalf("the overlapping tinted sprite landed in phase %d, want 1", got)
	}
	// Inside the same 32-pixel cell as the first two but disjoint from both: the
	// exact rectangles decide, and nothing constrains it, so it goes back to
	// phase zero rather than joining the latest phase.
	r.Sprite(drawlist.Sprite{Frame: frame, X: 16, Y: 16, Kind: drawlist.BlitTinted})
	if got := r.sched.curPhase; got != 0 {
		t.Fatalf("the same-cell disjoint tinted sprite landed in phase %d, want 0", got)
	}
	// A different cell likewise.
	r.Sprite(drawlist.Sprite{Frame: frame, X: 40, Y: 40, Kind: drawlist.BlitTinted})
	if got := r.sched.curPhase; got != 0 {
		t.Fatalf("the disjoint tinted sprite landed in phase %d, want 0", got)
	}
	// An opaque write over pixels both the phase-0 and the phase-1 tinted sprite
	// covered must overwrite the later of them, so it follows phase 1.
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 5, Y: 5, W: 1, H: 1}, Index: 9})
	if got := r.sched.curPhase; got != 2 {
		t.Fatalf("the overwriting fill landed in phase %d, want 2", got)
	}
	if got := r.sched.nphase; got != 3 {
		t.Fatalf("compiled %d phases, want 3", got)
	}
	// Each phase's destination rectangle is the union of its own destination
	// batch, which is all the following pass has to copy forward.
	if p := r.sched.phases[0]; !p.hasDest || p.x0 != 0 || p.y0 != 0 || p.x1 != 48 || p.y1 != 48 {
		t.Fatalf("phase 0 destination rectangle = %+v, want the union of its three tinted sprites", p)
	}
	if p := r.sched.phases[1]; !p.hasDest || p.x0 != 4 || p.y0 != 4 || p.x1 != 12 || p.y1 != 12 {
		t.Fatalf("phase 1 destination rectangle = %+v, want its one tinted sprite", p)
	}
	if !r.sched.phases[2].batch[schedDest].empty() {
		t.Fatal("phase 2 compiled a destination batch, want only the opaque overwrite")
	}
}

// A lit point batch tags the cells of its points, not of its bounding rectangle,
// and only a repeated pixel pushes a point past the point that wrote it
// (§11.2 "The scheduler").
func TestSchedulerLitPointsSplitOnRepeatedPixel(t *testing.T) {
	r, _ := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	// Four points inside one 32-pixel cell, all distinct: one phase.
	r.Points(drawlist.Points{Kind: drawlist.PointLit, Points: []drawlist.Point{
		{X: 1, Y: 1}, {X: 2, Y: 1}, {X: 3, Y: 1}, {X: 4, Y: 1},
	}})
	if got := r.sched.nphase; got != 1 {
		t.Fatalf("after four distinct points in one cell: %d phases, want 1", got)
	}
	if got := len(r.sched.phases[0].batch[schedDest].verts); got != quadVertices {
		t.Fatalf("four adjacent same-row points emitted %d vertices, want one span quad", got)
	}
	// A repeat of one of them must observe the earlier write.
	r.Points(drawlist.Points{Kind: drawlist.PointLit, Points: []drawlist.Point{{X: 2, Y: 1}}})
	if got := r.sched.curPhase; got != 1 {
		t.Fatalf("the repeated pixel landed in phase %d, want 1", got)
	}
	if got := len(r.sched.phases[1].batch[schedDest].verts); got != quadVertices {
		t.Fatalf("repeated point phase emitted %d vertices, want one point quad", got)
	}
	// An opaque write anywhere in a cell a point tagged still follows it: the
	// point set is not enumerable by a rectangle test.
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 20, Y: 20, W: 2, H: 2}, Index: 4})
	if got := r.sched.curPhase; got != 2 {
		t.Fatalf("the opaque write in a point cell landed in phase %d, want 2", got)
	}
	// A cell no point touched is unconstrained and goes back to phase zero.
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 40, Y: 40, W: 2, H: 2}, Index: 4})
	if got := r.sched.curPhase; got != 0 {
		t.Fatalf("the opaque write outside every point cell landed in phase %d, want 0", got)
	}
	if got := r.sched.nphase; got != 3 {
		t.Fatalf("compiled %d phases, want 3", got)
	}
}

// One phase's batch keeps its record order across an image change: the runs are
// consecutive and never reordered (C-G3).
func TestSchedulerSplitsRunsWithoutReordering(t *testing.T) {
	r, frame := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	// The second frame is wider than a shared atlas page, so the packer gives it
	// a page of its own and the batch must split at that command, in order. Its
	// destination quad is clipped to the fixture surface like any other, so only
	// the source page differs. Naming a size the shelf packer cannot share is
	// what keeps this fixture independent of the packer's own arithmetic — the
	// per-entry border of sceneAtlasPad included.
	wide := sceneAtlasPageSize + 1
	other := &formats.GAFFrame{Width: uint16(wide), Height: 1,
		Pixels: make([]byte, wide), Transparent: make([]bool, wide)}
	r.sceneFrameFor(frame)
	if e := r.sceneFrameFor(other); e.page == r.sceneFrameFor(frame).page {
		t.Fatalf("both frames packed onto page %d; the split cannot be exercised", e.page)
	}

	r.Sprite(drawlist.Sprite{Frame: frame, X: 1, Y: 1, Kind: drawlist.BlitKeyed})
	r.Sprite(drawlist.Sprite{Frame: other, X: 2, Y: 1, Kind: drawlist.BlitKeyed})
	r.Sprite(drawlist.Sprite{Frame: frame, X: 3, Y: 1, Kind: drawlist.BlitKeyed})
	if got := r.sched.nphase; got != 1 {
		t.Fatalf("three unconstrained opaque sprites compiled %d phases, want 1", got)
	}
	runs := r.sched.phases[0].batch[schedOpaque].runs
	if len(runs) != 3 {
		t.Fatalf("compiled %d opaque runs, want one per source page change", len(runs))
	}
	for i := 1; i < len(runs); i++ {
		if runs[i].vOff != runs[i-1].vOff+runs[i-1].vLen {
			t.Fatalf("run %d starts at %d, want the end of run %d", i, runs[i].vOff, i-1)
		}
	}
	verts := r.sched.classVerts(schedOpaque)
	if got := verts[0].DstX; got != 1 {
		t.Fatalf("first compiled vertex at x=%v, want the first recorded sprite", got)
	}
	if got := verts[8].DstX; got != 3 {
		t.Fatalf("third run's first vertex at x=%v, want the last recorded sprite", got)
	}
}

// The compile step allocates nothing once the frame's atlases exist (§11.2
// "Allocation policy"). It is the executor-side half of the benchmark's
// allocation gate, measurable without a device: every 2D family, an FNT text
// run, a lit point batch that has to open a phase on a repeated pixel, and the
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
	// The commits sample the resolved plane (post), so the stub pages carry
	// one beside the raster plane (§17).
	body := modelSlot{page: &modelPage{img: page, post: page}, rect: image.Rect(0, 0, 8, 8), box: image.Rect(0, 0, 8, 8)}
	shadow := modelSlot{page: &modelPage{img: shadowPage, post: shadowPage}, rect: image.Rect(0, 0, 8, 8), box: image.Rect(0, 0, 8, 8)}
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
		r.commitModelShadow(shadowGeom, shadow, bodyGeom, body)
		r.commitModelSlot(page, body.box, modelWorldBounds(bodyGeom))
	}
	compile()
	if got := r.sched.nphase; got < 3 {
		t.Fatalf("compiled %d phases, want the overlapping commands to have opened several", got)
	}
	if r.modelStats.Shadows == 0 {
		t.Fatal("the model shadow commit compiled nothing, so the case is not covered")
	}
	if got := testing.AllocsPerRun(8, compile); got != 0 {
		t.Fatalf("compile allocated %v objects per frame, want none", got)
	}
}

// A subject's body commit follows its own shadow commit by a phase, like every
// other opaque write over a destination read. The scheduler used to exempt the
// pair from that rule on the argument that the shadow writes only pixels the
// body's plane leaves uncovered; measured against the battle capture the two
// sets are not exactly complementary at the body's edge, and drawing the body
// first drops the shadow there, so the exemption is gone
// [03 R-REN-03D §4–§5].
func TestSchedulerBodyCommitFollowsItsShadow(t *testing.T) {
	r, _ := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	page := ebiten.NewImage(32, 32)
	shadowPage := ebiten.NewImage(32, 32)
	// The commits sample the resolved plane (post), so the stub pages carry
	// one beside the raster plane (§17).
	body := modelSlot{page: &modelPage{img: page, post: page}, rect: image.Rect(0, 0, 8, 8), box: image.Rect(0, 0, 8, 8)}
	shadow := modelSlot{page: &modelPage{img: shadowPage, post: shadowPage}, rect: image.Rect(0, 0, 8, 8), box: image.Rect(0, 0, 8, 8)}
	bodyGeom := &drawlist.ModelGeometry{Width: 8, Height: 8, AnchorX: 24, AnchorY: 24}
	shadowGeom := &drawlist.ModelGeometry{Width: 8, Height: 8, AnchorX: 26, AnchorY: 26}

	r.commitModelShadow(shadowGeom, shadow, bodyGeom, body)
	if r.modelStats.Shadows == 0 {
		t.Fatal("the shadow commit compiled nothing, so the pair is not exercised")
	}
	shadowPhase := r.sched.curPhase
	if shadowPhase != 0 {
		t.Fatalf("an unconstrained shadow commit landed in phase %d, want 0", shadowPhase)
	}
	r.commitModelSlot(page, body.box, modelWorldBounds(bodyGeom))
	if got := r.sched.curPhase; got != shadowPhase+1 {
		t.Fatalf("the body commit landed in phase %d, want the phase after its shadow's %d", got, shadowPhase)
	}
}

// A cell names only schedCellOwners commands exactly. The one it forgets is its
// oldest, and that owner's contribution becomes a floor for everything later over
// the cell, so a command that overwrites a forgotten destination read is still
// placed after it — the forgetting may cost a phase, never correctness.
func TestSchedulerForgottenCellOwnerStillConstrains(t *testing.T) {
	r, _ := schedulerFixture(t)
	frame := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{7}, Transparent: []bool{false}}
	r.sched.resetFrame(64, 64)
	// schedCellOwners+1 pairwise disjoint destination reads inside one 32-pixel
	// cell: each is unconstrained, so all land in phase 0, and the last one makes
	// the cell forget the first.
	for i := 0; i <= schedCellOwners; i++ {
		r.Sprite(drawlist.Sprite{Frame: frame, X: int32(2 * i), Kind: drawlist.BlitTinted})
		if got := r.sched.curPhase; got != 0 {
			t.Fatalf("disjoint tinted sprite %d landed in phase %d, want 0", i, got)
		}
	}
	// An opaque write over the forgotten command's own pixel has to overwrite its
	// result, so it must not share phase 0 with it.
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{W: 1, H: 1}, Index: 9})
	if got := r.sched.curPhase; got != 1 {
		t.Fatalf("the write over the forgotten destination read landed in phase %d, want 1", got)
	}
}
