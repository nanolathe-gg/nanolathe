package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// The scroll pass has a closed input list — the cursor position and the four
// arrows — with no minimap or GUI hit test anywhere in it [07 R-CAM-01 §10].
// The radar canvas is anchored at the screen's top-left corner, so a minimap
// test inside the edge gate costs exactly the places this table names: the
// top-left corner itself, the first 126 pixels of the top edge and the first
// 126 pixels of the left edge. That is what this build used to do, which left
// the corner a dead zone for edge panning.
//
// The anchor is asserted first so the table cannot pass vacuously against a
// battle whose radar rectangle failed to resolve.
func TestEdgeScrollRunsOverTheMinimapCanvas(t *testing.T) {
	const (
		screenW = 640
		screenH = 480
		// 34 ms is one scaled unit of raw delta, so a direction that scrolls
		// moves by the setting byte [07 §10] (C2).
		frameMS = 34
	)
	setting := int32((&battleSession{}).scrollSetting())
	if setting <= 0 || setting > 128 {
		t.Skipf("scroll setting %d outside the range this contract can assert", setting)
	}
	unit := setting

	for _, tc := range []struct {
		name           string
		mouseX, mouseY float32
		wantX, wantZ   int32
	}{
		{name: "top left corner scrolls left and up", mouseX: 0, mouseY: 0, wantX: -unit, wantZ: -unit},
		{name: "top edge above the minimap scrolls up", mouseX: 60, mouseY: 0, wantZ: -unit},
		{name: "left edge beside the minimap scrolls left", mouseX: 0, mouseY: 60, wantX: -unit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBattle(testCatalogON05(), testWorldON05(300, 300))
			millis := &fakeMillisSource{}
			b.millisSource = millis
			cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: screenW, Height: screenH})
			if err != nil {
				t.Fatal(err)
			}
			cl.SetCamera(b.cam)
			cl.SetFocused(true)
			b.cam.Zoom = camera.ZoomUnit
			// The battle composer's fixed 126-pixel radar canvas at the screen
			// origin [07 §6][07 §10] (C4).
			b.hud = &retailBattleHUD{
				minimapAnchor:   hud.Rect{X1: 0, Y1: 0, X2: camera.MinimapLongSide - 1, Y2: camera.MinimapLongSide - 1},
				minimapAnchorOK: true,
			}
			if !b.isOverMinimap(int32(tc.mouseX), int32(tc.mouseY)) {
				t.Fatalf("pointer %g,%g is not over the radar canvas: the case proves nothing", tc.mouseX, tc.mouseY)
			}
			cl.Input().Mouse.SetPosition(tc.mouseX, tc.mouseY)
			// One priming frame seeds the scroll clock's anchor.
			b.viewerStep(0, cl)
			b.cam.X, b.cam.Z = 1000, 1000

			millis.ms += frameMS
			b.viewerStep(float64(frameMS)/1000, cl)

			gotX, gotZ := b.cam.X-1000, b.cam.Z-1000
			if gotX != tc.wantX || gotZ != tc.wantZ {
				t.Fatalf("one frame moved (%d,%d) map pixels, want (%d,%d)", gotX, gotZ, tc.wantX, tc.wantZ)
			}
		})
	}
}
