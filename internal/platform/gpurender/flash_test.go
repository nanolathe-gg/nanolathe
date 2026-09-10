package gpurender

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// litDiscFixture is a 5x5 generated frame with an opaque cross and transparent
// corners, which is enough shape to tell a sampled column from its neighbour.
func litDiscFixture() drawlist.Flash {
	const side = 5
	rows := make([]uint8, side*side)
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			switch {
			case x == 2 || y == 2:
				rows[y*side+x] = uint8(x + y) // rows 0..6, all inside the LHT
			default:
				rows[y*side+x] = drawlist.FlashTransparentRow
			}
		}
	}
	return drawlist.Flash{
		Side: side, Offset: side / 2, Rows: rows,
		X: 20, Y: 20,
		Clip: drawlist.Rect{W: 64, H: 64},
	}
}

// A packed frame carries the row family's high lane as the same 24-bit triple
// the lit point plane stores, so the two paths brighten a row by the identical
// factor and cannot drift (docs/DESIGN_GPU_RENDERER.md §13.11, §13.8). A
// transparent texel and the reserved border are zero, which is the identity
// scale.
func TestFlashAtlasStoresThePointPlaneLane(t *testing.T) {
	skipAfterDeviceLoop(t)
	var a flashDiscAtlas
	f := litDiscFixture()
	region, ok := a.region(f)
	if !ok {
		t.Fatal("the disc atlas refused a 5x5 frame")
	}
	at := func(x, y int) [4]byte {
		off := (y*flashAtlasWidth + x) * 4
		return [4]byte{a.buf[off], a.buf[off+1], a.buf[off+2], a.buf[off+3]}
	}
	for y := 0; y < int(f.Side); y++ {
		for x := 0; x < int(f.Side); x++ {
			got := at(int(region.x)+x, int(region.y)+y)
			row := f.Rows[y*int(f.Side)+x]
			if row == drawlist.FlashTransparentRow {
				if got != [4]byte{} {
					t.Fatalf("transparent texel (%d,%d) stored %v, want the identity lane", x, y, got)
				}
				continue
			}
			lane := pointLaneBytes[row]
			if want := ([4]byte{lane[0], lane[1], lane[2], 255}); got != want {
				t.Fatalf("texel (%d,%d) row %d stored %v, want the point plane's lane %v", x, y, row, got, want)
			}
		}
	}
	// The reserved border is the identity, so a magnified quad's far-edge
	// fragment cannot pick up the next frame's ramp (flashAtlasPad).
	for _, p := range [][2]int{
		{int(region.x) - 1, int(region.y)},
		{int(region.x) + int(f.Side), int(region.y)},
		{int(region.x), int(region.y) - 1},
		{int(region.x), int(region.y) + int(f.Side)},
	} {
		if got := at(p[0], p[1]); got != [4]byte{} {
			t.Fatalf("border texel %v stored %v, want the identity lane", p, got)
		}
	}
	// The same identity packs once and answers from the map afterwards.
	again, ok := a.region(f)
	if !ok || again != region {
		t.Fatalf("the second lookup of the same frame gave %+v, want the packed %+v", again, region)
	}
}

// The flash draws as ONE quad over its projected extent, sampling the whole
// packed frame: at the native and detail scales the destination span is exactly
// one and two screen pixels per source texel, so the sampler's nearest column is
// the column the recorder's own per-source-pixel loop covered
// (docs/DESIGN_GPU_RENDERER.md §13.11).
func TestFlashCompilesOneQuadOverTheProjectedExtent(t *testing.T) {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		r, _ := schedulerFixture(t)
		r.sched.resetFrame(64, 64)
		f := litDiscFixture()
		f.Scale = scale
		r.Flash(f)
		if r.modelStats.Flashes != 1 {
			t.Fatalf("scale %v: %d flashes counted, want 1", scale, r.modelStats.Flashes)
		}
		b := &r.sched.phases[0].batch[schedDest]
		if len(b.verts) != quadVertices {
			t.Fatalf("scale %v: compiled %d vertices, want one quad", scale, len(b.verts))
		}
		wantX0 := float32(f.X + scale.Project(-f.Offset))
		wantX1 := float32(f.X + scale.Project(f.Side-f.Offset))
		if b.verts[0].DstX != wantX0 || b.verts[3].DstX != wantX1 {
			t.Fatalf("scale %v: quad spans x %v..%v, want %v..%v",
				scale, b.verts[0].DstX, b.verts[3].DstX, wantX0, wantX1)
		}
		region, _ := r.sched.flash.region(f)
		if b.verts[0].SrcX != float32(region.x) || b.verts[3].SrcX != float32(region.x)+float32(f.Side) {
			t.Fatalf("scale %v: quad samples x %v..%v, want the whole packed frame at %d",
				scale, b.verts[0].SrcX, b.verts[3].SrcX, region.x)
		}
		if b.verts[0].Custom3 != destOpLaneAtlas {
			t.Fatalf("scale %v: the flash quad carries op %v, want the lane atlas op", scale, b.verts[0].Custom3)
		}
	}
}

