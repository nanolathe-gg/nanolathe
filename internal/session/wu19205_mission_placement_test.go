package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/world"
)

// The campaign spawner's position fixup [08 R-ENTRY-01 §6] and its height probe
// [08 R-ENTRY-02 §1]. Before WU-19-205 every mission record — structure and
// mobile alike — was allocated at its authored XYZ, so an authored building sat
// off the footprint grid and at whatever `YPos` the map happened to carry
// (review finding R08). The two easy ways to regress it are to drop the snap
// for one axis and to key the class on `CanMove` instead of `BMcode`, which
// SC21 shows would sweep every stock factory in with the structures.

// wu19205Terrain is a flat plot of the requested height with the requested sea
// level. Height bytes go in the derived low/high pair the probe reads
// [04 §6.1].
func wu19205Terrain(cellW, cellH int32, height uint8, sea uint8) *world.Terrain {
	attrs := make([]formats.TNTAttribute, cellW*cellH)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: height, Feature: world.PlotFeatureNone}
	}
	return &world.Terrain{
		CellW: cellW, CellH: cellH,
		Plot:     world.ExpandPlot(attrs, int(cellW), int(cellH)),
		Version:  0x2000,
		SeaLevel: sea,
	}
}

// wu19205Structure is a building definition: `BMcode` clear is the whole
// structure test (SC21), and an empty yard map compiles to the all-`o` default
// whose control byte carries bit 3, so every covered cell folds into the
// probe's aggregates [04 §6.2][08 R-ENTRY-02 §1].
func wu19205Structure(name string, footX, footZ int32) *content.UnitDef {
	def := &content.UnitDef{
		UnitName:   name,
		MaxDamage:  1000,
		FootprintX: footX,
		FootprintZ: footZ,
		BuildTime:  100,
		WorkerTime: 30,
	}
	def.CanonicalKey = content.CanonicalKey(name)
	return def
}

func TestMissionPlacementSnapsStructuresToFootprintGrid(t *testing.T) {
	const w = 1 << 16
	ter := wu19205Terrain(512, 512, 10, 0)
	cases := []struct {
		name             string
		def              *content.UnitDef
		x, y, z          int32
		wantX, wantY, wZ int32
	}{
		{
			// The named stock record: `maps/a shortage of water.ota` schema 0
			// places a 5x5 ARMMOHO at (5696, 6544); the fixup moves it to the
			// centre of the footprint-aligned cell at (5704, 6552).
			name: "odd 5x5 stock placement", def: wu19205Structure("moho", 5, 5),
			x: 5696 * w, y: 0, z: 6544 * w,
			wantX: 5704 * w, wantY: 10 * w, wZ: 6552 * w,
		},
		{
			// An even footprint straddles a cell boundary, so the `+2^19` term
			// is a round-to-nearest rather than an absorbed half-extent.
			name: "even 2x2 off-grid", def: wu19205Structure("even", 2, 2),
			x: 25 * w, y: 0, z: 43 * w,
			wantX: 32 * w, wantY: 10 * w, wZ: 48 * w,
		},
		{
			name: "odd 1x1 off-grid", def: wu19205Structure("one", 1, 1),
			x: 25 * w, y: 0, z: 43 * w,
			wantX: 24 * w, wantY: 10 * w, wZ: 40 * w,
		},
		{
			// Already centred: the fixup is idempotent on a snapped record.
			name: "already snapped", def: wu19205Structure("even", 2, 2),
			x: 32 * w, y: 0, z: 48 * w,
			wantX: 32 * w, wantY: 10 * w, wZ: 48 * w,
		},
		{
			// The authored Y is discarded for a structure: the probe wins.
			name: "authored height overridden", def: wu19205Structure("even", 2, 2),
			x: 32 * w, y: 900 * w, z: 48 * w,
			wantX: 32 * w, wantY: 10 * w, wZ: 48 * w,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := mission.UnitPlacement{UnitName: tc.def.UnitName, X: tc.x, Y: tc.y, Z: tc.z}
			x, y, z := missionPlacementPosition(ter, tc.def, up)
			if int64(x) != int64(tc.wantX) || int64(z) != int64(tc.wZ) {
				t.Fatalf("snapped to (%d, %d) in whole world units, want (%d, %d) [08 R-ENTRY-01 §6]",
					int64(x)/w, int64(z)/w, tc.wantX/w, tc.wZ/w)
			}
			if int64(y) != int64(tc.wantY) {
				t.Fatalf("probe height %d, want %d [08 R-ENTRY-02 §1]", int64(y)/w, tc.wantY/w)
			}
		})
	}
}

// A mobile definition keeps every authored word, including a Y that no terrain
// probe would ever produce. Keying the branch on the definition's `BMcode` is
// what makes this hold for a stock factory too, which authors CanMove=1 (SC21).
func TestMissionPlacementLeavesMobileRecordsAlone(t *testing.T) {
	const w = 1 << 16
	ter := wu19205Terrain(64, 64, 10, 0)
	def := wu19205Structure("mover", 2, 2)
	def.BMCode = 1
	def.CanMove = true
	up := mission.UnitPlacement{UnitName: "mover", X: 25 * w, Y: 900 * w, Z: 43 * w}
	x, y, z := missionPlacementPosition(ter, def, up)
	if int64(x) != 25*w || int64(y) != 900*w || int64(z) != 43*w {
		t.Fatalf("mobile record moved to (%d, %d, %d), want its authored (25, 900, 43) [08 R-ENTRY-01 §6]",
			int64(x)/w, int64(y)/w, int64(z)/w)
	}
	// The stock-factory shape: BMcode clear with CanMove set is a STRUCTURE and
	// must be snapped, or SC21's census would put every factory on this branch.
	factory := wu19205Structure("factory", 2, 2)
	factory.CanMove = true
	fx, _, fz := missionPlacementPosition(ter, factory, up)
	if int64(fx) != 32*w || int64(fz) != 48*w {
		t.Fatalf("a CanMove=1 structure stayed at (%d, %d); the class is BMcode, not CanMove [SC21]",
			int64(fx)/w, int64(fz)/w)
	}
}

