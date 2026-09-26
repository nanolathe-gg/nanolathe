package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// The depth band a water building's placement enforces depends on its yard
// cells: sampled cells bound the maximum depth, float-only cells ignore it
// but must lie below the waterline [04 §6.4].
func TestWaterBand(t *testing.T) {
	cases := []struct {
		name           string
		yard           string
		min, max, line int32
		wantLo, wantHi int32
	}{
		{"floating maker", "wwwwwwwww", 11, 0, 4, 11, 10000},
		{"floater below its waterline", "wwww", 2, 0, 9, 9, 10000},
		{"sampled shipyard", "oooooooo", 30, 10000, 1, 30, 10000},
		{"sampled with a depth cap", "ooo", 34, 255, 0, 34, 255},
		{"mixed yard samples", "wwoo", 5, 40, 20, 5, 40},
	}
	for _, c := range cases {
		u := &aikit.UnitInfo{Def: &content.UnitDef{YardMap: c.yard, MinWaterDepth: c.min, MaxWaterDepth: c.max, Waterline: c.line}}
		lo, hi := waterBand(u)
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("%s: band [%d,%d], want [%d,%d]", c.name, lo, hi, c.wantLo, c.wantHi)
		}
	}
}

// An extractor fits a spot when the spot's floor lies in its depth band:
// a land extractor needs every cell dry, an underwater one enough depth.
func TestFitsSpot(t *testing.T) {
	m := &aikit.MapInfo{SeaLevel: 80}
	land := &aikit.UnitInfo{Def: &content.UnitDef{MinWaterDepth: -10000, MaxWaterDepth: 0}}
	under := &aikit.UnitInfo{Def: &content.UnitDef{MinWaterDepth: 19, MaxWaterDepth: 10000}}
	dry := &aikit.MetalSpot{Lo: 85, Hi: 95}
	shore := &aikit.MetalSpot{Lo: 70, Hi: 90, Water: true}
	deep := &aikit.MetalSpot{Lo: 30, Hi: 55, Water: true}
	for _, c := range []struct {
		name string
		x    *aikit.UnitInfo
		sp   *aikit.MetalSpot
		want bool
	}{
		{"land on dry", land, dry, true},
		{"land on shore", land, shore, false},
		{"underwater on deep", under, deep, true},
		{"underwater on shore", under, shore, false},
		{"underwater on dry", under, dry, false},
	} {
		if got := fitsSpot(m, c.x, c.sp); got != c.want {
			t.Errorf("%s: fits %v, want %v", c.name, got, c.want)
		}
	}
}

// Footprints are capped (3 on the ground, 4 at sea) so a handful of
// region maps serve every unit.
func TestCanonClass(t *testing.T) {
	a := canonClass(aikit.MoveClass{MinDepth: -10000, MaxDepth: 12, MaxSlope: 15, MaxWaterSlope: 255, FootX: 2, FootZ: 2})
	if a.FootX != 2 || a.FootZ != 2 {
		t.Errorf("2×2 ground class became %dx%d", a.FootX, a.FootZ)
	}
	b := canonClass(aikit.MoveClass{MinDepth: -10000, MaxDepth: 12, MaxSlope: 15, MaxWaterSlope: 255, FootX: 4, FootZ: 4})
	if b.FootX != 3 || b.FootZ != 3 {
		t.Errorf("4×4 ground class became %dx%d, want 3×3", b.FootX, b.FootZ)
	}
	s := canonClass(aikit.MoveClass{MinDepth: 3, MaxDepth: 10000, MaxSlope: 255, MaxWaterSlope: 255, FootX: 6, FootZ: 6})
	if s.FootX != 4 || s.FootZ != 4 {
		t.Errorf("ship footprint %dx%d, want 4×4", s.FootX, s.FootZ)
	}
}

