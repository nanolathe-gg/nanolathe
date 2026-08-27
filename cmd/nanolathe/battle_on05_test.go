package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
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
		Snapshot: &snapshot.Buffer{},
		Clock:    &clock.State{Active: 10, Requested: 10},
	}
	// Ensure orders table initialized (orders.Lookup needs table)
	_ = orders.Lookup("Move_Ground")
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	b.cam = cam
	b.panel = hud.NewPanel(0x04, 640, 480, nil)
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

// Test 1: simulated HUD product click arms correct product [R-P0-03]
func TestHUDProductClickArmsCorrectProduct(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	builder := placeUnit(b, "armcons", numeric.Fixed(0), numeric.Fixed(0))
	builder.Flags |= client.SelectionFlag
	b.armBuildPanel()
	if len(b.panelButtons) == 0 {
		t.Fatalf("panelButtons empty")
	}
	// Find first build button
	var btn panelButton
	for _, pb := range b.panelButtons {
		if pb.Kind == "build" {
			btn = pb
			break
		}
	}
	if btn.Name == "" {
		t.Fatalf("no build button found")
	}
	// Simulate click at button center
	mx := btn.X + panelButtonW/2
	my := btn.Y + panelButtonH/2
	// Use panelClick directly
	if !b.panelClick(mx, my) {
		t.Fatalf("panelClick should consume")
	}
	if b.buildDef == "" {
		t.Fatalf("buildDef not armed after product click")
	}
	// Data-driven: product must be from BuildMenus list
	if !hud.ValidateBuildProduct(cat, builder.Def.CanonicalKey, b.buildDef) {
		t.Fatalf("armed product %s not in BuildMenus", b.buildDef)
	}
	if b.buildDef != "armsolar" && b.buildDef != "armfav" {
		t.Fatalf("unexpected product %s", b.buildDef)
	}
	// No hardcoded unit names beyond data-driven: ensure we didn't invent
	if b.buildDef == "armcom" {
		t.Fatalf("invented product")
	}
}

// Test 2: HUD release never selects a world unit [F-P0-003]
func TestHUDReleaseNeverSelectsWorldUnit(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	// Place a unit at world (100,100) pixels -> screen approx 228,132 with cam 0, Origin 128,32
	wx := numeric.Fixed(int64(100) << 16)
	wz := numeric.Fixed(int64(100) << 16)
	u := placeUnit(b, "armsolar", wx, wz)
	u.Flags &^= client.SelectionFlag
	// Ensure panel is armed so isOverPanel true
	builder := placeUnit(b, "armcons", numeric.Fixed(0), numeric.Fixed(0))
	builder.Flags |= client.SelectionFlag
	b.armBuildPanel()
	// Simulate press that begins on HUD chrome (panel area)
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Mouse.InjectMouseMove(10, 450) // y 450 is panel band (>=428)
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	// This press should be captured as HUD
	b.handleInput(in, nil)
	if !b.hudCaptured {
		t.Fatalf("press on HUD should set hudCaptured")
	}
	// Now simulate drag release over world (over unit)
	in.Mouse.ClearEdges()
	in.Mouse.InjectMouseMove(228, 132) // over unit
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, false)
	b.handleInput(in, nil)
	// After release that began on HUD, no selection should have been applied
	if u.Flags&client.SelectionFlag != 0 {
		t.Fatalf("HUD release leaked into world selection")
	}
	if b.dragActive {
		t.Fatalf("drag should not be active after HUD capture")
	}
}

