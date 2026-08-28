package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// synthetic catalog for ON-05 tests (data-driven, no hardcoded retail names beyond synthetic)
func testCatalogON05() *content.Catalog {
	b1 := &content.UnitDef{UnitName: "armcons", Builder: true, CanMove: true, FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100}
	b1.CanonicalKey = content.CanonicalKey(b1.UnitName)
	b1.DefinitionHeader.CanonicalKey = b1.CanonicalKey
	p1 := &content.UnitDef{UnitName: "armsolar", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100}
	p1.CanonicalKey = content.CanonicalKey(p1.UnitName)
	p1.DefinitionHeader.CanonicalKey = p1.CanonicalKey
	// armfav is a vehicle: authored BMcode 1 and no yard map, like every stock
	// mobile unit. That is what makes it a factory product rather than a
	// placement one [07 §9].
	p2 := &content.UnitDef{UnitName: "armfav", BMCode: true, FootprintX: 2, FootprintZ: 2, MaxDamage: 100}
	p2.CanonicalKey = content.CanonicalKey(p2.UnitName)
	p2.DefinitionHeader.CanonicalKey = p2.CanonicalKey
	fac := &content.UnitDef{UnitName: "armfac", Builder: true, CanMove: false, FootprintX: 3, FootprintZ: 3, YardMap: "ooooooooo", MaxDamage: 500}
	fac.CanonicalKey = content.CanonicalKey(fac.UnitName)
	fac.DefinitionHeader.CanonicalKey = fac.CanonicalKey
	reclaimUnit := &content.UnitDef{UnitName: "armrecl", CanReclamate: true, Builder: false, FootprintX: 1, FootprintZ: 1, MaxDamage: 100}
	reclaimUnit.CanonicalKey = content.CanonicalKey(reclaimUnit.UnitName)
	reclaimUnit.DefinitionHeader.CanonicalKey = reclaimUnit.CanonicalKey
	featDef := &content.FeatureDef{}
	featDef.CanonicalKey = content.CanonicalKey("armrock")
	featDef.Reclaimable = true
	featDef.FootprintX = 1
	featDef.FootprintZ = 1

	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			b1.CanonicalKey:          b1,
			p1.CanonicalKey:          p1,
			p2.CanonicalKey:          p2,
			fac.CanonicalKey:         fac,
			reclaimUnit.CanonicalKey: reclaimUnit,
		},
		Features: map[string]*content.FeatureDef{
			featDef.CanonicalKey: featDef,
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			b1.CanonicalKey:  {Buttons: []string{"armsolar", "armfav", "armsolar", "armfav", "armsolar", "armfav", "armsolar", "armfav", "armsolar"}},
			fac.CanonicalKey: {Buttons: []string{"armfav", "armsolar"}},
		},
	}
	return cat
}

func testWorldON05(w, h int32) *world.Terrain {
	t := &world.Terrain{CellW: w, CellH: h}
	t.Plot = make([]world.PlotCell, int(w*h))
	for i := range t.Plot {
		t.Plot[i][8] = 0xFF
		t.Plot[i][9] = 0xFF
	}
	// Ensure PlayRight/Bottom for camera clamp
	t.PlayRight = w * 16
	t.PlayBottom = h * 16
	return t
}

func newTestBattle(cat *content.Catalog, terrain *world.Terrain) *battleSession {
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: terrain.CellW * 16, MapH: terrain.CellH * 16}
	uw := units.New(64, cat)
	sess := &session.Session{
		World:    terrain,
		Units:    uw,
		Catalog:  cat,
		Snapshot: &frame.Buffer{},
		Clock:    &clock.State{Active: 10, Requested: 10},
	}
	// Ensure orders table initialized (orders.Lookup needs table)
	_ = orders.Lookup("Move_Ground")
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	b.cam = cam
	return b
}