// The probe's guard is a return of zero, not a clamp and not an error
// [08 R-ENTRY-02 §1]: `cx > 0`, `cz >= 1`, `cx + fw < cellW`, `cz + fh < cellH`.
func TestMissionPlacementHeightProbeBounds(t *testing.T) {
	const w = 1 << 16
	ter := wu19205Terrain(32, 32, 10, 0)
	def := wu19205Structure("even", 2, 2)
	cases := []struct {
		name  string
		x, z  int32
		wantY int32
	}{
		{"west edge snaps to cell -1", 0, 48 * w, 0},
		{"north edge snaps to cell 0", 48 * w, 16 * w, 0},
		{"east edge overruns the grid", 31 * 16 * w, 48 * w, 0},
		{"south edge overruns the grid", 48 * w, 31 * 16 * w, 0},
		{"interior probes the terrain", 48 * w, 48 * w, 10 * w},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := mission.UnitPlacement{UnitName: def.UnitName, X: tc.x, Z: tc.z}
			_, y, _ := missionPlacementPosition(ter, def, up)
			if int64(y) != int64(tc.wantY) {
				t.Fatalf("probe height %d, want %d [08 R-ENTRY-02 §1]", int64(y), tc.wantY)
			}
		})
	}
}

// No covered cell carries yard bit 3, so the aggregates never move off their
// 255/0 starts, the maximum compares below the minimum, and the probe returns
// the water surface less the definition's waterline as an 8-bit subtraction
// [08 R-ENTRY-02 §1].
func TestMissionPlacementHeightProbeWaterlineFallback(t *testing.T) {
	const w = 1 << 16
	ter := wu19205Terrain(32, 32, 10, 90)
	def := wu19205Structure("floater", 2, 2)
	def.YardMap = ".." // the '.' control byte is 0x00: no bit 3 [04 §6.2]
	def.Waterline = 4
	up := mission.UnitPlacement{UnitName: def.UnitName, X: 48 * w, Z: 48 * w}
	_, y, _ := missionPlacementPosition(ter, def, up)
	if int64(y) != 86*w {
		t.Fatalf("no-bit-3 probe height %d, want SeaLevel-waterline = 86 [08 R-ENTRY-02 §1]", int64(y)/w)
	}
	// The subtraction is a byte: a waterline above the sea level wraps rather
	// than seating the unit below the world.
	def.Waterline = 100
	if _, y, _ = missionPlacementPosition(ter, def, up); int64(y) != 246*w {
		t.Fatalf("wrapped probe height %d, want the 8-bit (90-100) = 246 [08 R-ENTRY-02 §1]", int64(y)/w)
	}
}

// The end-to-end half: battle entry's spawner has to call the fixup between the
// player check and the allocator, so a structure record reaches the pool
// already snapped and re-seated while a mobile one does not.
func TestReconstructUnitsAppliesPositionFixup(t *testing.T) {
	const w = 1 << 16
	s := wu19205SpawnerSession(t)
	m := strictSyntheticMission()
	m.Units = []mission.UnitPlacement{
		{UnitName: "teststruct", X: 25 * w, Y: 900 * w, Z: 43 * w, Player: 1},
		{UnitName: "armcom", X: 25 * w, Y: 900 * w, Z: 43 * w, Player: 1},
	}
	if err := reconstructUnits(s, m); err != nil {
		t.Fatalf("reconstructUnits: %v", err)
	}
	got := map[string][3]int64{}
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		got[u.Def.UnitName] = [3]int64{int64(u.X), int64(u.Y), int64(u.Z)}
	}
	if want := [3]int64{32 * w, 10 * w, 48 * w}; got["teststruct"] != want {
		t.Fatalf("structure spawned at %v, want the snapped, re-seated %v [08 R-ENTRY-01 §6]", got["teststruct"], want)
	}
	if want := [3]int64{25 * w, 900 * w, 43 * w}; got["armcom"] != want {
		t.Fatalf("mobile unit spawned at %v, want its authored %v [08 R-ENTRY-01 §6]", got["armcom"], want)
	}
}

// wu19205SpawnerSession is strictNewSessionWithUnits with one structure added
// to the fixture catalog. The catalog cannot be extended after the sliced pool
// is built (the pool is sized from it and resolves definitions by index), so
// the composition is repeated here rather than wrapped.
func wu19205SpawnerSession(t *testing.T) *Session {
	t.Helper()
	cat := strictMinimalCatalog()
	structure := wu19205Structure("teststruct", 2, 2)
	structure.SightDistance = 128
	cat.Units[structure.CanonicalKey] = structure
	installFixtureCOB(cat)
	s := &Session{
		Catalog:  cat,
		World:    wu19205Terrain(32, 32, 10, 0),
		Mission:  strictSyntheticMission(),
		Clock:    &clock.State{Requested: 10, Active: 10},
		Snapshot: &frame.Buffer{},
	}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("newSlicedWorld: %v", err)
	}
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = uint8(i + 1)
		p.EndGameCountdown = -1
	}
	s.Econ.SeedDeadlines(0)
	s.SeedSessionRNG(7, 11)
	s.InitBattleWindForSession()
	_ = createAndBindServicesForTest(t, s)
	s.RegisterAll()
	s.State = StateBattle
	return s
}