// The halo draws as one quad over the square the byte writer walked, carrying
// the fragment's offset from the centre and the squared radius, and its colour
// lanes are the row's own scale — the same lanes a UI light rect of that row
// carries [03 §4.3.1].
func TestHaloCompilesOneQuadCarryingTheDiscTest(t *testing.T) {
	r, _ := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	h := drawlist.Halo{X: 30, Y: 30, Radius: 6, Row: 11, Clip: drawlist.Rect{W: 64, H: 64}}
	r.Halo(h)
	if r.modelStats.Flashes != 1 {
		t.Fatalf("%d lit-disc quads counted, want 1", r.modelStats.Flashes)
	}
	b := &r.sched.phases[0].batch[schedDest]
	if len(b.verts) != quadVertices {
		t.Fatalf("compiled %d vertices, want one quad", len(b.verts))
	}
	if b.verts[0].DstX != 24 || b.verts[3].DstX != 37 || b.verts[0].DstY != 24 || b.verts[3].DstY != 37 {
		t.Fatalf("quad spans (%v,%v)..(%v,%v), want the inclusive 2r+1 square",
			b.verts[0].DstX, b.verts[0].DstY, b.verts[3].DstX, b.verts[3].DstY)
	}
	if b.verts[0].Custom0 != -6 || b.verts[3].Custom0 != 7 || b.verts[0].Custom2 != 36 {
		t.Fatalf("corner lanes are (%v,%v) with r2 %v, want -6..7 and 36",
			b.verts[0].Custom0, b.verts[3].Custom0, b.verts[0].Custom2)
	}
	low, high := rowScaleLanes(lightScale(int(h.Row)))
	if b.verts[0].ColorR != low || b.verts[0].ColorG != high {
		t.Fatalf("row %d rides lanes (%v,%v), want the light rect's (%v,%v)",
			h.Row, b.verts[0].ColorR, b.verts[0].ColorG, low, high)
	}
}

// Both lit discs are row-family commands, so an overlapping lit rect shares
// their phase and an overlapping tinted sprite does not
// (docs/DESIGN_GPU_RENDERER.md §13.11 "The same-stream rule").
func TestLitDiscsAreRowStreamCommands(t *testing.T) {
	r, frame := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	f := litDiscFixture()
	f.Scale = camera.ViewScaleNative
	r.Flash(f)
	r.Halo(drawlist.Halo{X: 20, Y: 20, Radius: 4, Row: 3, Clip: drawlist.Rect{W: 64, H: 64}})
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 18, Y: 18, W: 6, H: 6}, Style: drawlist.FillLitRect, Level: 5})
	if got := r.sched.nphase; got != 1 {
		t.Fatalf("three overlapping row commands compiled %d phases, want 1", got)
	}
	r.Sprite(drawlist.Sprite{Frame: frame, X: 20, Y: 20, Kind: drawlist.BlitTinted})
	if got := r.sched.curPhase; got != 1 {
		t.Fatalf("the overlapping tinted sprite landed in phase %d, want 1", got)
	}
}

// A disc whose projected extent falls wholly outside the recorded gate compiles
// nothing: the gate is the terrain rectangle, and the halo must never brighten
// unit, effect or HUD pixels [03 §4.3.1].
func TestLitDiscsClipToTheRecordedGate(t *testing.T) {
	r, _ := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	f := litDiscFixture()
	f.Scale = camera.ViewScaleNative
	f.Clip = drawlist.Rect{X: 40, Y: 40, W: 20, H: 20}
	r.Flash(f)
	r.Halo(drawlist.Halo{X: 20, Y: 20, Radius: 4, Row: 3, Clip: f.Clip})
	if r.modelStats.Flashes != 0 || r.sched.nphase != 0 {
		t.Fatalf("a disc outside its gate compiled %d quads in %d phases, want none",
			r.modelStats.Flashes, r.sched.nphase)
	}
}