// reach_mix: until enemy buildings are seen, reach is the mean over the
// plausible enemy starts — not our own (within 400 wu of home) and not one
// a unit of ours has stood beside; when every start has been looked at the
// whole prior returns.
func TestReachMixPlausibleStarts(t *testing.T) {
	m := &aikit.MapInfo{HomeX: 1000, HomeZ: 1000, Starts: [][2]int32{{1000, 1000}, {1200, 1000}, {5000, 1000}, {1000, 5000}}}
	unit := &aikit.UnitInfo{Index: 0, Role: aikit.RoleMobile}
	k := &aikit.Kit{Map: m, Table: &aikit.Table{Units: []*aikit.UnitInfo{unit}}}
	s := &shared{p: Params{ReachMix: 1}, k: k}
	s.terr.ready = true
	// Reach of definition 0 at each start: ours, beside ours, a land
	// neighbour, across the water.
	s.terr.reachAt = [][]int16{{1000}, {1000}, {1000}, {0}}
	s.terr.reachAvg = []int16{667}
	s.setupReachMix(k)
	b := &core.Board{K: k, O: &aikit.Obs{}}
	s.selectReach(b)
	if got := s.reachOf(unit); got != 500 {
		t.Fatalf("unscouted prior: reach %d, want 500 (starts 2 and 3)", got)
	}
	b.O.Own = []aikit.OwnUnit{{Info: unit, X: 5100, Z: 1000, Built: true}}
	s.selectReach(b)
	if got := s.reachOf(unit); got != 0 {
		t.Fatalf("start 2 looked at: reach %d, want 0", got)
	}
	b.O.Own = append(b.O.Own, aikit.OwnUnit{Info: unit, X: 1000, Z: 4900, Built: true})
	s.selectReach(b)
	if got := s.reachOf(unit); got != 500 {
		t.Fatalf("every start looked at: reach %d, want the whole prior 500", got)
	}
}

// tidal_field: water economy fills its fields in blocks of six, nearest
// home first, and wraps around.
func TestWaterFieldBlocks(t *testing.T) {
	tide := &aikit.UnitInfo{Index: 0}
	s := &shared{count: make([]int32, 1), fields: make([]waterFields, 1), fieldDefs: []int32{0}}
	f := &s.fields[0]
	f.n = 3
	f.x = [maxFields]int32{100, 200, 300}
	for _, c := range []struct{ owned, wantX int32 }{{0, 100}, {5, 100}, {6, 200}, {17, 300}, {18, 100}} {
		s.count[0] = c.owned
		if x, _, ok := s.fieldSite(tide); !ok || x != c.wantX {
			t.Errorf("%d owned: field x %d ok %v, want %d", c.owned, x, ok, c.wantX)
		}
	}
}

// reach_mix: a factory is the source of land constructors when it makes a
// builder of economy that is not a water unit; a shipyard's naval
// constructor does not count, an air plant's air constructor does.
func TestLandConstructorSource(t *testing.T) {
	con := func(i int32, r aikit.Role) *aikit.UnitInfo {
		return &aikit.UnitInfo{Index: i, Role: r | aikit.RoleMobile | aikit.RoleBuilder}
	}
	cv, cs, ca := con(0, 0), con(1, aikit.RoleNaval), con(2, aikit.RoleAir)
	vp := &aikit.UnitInfo{Index: 3, Role: aikit.RoleFactory, Builds: []*aikit.UnitInfo{cv}}
	sy := &aikit.UnitInfo{Index: 4, Role: aikit.RoleFactory, Builds: []*aikit.UnitInfo{cs}}
	ap := &aikit.UnitInfo{Index: 5, Role: aikit.RoleFactory, Builds: []*aikit.UnitInfo{ca}}
	k := &aikit.Kit{Table: &aikit.Table{Units: []*aikit.UnitInfo{cv, cs, ca, vp, sy, ap}}}
	s := &shared{k: k, info: make([]staticInfo, 6), count: make([]int32, 6), factories: []int32{3, 4, 5}}
	s.info[0].canEco, s.info[1].canEco, s.info[2].canEco = true, true, true
	s.info[1].water = true
	if !makesLandCons(s, vp) || makesLandCons(s, sy) || !makesLandCons(s, ap) {
		t.Fatalf("vehicle plant %v shipyard %v air plant %v", makesLandCons(s, vp), makesLandCons(s, sy), makesLandCons(s, ap))
	}
	s.count[4] = 1 // a shipyard only
	if s.landConsFactory() {
		t.Error("a shipyard is not a land constructor source")
	}
	s.count[3] = 1
	if !s.landConsFactory() {
		t.Error("a vehicle plant is")
	}
}
