package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Shared authored fixture catalog for battle-input tests. The definitions are
// deliberately local test data, not a second content source.
func testCatalogON05() *content.Catalog {
	// armcons is a construction vehicle: authored BMcode 1, like every stock
	// mobile unit. The order resolver's live-mover test reads the
	// building-class status bit creation derives from that byte, so a mobile
	// fixture must author it or it resolves as an immobile builder
	// [04 R-ORD-02 §1][04 R-COLL-01 §2].
	b1 := &content.UnitDef{UnitName: "armcons", ObjectName: "armcons", Builder: true, BMCode: 1, CanMove: true, FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100}
	b1.CanonicalKey = content.CanonicalKey(b1.UnitName)
	b1.DefinitionHeader.CanonicalKey = b1.CanonicalKey
	p1 := &content.UnitDef{UnitName: "armsolar", ObjectName: "armsolar", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100}
	p1.CanonicalKey = content.CanonicalKey(p1.UnitName)
	p1.DefinitionHeader.CanonicalKey = p1.CanonicalKey
	// armfav is a vehicle: authored BMcode 1 and no yard map, like every stock
	// mobile unit. That is what makes it a factory product rather than a
	// placement one [07 §9].
	p2 := &content.UnitDef{UnitName: "armfav", ObjectName: "armfav", BMCode: 1, FootprintX: 2, FootprintZ: 2, MaxDamage: 100}
	p2.CanonicalKey = content.CanonicalKey(p2.UnitName)
	p2.DefinitionHeader.CanonicalKey = p2.CanonicalKey
	fac := &content.UnitDef{UnitName: "armfac", ObjectName: "armfac", Builder: true, CanMove: false, FootprintX: 3, FootprintZ: 3, YardMap: "ooooooooo", MaxDamage: 500}
	fac.CanonicalKey = content.CanonicalKey(fac.UnitName)
	fac.DefinitionHeader.CanonicalKey = fac.CanonicalKey
	reclaimUnit := &content.UnitDef{UnitName: "armrecl", ObjectName: "armrecl", CanReclamate: true, Builder: false, FootprintX: 1, FootprintZ: 1, MaxDamage: 100}
	reclaimUnit.CanonicalKey = content.CanonicalKey(reclaimUnit.UnitName)
	reclaimUnit.DefinitionHeader.CanonicalKey = reclaimUnit.CanonicalKey
	featDef := &content.FeatureDef{}
	featDef.CanonicalKey = content.CanonicalKey("armrock")
	featDef.Reclaimable = true
	featDef.FootprintX = 1
	featDef.FootprintZ = 1

	authorTestUnitScripts(b1, p1, p2, fac, reclaimUnit)

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
	uw := units.NewSliced(64, cat)
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
	// Retail has one message ring; battleSession.messageRing() reaches it
	// through the installed presentation client [07 R-HUD-03 §14.3]. A
	// fixture client, sized like the negotiated default surface, gives the
	// hotkey tests (F3/F12, the speed announcement) the same ring the real
	// battle shell shares with the composer.
	if cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: 640, Height: 480}); err == nil {
		b.cl = cl
	}
	// Picking is a hull test over the candidate's root-piece bounds
	// [07 R-REV-01], so a fixture unit needs a model the presentation can
	// resolve. These tests hold no VFS, so they author one: a symmetric
	// sixteen-unit-square root piece centred on the unit, which is enough for
	// the projected origin to lie strictly inside the hull. Hull arithmetic
	// itself is locked in internal/client, not here.
	installTestHullModels()
	return b
}

// installTestHullModels registers the fixture hull as the presentation model
// source for tests that hold no VFS.
func installTestHullModels() {
	client.SetUnitHullModels(client.UnitHullModelFunc(testHullModel))
}

