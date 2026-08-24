package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestSelectionPickWorld(t *testing.T) {
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 1000, MapH: 1000}
	w := units.New(10, nil)
	def := &content.UnitDef{UnitName: "u"}
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
	// Projected screen positions: 128+px, 32
	// Rect covering first two (128,32) to (138,32) inclusive.
	rect := NormalizeRect(128, 32, 138, 32)                        // [07 §9] inclusive
	changed, count := ApplyDragSelectionWorld(w, cam, rect, false) // additive clear
	if !changed {
		t.Fatal("expected changed on first select")
	}
	if count != 2 {
		t.Fatalf("selected %d want 2", count)
	}
	// Verify flags.
	iter := w.Iter()
	if iter[0].Flags&SelectionFlag == 0 || iter[1].Flags&SelectionFlag == 0 {
		t.Fatal("first two should be selected")
	}
	if iter[2].Flags&SelectionFlag != 0 {
		t.Fatal("third should not be selected")
	}
	// Additive toggle on same rect should deselect first two and preserve third (still not selected).
	changed, count = ApplyDragSelectionWorld(w, cam, rect, true)
	if !changed {
		t.Fatal("toggle should change")
	}
	if count != 0 {
		t.Fatalf("after toggle count %d want 0", count)
	}
	if iter[0].Flags&SelectionFlag != 0 || iter[1].Flags&SelectionFlag != 0 {
		t.Fatal("toggle should have cleared first two")
	}
	// Rect covering only third with additive false should select only third and clear others (already cleared).
	rect2 := NormalizeRect(148, 32, 148, 32)
	changed, count = ApplyDragSelectionWorld(w, cam, rect2, false)
	if count != 1 {
		t.Fatalf("rect2 count %d want 1", count)
	}
	if iter[2].Flags&SelectionFlag == 0 {
		t.Fatal("third should be selected")
	}
	// Verify IsUnitInRect helper matches.
	if !IsUnitInRect(cam, iter[2], rect2) {
		t.Fatal("IsUnitInRect failed for third")
	}
	if IsUnitInRect(cam, iter[0], rect2) {
		t.Fatal("first should not be in rect2")
	}
}
