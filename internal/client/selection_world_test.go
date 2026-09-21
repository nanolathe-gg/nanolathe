package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestSelectionPickWorld locks the committed-frame rectangle walk that a drag
// selection runs on: the projection of [03 §2.5] measured against the inclusive
// rectangle of [07 §9], in ascending slot order [I1]. The membership writes
// themselves are the session's HumanSelectionReplace / Toggle / Clear commands,
// which own the truth table; this is the picker those commands are handed.
func TestSelectionPickWorld(t *testing.T) {
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 1000, MapH: 1000}
	w := units.NewSliced(10, nil)
	// Selection is the subject of this test, but creation still requires a
	// loadable COB [R-COB-04 §8]. RETURN is a complete authored fixture program
	// [04 §4.3].
	def := &content.UnitDef{UnitName: "u", Script: &cob.Program{
		Code: []uint32{0x10065000}, Scripts: map[string]int{}, Pieces: []string{"base"},
	}}
	def.MaxDamage = 100
	// Place three units at X=0,10,20 pixels world (Fixed 16.16). Y=0.
	positions := []int32{0, 10, 20}
	for _, px := range positions {
		x := numeric.Fixed(int64(px) << 16)
		z := numeric.Fixed(0)
		y := numeric.Fixed(0)
		h, _ := w.Create(def, 0, x, y, z)
		_ = h
	}
	live := w.Iter()
	committed := &frame.Frame{Units: []frame.UnitView{
		{Slot: 1, Owner: 0, X: live[0].X, Y: live[0].Y, Z: live[0].Z},
		{Slot: 2, Owner: 0, X: live[1].X, Y: live[1].Y, Z: live[1].Z},
		{Slot: 3, Owner: 0, X: live[2].X, Y: live[2].Y, Z: live[2].Z},
	}}

	// A rectangle covering the first two, inclusive on both endpoints.
	rect := NormalizeRect(0, 0, 10, 0) // [07 §9] inclusive shell
	got := SnapshotUnitHandlesInBand(committed, cam, bandOfRecordRect(rect), 0)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("handles in 0,0..10,0 = %v, want the first two in ascending slot order", got)
	}

	// The far endpoint is inclusive: a degenerate rectangle on the third unit
	// picks it and nothing else.
	rect2 := NormalizeRect(20, 0, 20, 0)
	if got := SnapshotUnitHandlesInBand(committed, cam, bandOfRecordRect(rect2), 0); len(got) != 1 || got[0] != 3 {
		t.Fatalf("handles in the degenerate rectangle on the third unit = %v, want [3]", got)
	}

	// A rectangle between two units admits neither.
	if got := SnapshotUnitHandlesInBand(committed, cam, bandOfRecordRect(NormalizeRect(11, 0, 19, 0)), 0); len(got) != 0 {
		t.Fatalf("handles between two units = %v, want none", got)
	}
}

// TestSelectionRectSortsEndpoints locks the rubber-band rectangle's endpoint
// sort [07 §9]: a drag that ends above and left of where it started still
// produces an inclusive rectangle with MinX<=MaxX and MinY<=MaxY. The picker
// rejects an unsorted rectangle as empty, so this is the assertion that keeps a
// backwards drag selecting anything at all.
func TestSelectionRectSortsEndpoints(t *testing.T) {
	forward := NormalizeRect(149, 132, 329, 332)
	backward := NormalizeRect(329, 332, 149, 132)
	if forward != backward {
		t.Fatalf("drag direction changed the rectangle: %+v vs %+v", forward, backward)
	}
	if backward.MinX != 149 || backward.MinY != 132 || backward.MaxX != 329 || backward.MaxY != 332 {
		t.Fatalf("rectangle = %+v, want the inclusive 149,132..329,332", backward)
	}
	if rectEmpty(backward) {
		t.Fatal("a sorted rectangle must not read as empty")
	}
}