func placeUnit(b *battleSession, name string, x, z numeric.Fixed) *units.Unit {
	def, ok := b.cat.Unit(name)
	if !ok || def == nil {
		panic("def not found " + name)
	}
	h, err := b.sess.Units.Create(def, 0, x, numeric.Fixed(0), z)
	if err != nil {
		panic(err)
	}
	u := b.sess.Units.Unit(h)
	if u != nil {
		u.Health = 100
		u.MaxHealth = 100
	}
	return u
}

func TestReclaimClickResolvesFeature(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(10, 10)
	// Place feature at cell 9,5 so it lies inside the visible framebuffer
	// surface rather than under the side rail.
	featDef, _ := cat.Features[content.CanonicalKey("armrock")]
	terrain.FeatureNames = []string{"armrock"}
	terrain.FeatureDefs = []*content.FeatureDef{featDef}
	// Plot cell anchor at 9,5
	idx := 5*int(terrain.CellW) + 9
	terrain.Plot[idx][8] = 0
	terrain.Plot[idx][9] = 0 // index 0
	// Debug check
	if f := terrain.Plot[idx].Feature(); f != 0 {
		t.Fatalf("plot feature not set, got %d", f)
	}
	if ok := func() bool {
		_, ok := world.ResolveFeature(terrain.Plot, int(terrain.CellW), int(terrain.CellH), 9, 5)
		return ok
	}(); !ok {
		t.Fatalf("ResolveFeature direct failed at 9,5")
	}
	// Ensure anchor not fringe
	b := newTestBattle(cat, terrain)
	if ok := func() bool {
		_, ok := world.ResolveFeature(b.sess.World.Plot, int(b.sess.World.CellW), int(b.sess.World.CellH), 9, 5)
		return ok
	}(); !ok {
		t.Fatalf("b.sess.World ResolveFeature failed after b creation")
	}
	// Place reclaim unit elsewhere (2,2) so feature at 9,5 is not masked by unit [07 §8] unit>feature priority
	recl := placeUnit(b, "armrecl", numeric.Fixed(int64(2*16)<<16), numeric.Fixed(int64(2*16)<<16))
	// Place cam at 0
	b.cam.X = 0
	b.cam.Z = 0
	// Pick at the framebuffer position that maps to feature world 9*16 = 144.
	beamX, beamY := b.cam.WorldToScreen(numeric.Fixed(int64(9*16)<<16), 0, numeric.Fixed(int64(5*16)<<16))
	sx, sy := beamX-camera.OriginX, beamY-camera.OriginY
	wxDbg, _, wzDbg := b.cursorWorld(sx, sy)
	cx := world.WorldToCell(wxDbg)
	cz := world.WorldToCell(wzDbg)
	_, _, pos := b.pickTarget(sx, sy)
	if !pos.HasFeature {
		t.Fatalf("reclaim pick should resolve feature, got HasFeature false at %d,%d world %v cell %d,%d", sx, sy, pos, cx, cz)
	}
	// Also test that order resolves to Reclaim when HasFeature
	_ = recl
	code := 12 // RECLAIM
	id := orders.Resolve(code, recl, nil, pos)
	if id == 0 {
		t.Fatalf("reclaim resolve should not be 0 when feature present")
	}
	name := orders.DescriptorFor(id).Name
	if name != "Reclaim" && name != "VTOL_Reclaim" {
		t.Fatalf("reclaim feature should resolve to Reclaim, got %s", name)
	}
}

// Test 8: middle drag changes camera only [F-P1-008]
func TestMiddleDragChangesCameraOnly(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(100, 100)
	b := newTestBattle(cat, terrain)
	b.cam.X = 500
	b.cam.Z = 500
	u := placeUnit(b, "armsolar", numeric.Fixed(int64(100)<<16), numeric.Fixed(int64(100)<<16))
	origX, origZ := b.cam.X, b.cam.Z
	origUX, origUZ := u.X, u.Z
	// Directly test camera Drag as presentation-only (viewerStep uses same)
	b.cam.Drag(20, 10)
	if b.cam.X == origX && b.cam.Z == origZ {
		t.Fatalf("camera should move on drag orig %d,%d new %d,%d", origX, origZ, b.cam.X, b.cam.Z)
	}
	if u.X != origUX || u.Z != origUZ {
		t.Fatalf("unit position should not change on camera drag (presentation only)")
	}
}