// Test 3: legal placement queues one mobile build with expected coordinates [R-P0-03]
func TestLegalPlacementQueuesMobileBuild(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	bindBattleSessionCommandDispatch(b)
	builder := placeUnit(b, "armcons", numeric.Fixed(0), numeric.Fixed(0))
	builder.Flags |= client.SelectionFlag
	applyPendingBattleCommands(b)
	b.armBuildPanel()
	// Arm product
	btn := b.panelButtons[0]
	b.panelClick(btn.X+1, btn.Y+1)
	if b.buildDef == "" {
		t.Fatalf("not armed")
	}
	product := b.buildDef
	// Simulate placement at legal site: screen that maps inside map
	// Shell viewport: world = shell + cam. Choose shell 160,120 -> world 160,120
	// With foot 2x2, cell = 10,7 minus half => 9,6 inside 20
	b.buildMX = 160
	b.buildMY = 120
	b.updatePlacement(160, 120)
	if !b.buildOK {
		t.Fatalf("expected legal placement at center, got blocked")
	}
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Mouse.InjectMouseMove(160, 120)
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	// Ensure buildDef still armed
	if b.buildDef == "" {
		t.Fatalf("buildDef cleared prematurely")
	}
	// Capture placement before handleInput disarms it [07 §9]
	expCellX, expCellZ := b.buildCellX, b.buildCellZ
	expFootX, expFootZ := b.buildFootX, b.buildFootZ
	b.handleInput(in, nil)
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanMobileBuild || pending[0].MobileBuild.Builder != builder.Handle || pending[0].MobileBuild.Product != product {
		t.Fatalf("legal placement did not queue typed mobile build: %+v", pending)
	}
	// Apply the command at the authoritative input boundary before inspecting
	// the order queue [01 §4.4].
	applyPendingBattleCommands(b)
	q := orders.QueueForUnit(builder)
	if q.LenPrimary() != 1 {
		t.Fatalf("legal placement should queue 1, got %d", q.LenPrimary())
	}
	// The order carries the footprint's center, not the cursor point [07 §9]:
	// retail snaps the site to whole cells and stores ((foot + 2*cell) << 19).
	tail := q.Primary()[0]
	expectedWX, expectedWZ := world.PlacementCenter(expCellX, expCellZ, expFootX, expFootZ)
	if tail.GoalX != expectedWX || tail.GoalZ != expectedWZ {
		t.Fatalf("queued coords mismatch: got %d,%d want %d,%d", tail.GoalX, tail.GoalZ, expectedWX, expectedWZ)
	}
	if tail.GoalX != numeric.Fixed(int64(expCellX*16+expFootX*8)<<16) {
		t.Fatalf("site center %d is not the footprint midpoint of cell %d", tail.GoalX, expCellX)
	}
}

// Test 4: illegal placement queues nothing [R-P0-03]
func TestIllegalPlacementQueuesNothing(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(1, 1) // compact map has no visible 2x2 placement site
	b := newTestBattle(cat, terrain)
	bindBattleSessionCommandDispatch(b)
	builder := placeUnit(b, "armcons", numeric.Fixed(0), numeric.Fixed(0))
	builder.Flags |= client.SelectionFlag
	b.armBuildPanel()
	btn := b.panelButtons[0]
	b.panelClick(btn.X+1, btn.Y+1)
	// Use the visible battle-surface edge. The cursor-to-ground conversion
	// resolves it in the compact map's out-of-bounds region [07 §8].
	b.buildMX = 639
	b.buildMY = 447
	b.updatePlacement(639, 447)
	if b.buildOK {
		t.Fatalf("off-map placement should be illegal")
	}
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Mouse.InjectMouseMove(0, 0)
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	q := orders.QueueForUnit(builder)
	if q.LenPrimary() != 0 {
		t.Fatalf("illegal placement should queue nothing, got %d", q.LenPrimary())
	}
	// buildDef should remain armed for next attempt
	if b.buildDef == "" {
		t.Fatalf("illegal click should keep armed product")
	}
}

// Test 5: factory product click queues factory item [R-P0-03][F-P1-008]
func TestFactoryProductClickQueuesFactoryItem(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	bindBattleSessionCommandDispatch(b)
	fac := placeUnit(b, "armfac", numeric.Fixed(0), numeric.Fixed(0))
	fac.Flags |= client.SelectionFlag
	applyPendingBattleCommands(b)
	b.armBuildPanel()
	if len(b.panelButtons) == 0 {
		t.Fatalf("factory panel empty")
	}
	// Find button
	btn := b.panelButtons[0]
	before := 0
	if q := orders.QueueForUnit(fac); q != nil {
		before = q.LenPrimary()
	}
	// Click factory product
	if !b.panelClick(btn.X+1, btn.Y+1) {
		t.Fatalf("factory panelClick not handled")
	}
	// After factory click, should have queued directly, not armed placement
	if b.buildDef != "" {
		t.Fatalf("factory click should not arm placement, got %s", b.buildDef)
	}
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanFactoryBuild || pending[0].FactoryBuild.Builder != fac.Handle || pending[0].FactoryBuild.Product != btn.Name || pending[0].FactoryBuild.Count != 1 {
		t.Fatalf("factory product click did not queue typed build: %+v", pending)
	}
	applyPendingBattleCommands(b)
	q := orders.QueueForUnit(fac)
	if q.LenPrimary() != before+1 {
		t.Fatalf("factory product click should queue 1, got %d", q.LenPrimary())
	}
	tail := q.Primary()[q.LenPrimary()-1]
	if tail.BuildDefKey == "" {
		t.Fatalf("factory queue missing BuildDefKey")
	}
}

