package drawlist

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// recorder is a Sink stub that appends a tag per call plus the last-seen values
// of the two families the field-fidelity test checks.
type recorder struct {
	tags       []string
	lastSprite Sprite
	lastFill   Fill
}

func (r *recorder) Clear()          { r.tags = append(r.tags, "clear") }
func (r *recorder) Terrain(Terrain) { r.tags = append(r.tags, "terrain") }
func (r *recorder) Sprite(c Sprite) { r.tags = append(r.tags, "sprite"); r.lastSprite = c }
func (r *recorder) Glyphs(Glyphs)   { r.tags = append(r.tags, "glyphs") }
func (r *recorder) Fill(c Fill)     { r.tags = append(r.tags, "fill"); r.lastFill = c }
func (r *recorder) Line(Line)       { r.tags = append(r.tags, "line") }
func (r *recorder) Points(Points)   { r.tags = append(r.tags, "points") }
func (r *recorder) Flash(Flash)     { r.tags = append(r.tags, "flash") }
func (r *recorder) Halo(Halo)       { r.tags = append(r.tags, "halo") }
func (r *recorder) Model(Model)     { r.tags = append(r.tags, "model") }
func (r *recorder) Fog(Fog)         { r.tags = append(r.tags, "fog") }
func (r *recorder) Surface(Surface) { r.tags = append(r.tags, "surface") }
func (r *recorder) Cursor(Cursor)   { r.tags = append(r.tags, "cursor") }
func (r *recorder) Expand()         { r.tags = append(r.tags, "expand") }

// TestReplayPreservesRecordOrder locks C-G3: Replay visits commands in exact
// record order across families.
func TestReplayPreservesRecordOrder(t *testing.T) {
	var l List
	// A mixed sequence that interleaves families and repeats some of them, so a
	// per-family walk would produce a different tag order than the record order.
	l.RecordTerrain(Terrain{})
	l.RecordSprite(Sprite{})
	l.RecordFill(Fill{})
	l.RecordSprite(Sprite{})
	l.RecordGlyphs(Glyphs{})
	l.RecordFog(Fog{})
	l.RecordFill(Fill{})
	l.RecordExpand()
	l.RecordCursor(Cursor{})

	want := []string{
		"terrain", "sprite", "fill", "sprite", "glyphs", "fog", "fill", "expand", "cursor",
	}

	var r recorder
	l.Replay(&r)

	if len(r.tags) != len(want) {
		t.Fatalf("replayed %d commands, want %d: %v", len(r.tags), len(want), r.tags)
	}
	for i := range want {
		if r.tags[i] != want[i] {
			t.Fatalf("command %d = %q, want %q (full: %v)", i, r.tags[i], want[i], r.tags)
		}
	}
}

// TestResetKeepsCapacity locks the reuse contract: Reset truncates without
// freeing capacity, so a re-recorded frame that fits reallocates nothing.
func TestResetKeepsCapacity(t *testing.T) {
	var l List
	for i := 0; i < 8; i++ {
		l.RecordSprite(Sprite{X: int32(i)})
		l.RecordFill(Fill{Index: uint8(i)})
	}
	orderCap := cap(l.order)
	spriteCap := cap(l.sprite)
	fillCap := cap(l.fill)

	l.Reset()
	if len(l.order) != 0 || len(l.sprite) != 0 || len(l.fill) != 0 {
		t.Fatalf("Reset left non-zero lengths: order=%d sprite=%d fill=%d",
			len(l.order), len(l.sprite), len(l.fill))
	}

	// Re-record an equal-or-smaller frame; capacities must not shrink and must
	// not grow.
	for i := 0; i < 8; i++ {
		l.RecordSprite(Sprite{X: int32(i)})
		l.RecordFill(Fill{Index: uint8(i)})
	}
	if got := cap(l.order); got != orderCap {
		t.Fatalf("order cap changed across Reset: got %d, want %d", got, orderCap)
	}
	if got := cap(l.sprite); got != spriteCap {
		t.Fatalf("sprite cap changed across Reset: got %d, want %d", got, spriteCap)
	}
	if got := cap(l.fill); got != fillCap {
		t.Fatalf("fill cap changed across Reset: got %d, want %d", got, fillCap)
	}
}

