package main

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The Enhanced guide chooses independently enabled slots and ignores record zero
// even when NOWEAPON authors a range [06 R-WPN-05 §3]. Interceptor coverage is a
// distinct guide; stockpiling alone does not imply interception [06 §11.2].
func TestTacticalRangeWeaponAdmission(t *testing.T) {
	d := &content.UnitDef{Weapon1Def: &content.WeaponDef{ID: 0, Range: 16}, Weapon2Def: &content.WeaponDef{ID: 1, Range: 300}, Weapon3Def: &content.WeaponDef{ID: 2, Range: 32000, Coverage: 2000, Interceptor: true}}
	check := func(enabled [3]bool, preview bool, want []tacticalRange) {
		t.Helper()
		got := tacticalRanges(d, enabled, preview, true)
		if !reflect.DeepEqual(got.values[:got.n], want) {
			t.Fatalf("ranges %+v, want %+v", got.values[:got.n], want)
		}
	}
	check([3]bool{true, false, true}, false, []tacticalRange{{tacticalIntercept, 2000, false}})
	check([3]bool{}, true, []tacticalRange{{tacticalWeapon, 300, false}, {tacticalIntercept, 2000, false}})
	d.Weapon3Def.Interceptor = false
	d.Weapon3Def.Stockpile = true
	check([3]bool{false, false, true}, false, []tacticalRange{{tacticalWeapon, 32000, false}})
	d.Weapon3Def.Range = 300
	check([3]bool{true, true, true}, false, []tacticalRange{{tacticalWeapon, 300, false}})
}

// Field widths agree with the authored range readers [07 R-P0-11 §3].
func TestTacticalRangeWidthsAndInactiveSensors(t *testing.T) {
	d := &content.UnitDef{Builder: true, BuildDistance: 65536 + 128, RadarDistance: 65536 + 1440, SonarDistance: 32768, RadarDistanceJam: 640, SonarDistanceJam: -1, OnOffable: true}
	got := tacticalRanges(d, [3]bool{}, false, false)
	want := []tacticalRange{{tacticalRadar, 1440, true}, {tacticalRadarJam, 640, true}, {tacticalBuild, 128, false}}
	if !reflect.DeepEqual(got.values[:got.n], want) {
		t.Fatalf("ranges %+v, want %+v", got.values[:got.n], want)
	}
	preview := tacticalRanges(d, [3]bool{}, true, false)
	for _, r := range preview.values[:preview.n] {
		if r.inactive {
			t.Fatal("prospective product inherited inactive state")
		}
	}
}

func TestTacticalRangeHeldKeyAndFocus(t *testing.T) {
	b := &battleSession{}
	in := input.NewState()
	in.Kbd.SetKey(input.KeyAlt, true)
	b.updateTacticalRangeInput(in, true)
	if b.tacticalRangesHeld {
		t.Fatal("Alt enabled the Shift overlay")
	}
	in.Kbd.SetKey(input.KeyAlt, false)
	in.Kbd.SetKey(input.KeyShift, true)
	b.updateTacticalRangeInput(in, true)
	if !b.tacticalRangesHeld {
		t.Fatal("held Shift was ignored")
	}
	b.updateTacticalRangeInput(in, false)
	if b.tacticalRangesHeld {
		t.Fatal("focus loss retained Shift")
	}
	in.Kbd.SetKey(input.KeyAlt, true)
	in.Kbd.SetKey(input.KeyShift, false)
	b.updateTacticalRangeInput(in, true)
	if b.tacticalRangesHeld {
		t.Fatal("release retained Shift")
	}
}

func TestTacticalPlacementGuideUsesProspectiveProductAndSnappedSite(t *testing.T) {
	cl, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cam := &camera.Camera{Zoom: camera.ZoomUnit / 2, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"turret": {Weapon1Def: &content.WeaponDef{ID: 1, Range: 300}}}}
	cl.SetCamera(cam)
	cl.SetEnhanced(true)
	cl.SetStrategicIconCatalog(client.NewStrategicIconCatalog(cat))
	b := &battleSession{cat: cat, cam: cam, battleUI: ui.NewProductionBattleState(), tacticalRangesHeld: true}
	state := b.battleState()
	state.ArmPlacement("turret", 2, 3)
	state.Input.PointerX, state.Input.PointerY = 300, 200
	state.Input.BuildCellX, state.Input.BuildCellZ, state.Input.BuildSiteH = 20, 14, 50
	// Invalid placement still needs its prospective range for repositioning.
	state.Input.BuildOK = false
	for _, zoom := range []camera.Zoom{camera.ZoomUnit / 2, camera.ZoomUnit, camera.ZoomUnit * 3 / 2, camera.ZoomUnit * 2} {
		cam.Zoom = zoom
		seen := 0
		b.visitTacticalRanges(cl, &frame.Frame{}, func(x, y, z numeric.Fixed, r tacticalRange) {
			seen++
			wx, wz := world.PlacementCenter(20, 14, 2, 3)
			if x != wx || z != wz || y != numeric.FixedFromInt(50) || r.kind != tacticalWeapon || r.radius != 300 {
				t.Fatalf("wrong placement guide: %v %v %v %+v", x, y, z, r)
			}
		})
		if seen != 1 {
			t.Fatalf("got %d preview ranges", seen)
		}
	}
	noGuide := func() {
		t.Helper()
		b.visitTacticalRanges(cl, &frame.Frame{}, func(_, _, _ numeric.Fixed, _ tacticalRange) { t.Fatal("inactive overlay emitted a guide") })
	}
	state.Input.PointerX = 10
	noGuide()
	state.Input.PointerX = 300
	b.tacticalRangesHeld = false
	noGuide()
	b.tacticalRangesHeld = true
	cl.SetEnhanced(false)
	noGuide()
}

func TestTacticalHoverDoesNotStickThroughSelectionDrag(t *testing.T) {
	cl, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cam := &camera.Camera{Zoom: camera.ZoomUnit / 2, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"turret": {Weapon1Def: &content.WeaponDef{ID: 1, Range: 300}}}}
	cl.SetCamera(cam)
	cl.SetEnhanced(true)
	cl.SetStrategicIconCatalog(client.NewStrategicIconCatalog(cat))
	b := &battleSession{cat: cat, cam: cam, battleUI: ui.NewProductionBattleState(), tacticalRangesHeld: true, footerHoverUnit: 1}
	state := b.battleState()
	state.Input.PointerX, state.Input.PointerY = 300, 200
	f := &frame.Frame{Units: []frame.UnitView{{Slot: 1, DefName: "turret", X: numeric.FixedFromInt(600), Z: numeric.FixedFromInt(400), EnabledWeaponSlots: [3]bool{true}}}}
	count := func() int {
		n := 0
		b.visitTacticalRanges(cl, f, func(_, _, _ numeric.Fixed, _ tacticalRange) { n++ })
		return n
	}
	if count() != 1 {
		t.Fatal("hovered own unit has no range")
	}
	state.Input.DragActive = true
	if count() != 0 {
		t.Fatal("retained footer identity became stale range hover")
	}
	f.Units[0].Flags |= hud.SelectionFlag
	if count() != 1 {
		t.Fatal("drag suppressed selected-unit range")
	}
}
