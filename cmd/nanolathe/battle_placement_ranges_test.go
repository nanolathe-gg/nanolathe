package main

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestPlacementRangeUsesShowRangesPrimitives(t *testing.T) {
	cl, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cam := &camera.Camera{Zoom: camera.ZoomUnit, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	// NOWEAPON must stay absent; the third active slot does not depend on the
	// first slot for a prospective product. Sensor guides are not placement ink.
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"turret": {
		RadarDistance: 600,
		Weapon1Def:    &content.WeaponDef{ID: 0, Range: 16},
		Weapon3Def:    &content.WeaponDef{ID: 2, Range: 300, Coverage: 2000, Interceptor: true},
	}}}
	cl.SetCamera(cam)
	cl.SetEnhanced(true)
	cl.SetFocused(true)
	b := &battleSession{cat: cat, cam: cam, battleUI: ui.NewProductionBattleState()}
	state := b.battleState()
	state.ArmPlacement("turret", 2, 3)
	state.Input.PointerX, state.Input.PointerY = 300, 200
	state.Input.BuildCellX, state.Input.BuildCellZ, state.Input.BuildSiteH = 20, 14, 50
	state.Input.BuildOK = false // Repositioning an invalid site keeps the guide.
	opt := hud.QueueOverlayOptions{Tick: 20,
		Project: func(x, y, z numeric.Fixed) hud.QueuePoint {
			return hud.QueuePoint{X: int32(x >> 16), Y: int32(z>>16) - int32(y>>17)}
		},
		GroundHeight: func(numeric.Fixed, numeric.Fixed) numeric.Fixed { return 60 << 16 },
	}
	x, z := world.PlacementCenter(20, 14, 2, 3)
	want := hud.WeaponRangeOverlay(hud.QueueWorldPoint{X: x, Y: 50 << 16, Z: z}, [3]hud.RangeWeapon{{}, {}, {Enabled: true, Range: 300}}, opt)
	for _, zoom := range []camera.Zoom{camera.ZoomUnit / 2, camera.ZoomUnit, camera.ZoomUnit * 3 / 2, camera.ZoomUnit * 2} {
		cam.Zoom = zoom
		if got := b.placementRangeOverlay(cl, opt); len(got) == 0 || !reflect.DeepEqual(got, want) {
			t.Fatalf("zoom %v: placement differs from shared weapon ring", zoom)
		}
	}
	noGuide := func() {
		t.Helper()
		if got := b.placementRangeOverlay(cl, opt); len(got) != 0 {
			t.Fatal("inactive placement emitted ranges")
		}
	}
	disabled := false
	b.rangePreferences.PlacementWeaponRanges = &disabled
	noGuide()
	b.dispatchLocalCommand("+showranges")
	noGuide()
	state.Input.ShiftHeld = true
	if len(b.placementRangeOverlay(cl, opt)) == 0 {
		t.Fatal("explicit +showranges with Shift ignored mod default")
	}
	b.dispatchLocalCommand("+showranges")
	noGuide()
	b.rangePreferences.PlacementWeaponRanges = nil
	cl.SetFocused(false)
	noGuide()
	cl.SetFocused(true)
	cl.SetEnhanced(false)
	noGuide()
	cl.SetEnhanced(true)
	state.Input.PointerX = 10
	noGuide()
	state.Input.PointerX = 300
	state.Input.BuildDef = ""
	noGuide()
	state.Input.BuildDef = "turret"
	b.modernDrag = &battleCommandDrag{}
	noGuide()
	b.modernDrag = nil
	state.Input.BuildDef = ""
	noGuide()
}

// The command remains process-only and survives entering the next battle.
func TestShowRangesShellLifetime(t *testing.T) {
	shell := &gameShell{}
	first := &battleSession{shell: shell}
	first.dispatchLocalCommand("+showranges")
	next := &battleSession{shell: shell}
	if !next.rangesShown() {
		t.Fatal("battle transition lost +showranges")
	}
	next.dispatchLocalCommand("+showranges")
	if first.rangesShown() {
		t.Fatal("second command did not disable ranges")
	}
}