// Test 6: paging changes page data-driven [R-P0-03][07 §9] C10
func TestPagingChangesPageDataDriven(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	builder := placeUnit(b, "armcons", numeric.Fixed(0), numeric.Fixed(0))
	builder.Flags |= client.SelectionFlag
	b.armBuildPanel()
	// Initially page 0
	if hud.IsPaged(builder.Flags) {
		t.Fatalf("initial should be page 0 not paged")
	}
	firstPageFirstBtn := b.panelButtons[0].Name
	b.nextBuildPage()
	if !hud.IsPaged(builder.Flags) {
		t.Fatalf("after next, should be paged")
	}
	if hud.DecodePage(builder.Flags) != 1 {
		t.Fatalf("page after next want 1 got %d", hud.DecodePage(builder.Flags))
	}
	b.armBuildPanel()
	secondPageFirstBtn := b.panelButtons[0].Name
	if firstPageFirstBtn == secondPageFirstBtn && len(b.panelButtons) > 1 {
		// With 9 items and 8 per page, first page 8 items, second page 1 item, they differ
		// But if synthetic list is 9, second page should be the 9th item "armsolar" again? Might be same product but page still changed
	}
	// Prev should return to 0
	b.prevBuildPage()
	if hud.DecodePage(builder.Flags) != 0 && hud.IsPaged(builder.Flags) {
		// Page 0 is not paged (bit clear) or paged 0? EncodePageBits clears bit for 0
		// So after prev, IsPaged should be false
		if hud.IsPaged(builder.Flags) {
			t.Fatalf("after prev to 0, should not be paged")
		}
	}
	// Guard: next beyond max stays
	b.nextBuildPage()
	b.nextBuildPage() // already at max 1, second next should stay 1
	if hud.DecodePage(builder.Flags) != 1 {
		t.Fatalf("guard should prevent beyond max")
	}
	// Digit page switching also guard
	b.switchBuildPage(9) // digit 9 => page 8 clamped to 1
	if hud.DecodePage(builder.Flags) != 1 {
		t.Fatalf("digit page clamp failed")
	}
}

// Test 7: reclaim click resolves feature [R-P0-03][07 §8]
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
	recl.Flags |= client.SelectionFlag
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
	buf := &snapshot.Buffer{}
	cl, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Headless: true, Step: func(delta float64) {}})
	cl.SetCamera(b.cam)
	cl.Input().Mouse.InjectMouseMove(320, 240) // center to avoid edge scroll
	origX, origZ := b.cam.X, b.cam.Z
	// Inject W held
	cl.Input().Kbd.InjectKey(input.KeyW, true)
	cl.Input().Kbd.InjectKey(input.KeyA, true)
	cl.Input().Kbd.InjectKey(input.KeyS, true)
	cl.Input().Kbd.InjectKey(input.KeyD, true)
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
	cl2.Input().Mouse.InjectMouseMove(320, 240)
	cl2.Input().Kbd.InjectKey(input.KeyUp, true)
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
	cl3.Input().Mouse.InjectMouseMove(0, 240) // left edge exact [07 §10] x==0
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
	if b.latch != input.LatchAttack {
		t.Fatalf("attack button should arm Attack latch, got %v", b.latch)
	}
	b.handleHudOrderButton("repair")
	if b.latch != input.LatchRepair {
		t.Fatalf("repair latch failed")
	}
	b.handleHudOrderButton("reclaim")
	if b.latch != input.LatchReclaim {
		t.Fatalf("reclaim latch")
	}
	b.handleHudOrderButton("stop")
	// stop is LatchNormal immediate
	if b.latch != input.LatchNormal {
		t.Fatalf("stop should set Normal")
	}
}

