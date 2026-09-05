package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

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
	// Projected screen positions: shell px,0 (beam 128+px,32 rebased)
	// Rect covering first two (0,0) to (10,0) inclusive shell.
	rect := NormalizeRect(0, 0, 10, 0) // [07 §9] inclusive shell
	apply := func(r Rect, additive bool) (bool, int) {
		iter := w.Iter()
		views := make([]*hud.SelectUnit, 0, len(iter))
		live := make([]*units.Unit, 0, len(iter))
		for _, u := range iter {
			if u == nil || !u.Alive {
				continue
			}
			views = append(views, &hud.SelectUnit{Flags: u.Flags})
			live = append(live, u)
		}
		changed, count := hud.ApplyDragSelection(views, hud.DragRect(r), additive, nil,
			func(v *hud.SelectUnit) (int32, int32) {
				for i, candidate := range views {
					if candidate == v {
						p := NewViewportTransform(cam, nil, 0, 0).WorldToSurface(live[i].X, live[i].Y, live[i].Z)
						return p.X, p.Y
					}
				}
				return 0, 0
			}, func(v *hud.SelectUnit) bool { return v != nil })
		for i, u := range live {
			u.Flags = views[i].Flags
		}
		return changed, count
	}
	changed, count := apply(rect, false) // additive clear
	if !changed {
		t.Fatal("expected changed on first select")
	}
	if count != 2 {
		t.Fatalf("selected %d want 2", count)
	}
	// Verify flags.
	iter := w.Iter()
	if iter[0].Flags&hud.SelectionFlag == 0 || iter[1].Flags&hud.SelectionFlag == 0 {
		t.Fatal("first two should be selected")
	}
	if iter[2].Flags&hud.SelectionFlag != 0 {
		t.Fatal("third should not be selected")
	}
	// Additive toggle on same rect should deselect first two and preserve third (still not selected).
	changed, count = apply(rect, true)
	if !changed {
		t.Fatal("toggle should change")
	}
	if count != 0 {
		t.Fatalf("after toggle count %d want 0", count)
	}
	if iter[0].Flags&hud.SelectionFlag != 0 || iter[1].Flags&hud.SelectionFlag != 0 {
		t.Fatal("toggle should have cleared first two")
	}
	// Rect covering only third with additive false should select only third and clear others (already cleared).
	rect2 := NormalizeRect(20, 0, 20, 0)
	_, count = apply(rect2, false)
	if count != 1 {
		t.Fatalf("rect2 count %d want 1", count)
	}
	if iter[2].Flags&hud.SelectionFlag == 0 {
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