// TestCloneDeepCopiesReusedArrays locks the WU-1.8 snapshot contract: Clone
// must not alias any array the client reuses each frame — the point batches a
// Points record sub-slices out of the client's point arena, the fog op lists,
// and the surface pixel buffers. A ComposeFrameSnapshot hands the clone to a
// caller who replays it after the client has moved on to the next frame and
// overwritten those arrays, so a shallow copy would silently corrupt it.
func TestCloneDeepCopiesReusedArrays(t *testing.T) {
	// arena stands in for the client's reusable point arena; the Points record
	// carries a three-index sub-slice of it, exactly as emitPoints records.
	arena := []Point{{X: 1}, {X: 2}, {X: 3}}
	var l List
	l.RecordClear()
	l.RecordPoints(Points{Kind: PointLit, Points: arena[0:2:2]})
	l.RecordFog(Fog{Ops: []render.FogOp{{ScreenX0: 5}}})
	l.RecordSurface(Surface{Pixels: []byte{9, 8, 7}, SrcW: 3, SrcH: 1})

	clone := l.Clone()

	// Overwrite every reused source the way the next frame's recording would.
	arena[0].X = 99
	l.fog[0].Ops[0].ScreenX0 = 99
	l.surface[0].Pixels[0] = 42

	if clone.points[0].Points[0].X != 1 {
		t.Fatalf("clone point batch aliases the source arena: X=%d, want 1", clone.points[0].Points[0].X)
	}
	if clone.fog[0].Ops[0].ScreenX0 != 5 {
		t.Fatalf("clone fog ops alias the source: ScreenX0=%d, want 5", clone.fog[0].Ops[0].ScreenX0)
	}
	if clone.surface[0].Pixels[0] != 9 {
		t.Fatalf("clone surface pixels alias the source: [0]=%d, want 9", clone.surface[0].Pixels[0])
	}
	if len(clone.order) != len(l.order) {
		t.Fatalf("clone order length = %d, want %d", len(clone.order), len(l.order))
	}
	// The clone replays the same command sequence.
	var r recorder
	clone.Replay(&r)
	want := []string{"clear", "points", "fog", "surface"}
	if len(r.tags) != len(want) {
		t.Fatalf("clone replayed %v, want %v", r.tags, want)
	}
	for i := range want {
		if r.tags[i] != want[i] {
			t.Fatalf("clone command %d = %q, want %q", i, r.tags[i], want[i])
		}
	}
}

// TestFieldFidelity locks that a recorded Sprite and Fill round-trip their
// exact field values through Replay to the Sink.
func TestFieldFidelity(t *testing.T) {
	frame := &formats.GAFFrame{Width: 4, Height: 4}
	sprite := Sprite{
		Frame:    frame,
		X:        12,
		Y:        -7,
		Clip:     Rect{X: 1, Y: 2, W: 3, H: 4},
		HasClip:  true,
		Kind:     BlitLit,
		LightRow: 9,
		Key:      9,
	}
	fill := Fill{
		Rect:  Rect{X: 5, Y: 6, W: 7, H: 8},
		Index: 0xA3,
		Style: FillShadeRect,
		Row:   15,
	}

	var l List
	l.RecordSprite(sprite)
	l.RecordFill(fill)

	var r recorder
	l.Replay(&r)

	if r.lastSprite != sprite {
		t.Fatalf("sprite round-trip mismatch:\n got  %+v\n want %+v", r.lastSprite, sprite)
	}
	if r.lastSprite.Frame != frame {
		t.Fatalf("sprite frame pointer not preserved")
	}
	if r.lastFill != fill {
		t.Fatalf("fill round-trip mismatch:\n got  %+v\n want %+v", r.lastFill, fill)
	}
}

