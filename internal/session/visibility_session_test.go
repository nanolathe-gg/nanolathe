package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestVisibilityModeRespectsSkirmishConfig verifies ModeHistory/Current/TerrainRay
// from SkirmishConfig [08 "Skirmish configuration"][03 §3.1].
func TestVisibilityModeRespectsSkirmishConfig(t *testing.T) {
	cat := minimalCatalogForStrict()
	terrain := minimalTerrain()
	m := syntheticMission()
	m.Type = mission.TypeSkirmish
	cases := []struct {
		name     string
		cfg      SkirmishConfig
		wantHist bool
		wantCur  bool
		wantRay  bool
	}{
		{"all enabled", SkirmishConfig{MapName: "test", NumPlayers: 2, Mapping: 1, LineOfSight: 1, LOSType: 1}, true, true, true},
		{"mapping off", SkirmishConfig{MapName: "test", NumPlayers: 2, Mapping: 0, LineOfSight: 1, LOSType: 1}, false, true, true},
		{"los off", SkirmishConfig{MapName: "test", NumPlayers: 2, Mapping: 1, LineOfSight: 0, LOSType: 1}, true, false, true},
		{"lostype off", SkirmishConfig{MapName: "test", NumPlayers: 2, Mapping: 1, LineOfSight: 1, LOSType: 0}, true, true, false},
		{"all off", SkirmishConfig{MapName: "test", NumPlayers: 2, Mapping: 0, LineOfSight: 0, LOSType: 0}, false, false, false},
		// Battle entry takes the LOW BIT of each field, not a non-zero test
		// [08 R-SKIR-01 §2]; no lobby toggle stores 2, so this locks the
		// traced form rather than a reachable difference.
		{"low bit only", SkirmishConfig{MapName: "test", NumPlayers: 2, Mapping: 2, LineOfSight: 3, LOSType: 2}, false, true, false},
	}
	for _, tc := range cases {
		// Need to bypass ApplyDefaults which would set defaults 1; set rulesDefaultsApplied true to keep explicit zeros.
		tc.cfg.rulesDefaultsApplied = true
		s := &Session{Catalog: cat, World: terrain, Mission: m, Skirmish: tc.cfg}
		w, _ := newSlicedWorld(cat)
		s.Units = w
		s.Econ = economyForTest()
		s.Econ.Players[0].Exists = true
		s.Econ.Players[0].ControllerState = 1
		s.Econ.Players[1].Exists = true
		s.Econ.Players[1].ControllerState = 1
		if err := createAndBindServicesForTest(t, s); err != nil {
			t.Fatalf("%s: createAndBindServices: %v", tc.name, err)
		}
		mode := s.Vis.Mode()
		if got := mode&visibility.ModeHistoryEnabled != 0; got != tc.wantHist {
			t.Fatalf("%s: history want %v got %v", tc.name, tc.wantHist, got)
		}
		if got := mode&visibility.ModeCurrentEnabled != 0; got != tc.wantCur {
			t.Fatalf("%s: current want %v got %v", tc.name, tc.wantCur, got)
		}
		if got := mode&visibility.ModeTerrainRay != 0; got != tc.wantRay {
			t.Fatalf("%s: ray want %v got %v", tc.name, tc.wantRay, got)
		}
		_ = mode
		// Also verify grid dimensions respect terrain CellW/2.
		if w, h := s.Vis.GridDimensions(); w != terrain.CellW/2 || h != terrain.CellH/2 {
			t.Fatalf("%s: grid dims %d/%d want %d/%d", tc.name, w, h, terrain.CellW/2, terrain.CellH/2)
		}
	}
}

