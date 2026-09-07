package drawlist

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/render"
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