// The lit-disc families take their place in record order like every other
// family, and Replay hands them to the Sink where they were recorded
// (docs/DESIGN_GPU_RENDERER.md §13.11).
func TestReplayVisitsTheLitDiscFamilies(t *testing.T) {
	var l List
	l.RecordFill(Fill{})
	l.RecordFlash(Flash{})
	l.RecordPoints(Points{})
	l.RecordHalo(Halo{})
	l.RecordFlash(Flash{})
	var r recorder
	l.Replay(&r)
	want := []string{"fill", "flash", "points", "halo", "flash"}
	if len(r.tags) != len(want) {
		t.Fatalf("replayed %v, want %v", r.tags, want)
	}
	for i := range want {
		if r.tags[i] != want[i] {
			t.Fatalf("replayed %v, want %v", r.tags, want)
		}
	}
	// Reset drops them with every other family, and a cloned list keeps them.
	c := l.Clone()
	l.Reset()
	var after recorder
	l.Replay(&after)
	if len(after.tags) != 0 {
		t.Fatalf("after Reset the list replayed %v, want nothing", after.tags)
	}
	var cloned recorder
	c.Replay(&cloned)
	if len(cloned.tags) != len(want) {
		t.Fatalf("the clone replayed %v, want %v", cloned.tags, want)
	}
}

// A Flash expands to the pixels the byte writer covers: every opaque texel of
// the frame, magnified by the view scale, gated by the recorded rectangle
// [03 R-FX-01 §4].
func TestFlashExpandCoversTheMagnifiedFrame(t *testing.T) {
	f := Flash{
		Side: 3, Offset: 1,
		Rows: []uint8{FlashTransparentRow, 4, FlashTransparentRow,
			5, 6, 7,
			FlashTransparentRow, 8, FlashTransparentRow},
		X: 10, Y: 10, Scale: camera.ViewScaleDetail,
		Clip: Rect{W: 64, H: 64},
	}
	var got []Point
	f.Expand(func(x, y int32, row uint8) { got = append(got, Point{X: x, Y: y, Index: row}) })
	// Five opaque texels, each two by two at the detail scale.
	if len(got) != 5*4 {
		t.Fatalf("the disc expanded to %d pixels, want 20", len(got))
	}
	// The centre texel is row 6 and sits at the anchor, two pixels on a side.
	for _, p := range []Point{{X: 10, Y: 10, Index: 6}, {X: 11, Y: 11, Index: 6}} {
		found := false
		for _, q := range got {
			if q == p {
				found = true
			}
		}
		if !found {
			t.Fatalf("the centre texel did not cover %+v; got %v", p, got)
		}
	}
	// The gate removes what falls outside it and nothing else.
	f.Clip = Rect{X: 11, Y: 11, W: 40, H: 40}
	var clipped []Point
	f.Expand(func(x, y int32, row uint8) { clipped = append(clipped, Point{X: x, Y: y, Index: row}) })
	if len(clipped) >= len(got) || len(clipped) == 0 {
		t.Fatalf("the gate left %d of %d pixels", len(clipped), len(got))
	}
	for _, p := range clipped {
		if p.X < 11 || p.Y < 11 {
			t.Fatalf("the gate admitted %+v", p)
		}
	}
}

// A Halo expands to the byte writer's own disc: the inclusive square of
// 2r+1 pixels, minus the corners the dx*dx + dy*dy <= r*r test rejects
// [03 §4.3.1].
func TestHaloExpandMatchesTheRadiusTest(t *testing.T) {
	h := Halo{X: 20, Y: 20, Radius: 3, Row: 9, Clip: Rect{W: 64, H: 64}}
	seen := map[[2]int32]bool{}
	h.Expand(func(x, y int32, row uint8) {
		if row != 9 {
			t.Fatalf("pixel (%d,%d) carries row %d, want the halo's 9", x, y, row)
		}
		seen[[2]int32{x, y}] = true
	})
	for dy := int32(-4); dy <= 4; dy++ {
		for dx := int32(-4); dx <= 4; dx++ {
			want := dx*dx+dy*dy <= 9
			if got := seen[[2]int32{20 + dx, 20 + dy}]; got != want {
				t.Fatalf("offset (%d,%d): covered %v, want %v", dx, dy, got, want)
			}
		}
	}
}