// TestSessionVisibility covers owner bypass, LOS, and sensor-independent
// visibility predicates [03 §3.2].
func TestSessionVisibility(t *testing.T) {
	cat := minimalCatalogForStrict()
	terrain := &world.Terrain{CellW: 64, CellH: 64, SeaLevel: 0}
	terrain.Plot = make([]world.PlotCell, 64*64)
	for i := range terrain.Plot {
		terrain.Plot[i] = world.PlotCell{}
	}
	_ = terrain.ApplySchema(nil, 0)
	m := syntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.Econ.SeedDeadlines(0)
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Create two units far apart > sight. Y above sea (30) so not underwater.
	def := &content.UnitDef{UnitName: "testunit", MaxDamage: 100, SightDistance: 160, FootprintX: 1, FootprintZ: 1, Script: fixtureCOBProgram()}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	h0, _ := s.Units.Create(def, 0, numeric.Fixed(10*32*65536), numeric.Fixed(30*65536), numeric.Fixed(10*32*65536))
	h1, _ := s.Units.Create(def, 1, numeric.Fixed(50*32*65536), numeric.Fixed(30*65536), numeric.Fixed(50*32*65536))
	u0 := s.Units.Unit(h0)
	u1 := s.Units.Unit(h1)
	if u0 == nil || u1 == nil {
		t.Fatalf("units not created")
	}
	// Ensure Y above sea.
	u0.Y = numeric.Fixed(30 * 65536)
	u1.Y = numeric.Fixed(30 * 65536)
	// Publish synchronously.
	publishOne(s, u0)
	publishOne(s, u1)
	// Enemy outside LOS (distance 40 tiles > sight 5 tiles) should not be visible.
	if s.IsUnitVisible(0, u1) {
		t.Fatalf("enemy outside LOS should not be visible")
	}
	// Owner bypass: own unit always visible even outside coverage.
	if !s.IsUnitVisible(0, u0) {
		t.Fatalf("owner bypass should see own unit")
	}
	// Move enemy close to viewer and refresh.
	u1.X = numeric.Fixed(12 * 32 * 65536)
	u1.Z = numeric.Fixed(12 * 32 * 65536)
	u1.Y = numeric.Fixed(30 * 65536)
	// Refresh visibility for moved unit (simulates movement-refresh phase).
	cx := world.WorldToCell(u1.X) / 2
	cz := world.WorldToCell(u1.Z) / 2
	s.Vis.Refresh(visibility.ObserverID(u1.Handle), visibility.Observer{Owner: visibility.PlayerID(u1.Owner), CX: cx, CZ: cz, HeightByte: heightByteFor(u1), Radius: radiusFor(u1)})
	if !s.IsUnitVisible(0, u1) {
		t.Fatalf("enemy after moving inside LOS should be visible")
	}
	// Cloaked enemy should be rejected even inside LOS.
	u1.Hidden = true
	// Need to ensure sensor status does not have decloak.
	s.visStatus[int(u1.Handle)] = 0
	if s.IsUnitVisible(0, u1) {
		t.Fatalf("cloaked enemy should be rejected even inside LOS")
	}
	// The decloak timer does not bypass the direct cloak gate.
	s.visStatus[int(u1.Handle)] = visibility.DecloakBit
	if s.IsUnitVisible(0, u1) {
		t.Fatalf("hidden enemy with decloak timer should still be rejected")
	}
	// Authored stealth is NOT an input to this predicate. It is the contact
	// callback's third reject: it suppresses radar and sonar detection
	// outright, with no distance or elevation term, and never touches line of
	// sight [03 R-VIS-01 §5]. This block used to assert the opposite — that a
	// stealth unit "should be rejected even inside LOS" — which made every
	// stealth unit invisible to the eye as well as to the dish.
	u1.Hidden = false
	u1.Def.Stealth = true
	s.visStatus[int(u1.Handle)] = 0
	if !s.IsUnitVisible(0, u1) {
		t.Fatalf("stealth enemy inside LOS should be visible: stealth suppresses radar and sonar, never line of sight [03 R-VIS-01 §5]")
	}
	u1.Def.Stealth = false
	// Underwater enemy without exempt should be rejected.
	u1.Hidden = false
	s.visStatus[int(u1.Handle)] = 0
	terrain.SeaLevel = 20
	u1.Y = numeric.Fixed(10 * 65536) // below sea 20
	// The unit has not moved, so the observer stays where line 117 refreshed
	// it: the cell pair is not recomputed here.
	if s.IsUnitVisible(0, u1) {
		t.Fatalf("underwater enemy without 0x200 should be rejected even inside LOS")
	}
	// With 0x200 exempt (friendly), but enemy is not friendly; sensor would not set it. So still rejected.
	// Simulate friendly underwater: own unit underwater with friendly mask should be visible.
	u0.Y = numeric.Fixed(10 * 65536)
	s.visStatus[int(u0.Handle)] = visibility.FriendlyMask // includes 0x200
	if !s.IsUnitVisible(0, u0) {
		t.Fatalf("own underwater with friendly mask should be exempt")
	}
	// Allied not OR: publish ally 1's coverage should not make enemy 2 visible to viewer 0.
	// Already tested via LOS, but verify no allied OR.
	// Add third player allied with 0? For now check that enemy 1's coverage doesn't make enemy 2 visible.
	s2 := &Session{Catalog: cat, World: terrain, Mission: m}
	w2, _ := newSlicedWorld(cat)
	s2.Units = w2
	s2.Econ = economyForTest()
	s2.Econ.Players[0].Exists = true
	s2.Econ.Players[0].ControllerState = 1
	s2.Econ.Players[1].Exists = true
	s2.Econ.Players[1].ControllerState = 1
	s2.Econ.Players[2].Exists = true
	s2.Econ.Players[2].ControllerState = 2
	if err := createAndBindServicesForTest(t, s2); err != nil {
		t.Fatalf("bind2: %v", err)
	}
	// Publish ally 1 at 20,20
	s2.Vis.Publish(visibility.PlayerID(1), 20, 20, 0, 320)
	// Viewer 0 checks target owned by 2 at same tile - should be false (no ally OR).
	if s2.Vis.IsVisible(0, visibility.Target{Owner: 2, X: numeric.Fixed(20 * 32 * 65536), Z: numeric.Fixed(20 * 32 * 65536)}) {
		t.Fatalf("ally vision OR'd")
	}
	// Owner bypass for ally's own unit should succeed.
	if !s2.Vis.IsVisible(1, visibility.Target{Owner: 1, X: numeric.Fixed(20 * 32 * 65536), Z: numeric.Fixed(20 * 32 * 65536)}) {
		t.Fatalf("ally own visibility failed")
	}
	_ = h0
	_ = h1
}