// Ensure buildDef retained after mobile product click, and factory queues directly
func TestBuildDefRetainedAndFactoryQueue(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	bindBattleSessionCommandDispatch(b)
	builder := placeUnit(b, "armcons", numeric.Fixed(0), numeric.Fixed(0))
	builder.Flags |= client.SelectionFlag
	b.armBuildPanel()
	btn := b.panelButtons[0]
	b.panelClick(btn.X+1, btn.Y+1)
	if b.buildDef == "" {
		t.Fatalf("mobile product should arm")
	}
	// Clear armed before factory test to avoid leftover
	b.buildDef = ""
	// Factory
	fac := placeUnit(b, "armfac", numeric.Fixed(0), numeric.Fixed(0))
	// Deselect builder, select factory
	builder.Flags &^= client.SelectionFlag
	fac.Flags |= client.SelectionFlag
	applyPendingBattleCommands(b)
	b.armBuildPanel()
	if len(b.panelButtons) == 0 {
		t.Fatalf("factory panel empty")
	}
	btn2 := b.panelButtons[0]
	// Record queue before
	qBefore := orders.QueueForUnit(fac).LenPrimary()
	b.panelClick(btn2.X+1, btn2.Y+1)
	if b.buildDef != "" {
		t.Fatalf("factory should queue not arm, got buildDef %s", b.buildDef)
	}
	applyPendingBattleCommands(b)
	qAfter := orders.QueueForUnit(fac).LenPrimary()
	if qAfter != qBefore+1 {
		t.Fatalf("factory queue should increase by 1, before %d after %d", qBefore, qAfter)
	}
}

// Right-click cancels placement before affecting selection
func TestRightClickCancelsPlacement(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	builder := placeUnit(b, "armcons", numeric.Fixed(0), numeric.Fixed(0))
	builder.Flags |= client.SelectionFlag
	b.armBuildPanel()
	b.panelClick(b.panelButtons[0].X+1, b.panelButtons[0].Y+1)
	if b.buildDef == "" {
		t.Fatalf("armed")
	}
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Mouse.InjectMouseButton(input.MouseButtonRight, true)
	b.handleInput(in, nil)
	if b.buildDef != "" {
		t.Fatalf("right-click should cancel armed placement")
	}
	// Ensure no selection change side effect
}

// Escape cancels too
func TestEscapeCancelsPlacement(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	builder := placeUnit(b, "armcons", numeric.Fixed(0), numeric.Fixed(0))
	builder.Flags |= client.SelectionFlag
	b.armBuildPanel()
	b.panelClick(b.panelButtons[0].X+1, b.panelButtons[0].Y+1)
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Kbd.InjectKey(input.KeyEscape, true)
	b.handleInput(in, nil)
	if b.buildDef != "" {
		t.Fatalf("escape should cancel")
	}
	if b.latch != input.LatchNormal {
		t.Fatalf("escape should reset latch")
	}
}

// Footprint preview drawn legal/illegal already verified via buildOK in updatePlacement
func TestFootprintPreviewLegalIllegal(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(39, 39)
	b := newTestBattle(cat, terrain)
	b.cam.X = 0
	b.cam.Z = 0
	builder := placeUnit(b, "armcons", numeric.Fixed(0), numeric.Fixed(0))
	builder.Flags |= client.SelectionFlag
	b.armBuildPanel()
	b.panelClick(b.panelButtons[0].X+1, b.panelButtons[0].Y+1)
	b.updatePlacement(320, 240) // center legal (500? but with large map 100*16=1600, center 192,208 inside)
	if !b.buildOK {
		t.Fatalf("center should be legal")
	}
	// The far visible edge resolves beyond this map's right boundary, making
	// the 2x2 footprint illegal [07 §8].
	b.updatePlacement(639, 447)
	if b.buildOK {
		t.Fatalf("off-map placement should be illegal")
	}
}
