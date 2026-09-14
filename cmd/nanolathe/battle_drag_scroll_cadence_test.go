package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// Per-service signed truncation discards small motion even while paused. Extra
// presentation draws do not poll or spend another delta [07 R-CAM-01 §11].
func TestDragScrollViewerDiscardsRemainderIndependentlyOfDraws(t *testing.T) {
	for _, paused := range []bool{false, true} {
		b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
		b.millisSource = &fakeMillisSource{}
		b.cam = &camera.Camera{X: 128, Z: 128, ViewW: 640, ViewH: 480, MapW: 4000, MapH: 4000}
		cl := b.cl
		cl.SetFocused(true)
		b.applyBattleSchedule(ui.PauseIntent(paused))
		step := func(kind input.PointerEventKind, x, y int32, held bool) {
			e := input.PointerEvent{Kind: kind, X: x, Y: y, Buttons: input.MouseButtons{Right: held}, Modifiers: input.Modifiers{Ctrl: true}}
			cl.Input().UpdatePointerMotion(e)
			if kind != 0 {
				cl.Input().EnqueuePointer(e)
			}
			cl.Input().PublishPointer()
			b.viewerStep(0, cl)
		}
		step(input.RightDown, 300, 200, true)
		if !b.dragScrollActive {
			t.Fatal("viewer did not capture Ctrl-right")
		}
		for i := int32(1); i <= 8; i++ {
			step(0, 300+i*3, 200-i*3, true)
			for draw := 0; draw < 4; draw++ {
				cl.ComposeFrameSnapshot()
			}
			if b.cam.X != 128 || b.cam.Z != 128 {
				t.Fatalf("paused=%v sample=%d accumulated discarded motion: %d,%d", paused, i, b.cam.X, b.cam.Z)
			}
		}
		step(input.RightUp, 328, 172, false)
		if b.cam.X != 144 || b.cam.Z != 112 || b.dragScrollActive || cl.PointerCaptured() {
			t.Fatalf("paused=%v release camera=%d,%d capture=%v", paused, b.cam.X, b.cam.Z, b.dragScrollActive)
		}
	}
}