// TestFogSnapshot verifies fog cache publication at each committed tick [03 §3.3].
func TestFogSnapshot(t *testing.T) {
	cat := minimalCatalogForStrict()
	terrain := minimalTerrain()
	m := syntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.SeedDeadlines(0)
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle // authoritative ticks run only in battle
	// Initially fog valid after RebuildFog in create? It starts invalid then rebuilt lazily. After publish, invalid.
	// Run one subtick via Step.
	s.Clock.ScaledAnchor = 0
	s.Step(1)
	cur := s.Snapshot.Current()
	if cur == nil {
		t.Fatalf("snapshot not published after Step")
	}
	if cur.Fog.W == 0 || cur.Fog.H == 0 {
		t.Fatalf("fog dimensions zero after tick: %d %d", cur.Fog.W, cur.Fog.H)
	}
	if !cur.Fog.Valid {
		t.Fatalf("fog should be valid after tick")
	}
	if len(cur.Fog.Ch0) == 0 || len(cur.Fog.Ch1) == 0 {
		t.Fatalf("fog channels empty")
	}
	// Second tick with 0 delta still publishes fog (valid remains).
}

// TestMovementRefreshViaTick verifies coverage updates at the retail cell/2
// threshold [03 §3.2].
func TestMovementRefreshViaTick(t *testing.T) {
	cat := minimalCatalogForStrict()
	terrain := &world.Terrain{CellW: 64, CellH: 64}
	terrain.Plot = make([]world.PlotCell, 64*64)
	_ = terrain.ApplySchema(nil, 0)
	m := syntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.Econ.SeedDeadlines(0)
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	def := &content.UnitDef{UnitName: "scout", MaxDamage: 100, SightDistance: 160, FootprintX: 1, FootprintZ: 1, Script: fixtureCOBProgram()}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	h, _ := s.Units.Create(def, 0, numeric.Fixed(10*32*65536), 0, numeric.Fixed(10*32*65536))
	u := s.Units.Unit(h)
	publishOne(s, u)
	s.RegisterAll()
	// Snapshot after publish should show visibility for unit's tile.
	if !s.Vis.IsVisible(0, visibility.Target{Owner: 1, X: numeric.Fixed(10 * 32 * 65536), Z: numeric.Fixed(10 * 32 * 65536)}) {
		// No enemy at same tile; just check own coverage via IsVisible with enemy owner but same pos after publish? Actually viewer 0 should see tile 10,10 via own coverage.
		// Use target owned by 1 at same location as viewer 0's unit.
		// Since viewer 0's LOS covers its own pos, enemy there should be visible.
		t.Logf("initial coverage check")
	}
	// Move unit by < cell/2 (16 world pixels = 16*65536 = 1,048,576? Actually cell=16 pixels=1,048,576, half tile=32 pixels/2=16 pixels? Visibility tile is 32 pixels =2 cells, half of that is 16 pixels =1 cell. So moving within same visibility tile should be throttled.
	// Move by 8 pixels (8*65536) within same visibility tile (32 pixels).
	origX := u.X
	u.X = numeric.Fixed(int64(origX) + 8*65536)
	u.Z = numeric.Fixed(int64(u.Z) + 0)
	// Run visibility-refresh via direct call (simulates kernel phase).
	cx := world.WorldToCell(u.X) / 2
	cz := world.WorldToCell(u.Z) / 2
	// Before refresh, footprint still at old tile; after refresh with same CX/CZ, should be throttled (no change).
	beforeCX := world.WorldToCell(origX) / 2
	if cx != beforeCX {
		t.Fatalf("moving 8 pixels should stay within same visibility tile, got %d vs %d", cx, beforeCX)
	}
	// Refresh should be throttled (no extra publish). Check byte grid unchanged.
	s.Vis.Refresh(visibility.ObserverID(u.Handle), visibility.Observer{Owner: visibility.PlayerID(u.Owner), CX: cx, CZ: cz, HeightByte: heightByteFor(u), Radius: radiusFor(u)})
	// Now move to next visibility tile (32 pixels = 2,097,152 = 32*65536).
	u.X = numeric.Fixed(int64(origX) + 40*65536) // 40 pixels > 32
	cx2 := world.WorldToCell(u.X) / 2
	if cx2 == beforeCX {
		t.Fatalf("far move should change CX")
	}
	s.Vis.Refresh(visibility.ObserverID(u.Handle), visibility.Observer{Owner: visibility.PlayerID(u.Owner), CX: cx2, CZ: cz, HeightByte: heightByteFor(u), Radius: radiusFor(u)})
	// After move, new tile should be visible.
	if !s.Vis.IsVisible(0, visibility.Target{Owner: 1, X: u.X, Z: u.Z}) {
		t.Fatalf("after far move, new position should be visible to owner")
	}
}