// testHullModel authors the fixture hull described in newTestBattle.
func testHullModel(name string) *compiledmodel.Model {
	if name == "" {
		return nil
	}
	const half = numeric.Fixed(8 << 16)
	m := &compiledmodel.Model{
		Root: 0,
		Name: name,
		Pieces: []compiledmodel.Piece{{
			Name:   "base",
			Parent: -1,
			Vertices: [][3]numeric.Fixed{
				{-half, 0, -half},
				{half, 0, half},
				{0, 0, 0},
			},
		}},
	}
	return m
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

func TestFeatureClickResolvesReclaimOrder(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(10, 10)
	// Place feature at cell 9,5 so it lies inside the visible framebuffer
	// surface rather than under the side rail.
	featDef := cat.Features[content.CanonicalKey("armrock")]
	terrain.FeatureNames = []string{"armrock"}
	terrain.FeatureDefs = []*content.FeatureDef{featDef}
	b := newTestBattle(cat, terrain)
	b.sess.Features = features.NewService(terrain, nil, nil, nil)
	if b.sess.Features.PlaceAt(9, 5, featDef) == nil {
		t.Fatalf("feature placement failed at 9,5")
	}
	b.sess.Vis = visibility.New(terrain, 0)
	b.sess.Vis.SetLocal(0)
	if ok := func() bool {
		_, ok := world.ResolveFeature(b.sess.World.Plot, int(b.sess.World.CellW), int(b.sess.World.CellH), 9, 5)
		return ok
	}(); !ok {
		t.Fatalf("b.sess.World ResolveFeature failed after b creation")
	}
	// Place reclaim unit elsewhere (2,2) so feature at 9,5 is not masked by unit [07 §8] unit>feature priority
	recl := placeUnit(b, "armrecl", numeric.Fixed(int64(2*16)<<16), numeric.Fixed(int64(2*16)<<16))
	// Picking reads the committed feature/visibility publication, not live
	// terrain or feature state [I6].
	applyPendingBattleCommands(b)
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

// Middle-button dragging changes only the presentation camera [07 §10].
func TestMiddleDragLeavesWorldUnchanged(t *testing.T) {
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

// The detail view scale changes only the presentation projection [07 §10]
// [F-P1-008] (DESIGN_GPU_RENDERER §14.1). The wheel is not a camera control.
func TestDetailScaleLeavesWorldUnchanged(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	u := placeUnit(b, "armsolar", numeric.Fixed(int64(200)<<16), numeric.Fixed(int64(200)<<16))
	sx1, sy1 := b.cam.WorldToScreen(u.X, numeric.Fixed(0), u.Z)
	b.cam.SetScaleAbout(320, 240, 2)
	if b.cam.Scale != 2 {
		t.Fatalf("scale should be 2, got %d", b.cam.Scale)
	}
	sx2, sy2 := b.cam.WorldToScreen(u.X, numeric.Fixed(0), u.Z)
	if sx1 == sx2 && sy1 == sy2 {
		t.Fatalf("projection should change at the detail scale")
	}
	if u.X != numeric.Fixed(int64(200)<<16) {
		t.Fatalf("unit world pos should not change with the view scale")
	}
}

// The battle camera ignores WASD while retaining the authored arrow-key and
// edge-scroll controls [07 §10].
func TestBattleCameraIgnoresWASD(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(100, 100)
	b := newTestBattle(cat, terrain)
	// The scroll pass consumes a scaled 30-per-second delta, so a frame has to
	// cost wall-clock time before it can scroll at all: three back-to-back
	// viewerStep calls inside the same thirtieth of a second scroll nothing
	// [07 §10]. Drive a deterministic host clock and advance it a full unit per
	// frame.
	millis := &fakeMillisSource{}
	b.millisSource = millis
	advance := func() { millis.ms += 34 }
	b.cam.X = 500
	b.cam.Z = 500
	// Place minimal client for viewerStep
	buf := &frame.Buffer{}
	cl, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Step: func(delta float64) {}})
	cl.SetCamera(b.cam)
	cl.Input().Mouse.SetPosition(320, 240) // center to avoid edge scroll
	origX, origZ := b.cam.X, b.cam.Z
	// Inject W held
	cl.Input().Kbd.SetKey(input.KeyW, true)
	cl.Input().Kbd.SetKey(input.KeyA, true)
	cl.Input().Kbd.SetKey(input.KeyS, true)
	cl.Input().Kbd.SetKey(input.KeyD, true)
	// Call viewerStep with delta 16ms
	advance()
	b.viewerStep(0.016, cl)
	advance()
	b.viewerStep(0.016, cl)
	if b.cam.X != origX || b.cam.Z != origZ {
		t.Fatalf("WASD should not move camera, got %d,%d want %d,%d", b.cam.X, b.cam.Z, origX, origZ)
	}
	// Arrow keys should still move
	cl2, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Step: func(delta float64) {}})
	cl2.SetCamera(b.cam)
	b.cam.X = 500
	b.cam.Z = 500
	cl2.Input().Mouse.SetPosition(320, 240)
	cl2.Input().Kbd.SetKey(input.KeyUp, true)
	advance()
	b.viewerStep(0.016, cl2)
	if b.cam.Z == 500 {
		t.Fatalf("arrow up should move camera")
	}
	// Reset
	b.cam.X = 500
	b.cam.Z = 500
	// Edge scroll still works: place mouse near edge
	cl3, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Step: func(delta float64) {}})
	cl3.SetCamera(b.cam)
	cl3.Input().Mouse.SetPosition(0, 240) // left edge exact [07 §10] x==0
	cl3.SetFocused(true)                  // edge scroll is suppressed without window focus [07 §10]
	advance()
	b.viewerStep(0.016, cl3)
	if b.cam.X == 500 {
		t.Fatalf("edge scroll should move left")
	}
}

// HUD order buttons arm the corresponding command latch [07 §9].
func TestHUDOrderButtonsArmLatches(t *testing.T) {
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