// Test 9: wheel changes presentation projection only [F-P1-008]
func TestWheelChangesPresentationOnly(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	u := placeUnit(b, "armsolar", numeric.Fixed(int64(200)<<16), numeric.Fixed(int64(200)<<16))
	sx1, sy1 := b.cam.WorldToScreen(u.X, numeric.Fixed(0), u.Z)
	origScale := b.cam.Scale
	if origScale == 0 {
		origScale = 1
	}
	b.cam.AddZoom(1, 320, 240)
	newScale := b.cam.Scale
	if newScale == origScale {
		t.Fatalf("wheel should change scale")
	}
	sx2, sy2 := b.cam.WorldToScreen(u.X, numeric.Fixed(0), u.Z)
	if sx1 == sx2 && sy1 == sy2 {
		t.Fatalf("projection should change after zoom")
	}
	if u.X != numeric.Fixed(int64(200)<<16) {
		t.Fatalf("unit world pos should not change on zoom")
	}
}

// Test 10: W/A/S/D unbound [F-P1-008]
func TestWASDUnbound(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(100, 100)
	b := newTestBattle(cat, terrain)
	b.cam.X = 500
	b.cam.Z = 500
	// Place minimal client for viewerStep
	buf := &frame.Buffer{}
	cl, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Headless: true, Step: func(delta float64) {}})
	cl.SetCamera(b.cam)
	cl.Input().Mouse.SetPosition(320, 240) // center to avoid edge scroll
	origX, origZ := b.cam.X, b.cam.Z
	// Inject W held
	cl.Input().Kbd.SetKey(input.KeyW, true)
	cl.Input().Kbd.SetKey(input.KeyA, true)
	cl.Input().Kbd.SetKey(input.KeyS, true)
	cl.Input().Kbd.SetKey(input.KeyD, true)
	// Call viewerStep with delta 16ms
	b.viewerStep(0.016, cl)
	if b.cam.X != origX || b.cam.Z != origZ {
		t.Fatalf("WASD should not move camera, got %d,%d want %d,%d", b.cam.X, b.cam.Z, origX, origZ)
	}
	// Arrow keys should still move
	cl2, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Headless: true, Step: func(delta float64) {}})
	cl2.SetCamera(b.cam)
	b.cam.X = 500
	b.cam.Z = 500
	cl2.Input().Mouse.SetPosition(320, 240)
	cl2.Input().Kbd.SetKey(input.KeyUp, true)
	b.viewerStep(0.016, cl2)
	if b.cam.Z == 500 {
		t.Fatalf("arrow up should move camera")
	}
	// Reset
	b.cam.X = 500
	b.cam.Z = 500
	// Edge scroll still works: place mouse near edge
	cl3, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Headless: true, Step: func(delta float64) {}})
	cl3.SetCamera(b.cam)
	cl3.Input().Mouse.SetPosition(0, 240) // left edge exact [07 §10] x==0
	b.viewerStep(0.016, cl3)
	if b.cam.X == 500 {
		t.Fatalf("edge scroll should move left")
	}
}

// Additional: order button latch via hud
func TestOrderButtonLatch(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	// Simulate HUD order button handling via handleHudOrderButton
	b.handleHudOrderButton("attack")
	if b.battleState().Input.Latch != input.LatchAttack {
		t.Fatalf("attack button should arm Attack latch, got %v", b.battleState().Input.Latch)
	}
	b.handleHudOrderButton("repair")
	if b.battleState().Input.Latch != input.LatchRepair {
		t.Fatalf("repair latch failed")
	}
	b.handleHudOrderButton("reclaim")
	if b.battleState().Input.Latch != input.LatchReclaim {
		t.Fatalf("reclaim latch")
	}
	b.handleHudOrderButton("stop")
	// stop is LatchNormal immediate
	if b.battleState().Input.Latch != input.LatchNormal {
		t.Fatalf("stop should set Normal")
	}
}

// Ensure buildDef retained after mobile product click, and factory queues directly