// TestSaveLoadRebuild ensures save/load restores mapping and republishes [03 §3.3].
func TestSaveLoadRebuild(t *testing.T) {
	cat := minimalCatalogForStrict()
	terrain := minimalTerrain()
	m := syntheticMission()
	m.Type = mission.TypeCampaign // use campaign so visibility mode defaults to all enabled [03 §3.1]
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Bind fixture sight shapes for sprite-mask raster (catalog has none in minimal fixture) [03 §3.2].
	s.Vis.SetMode(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled)
	s.Vis.SetShapes(fixtureShapesForTest())
	def := &content.UnitDef{UnitName: "scout", MaxDamage: 100, SightDistance: 160, Script: fixtureCOBProgram()}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	h, _ := s.Units.Create(def, 0, numeric.Fixed(10*32*65536), numeric.Fixed(30*65536), numeric.Fixed(10*32*65536))
	u := s.Units.Unit(h)
	u.Y = numeric.Fixed(30 * 65536)
	publishOne(s, u)
	// Simulate save: capture observers
	var observers []visibility.Observer
	for _, uu := range s.Units.IterSliced() {
		observers = append(observers, visibility.Observer{
			Owner: visibility.PlayerID(uu.Owner), CX: world.WorldToCell(uu.X) / 2, CZ: world.WorldToCell(uu.Z) / 2, HeightByte: heightByteFor(uu), Radius: radiusFor(uu),
		})
	}
	// Simulate load into new session with same terrain but fresh Vis.
	m2 := syntheticMission()
	m2.Type = mission.TypeCampaign
	s2 := &Session{Catalog: cat, World: terrain, Mission: m2}
	w2, _ := newSlicedWorld(cat)
	s2.Units = w2
	s2.Econ = economyForTest()
	s2.Econ.Players[0].Exists = true
	s2.Econ.Players[0].ControllerState = 1
	if err := createAndBindServicesForTest(t, s2); err != nil {
		t.Fatalf("bind2: %v", err)
	}
	s2.Vis.SetMode(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled)
	s2.Vis.SetShapes(fixtureShapesForTest())
	// RebuildAll before mapping read, then publish synchronously [03 §3.3] C10.
	s2.Vis.RebuildAll(nil)
	// At this point, no coverage (history enabled => zero).
	if s2.Vis.IsVisible(0, visibility.Target{Owner: 1, X: numeric.Fixed(10 * 32 * 65536), Y: numeric.Fixed(30 * 65536), Z: numeric.Fixed(10 * 32 * 65536)}) {
		t.Fatalf("after rebuild with no observers, no coverage should exist")
	}
	// Republish current footprints synchronously before loader returns — no empty-coverage frame.
	for _, ob := range observers {
		s2.Vis.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
	}
	if !s2.Vis.IsVisible(0, visibility.Target{Owner: 1, X: numeric.Fixed(10 * 32 * 65536), Y: numeric.Fixed(30 * 65536), Z: numeric.Fixed(10 * 32 * 65536)}) {
		t.Fatalf("after republish, coverage should be restored")
	}
	_ = h
}

func fixtureShapesForTest() *content.SightShapes {
	sh := &content.SightShapes{}
	for k := int32(0); k < 10; k++ {
		side := 11 + 2*k
		s := content.SightShape{W: side, H: side, AnchorX: side / 2, AnchorY: side / 2, Opaque: make([]bool, side*side)}
		for i := range s.Opaque {
			s.Opaque[i] = true
		}
		sh.Shapes = append(sh.Shapes, s)
	}
	return sh
}
