package tactics

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// bridgeMap is two flat islands (height 60) on a sea (level 50, floor 10),
// x 2..45 and x 66..93 cells, rows 2..29, joined by a land bridge four
// cells wide (rows 14..17) — a passage for anything that cannot wade deep
// water. Home is on the west island.
func bridgeMap() *aikit.MapInfo {
	w, h := 96, 32
	attrs := make([]formats.TNTAttribute, w*h)
	for z := 0; z < h; z++ {
		for x := 0; x < w; x++ {
			v := uint8(10)
			island := z >= 2 && z < 30 && ((x >= 2 && x < 46) || (x >= 66 && x < 94))
			bridge := z >= 14 && z < 18 && x >= 46 && x < 66
			if island || bridge {
				v = 60
			}
			attrs[z*w+x] = formats.TNTAttribute{Height: v, Feature: world.PlotFeatureNone}
		}
	}
	ter := &world.Terrain{CellW: int32(w), CellH: int32(h), SeaLevel: 50, Plot: world.ExpandPlot(attrs, w, h)}
	return aikit.AnalyzeMap(ter, [][2]int32{{8 * 16, 16 * 16}, {80 * 16, 16 * 16}}, 2, 2, 8*16, 16*16)
}

func passageArmy(t *testing.T) (*Army, *core.Board, *squad) {
	t.Helper()
	m := bridgeMap()
	tank := &aikit.UnitInfo{Index: 0, Side: "ARM", Role: aikit.RoleCombat | aikit.RoleMobile, FootX: 2, FootZ: 2, Range: 200, Value: 100,
		Def: &content.UnitDef{MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 10, MaxWaterSlope: 255, Weapon1Def: weapon(20, 30, nil)}}
	k := &aikit.Kit{Side: "ARM", Table: &aikit.Table{Units: []*aikit.UnitInfo{tank}}, Map: m}
	a := &Army{P: DefaultParams()}
	a.setupReach(k)
	if !a.reachReady {
		t.Fatal("no reach tables")
	}
	b := &core.Board{K: k, O: &aikit.Obs{}, HomeX: m.HomeX, HomeZ: m.HomeZ}
	s := &a.sq[sqMain]
	s.id = sqMain
	c := a.defCls[0]
	s.addGroup(c, uint16(a.rcls[c].r.At(m.HomeX, m.HomeZ)), 1000, 200)
	s.total.n = 16
	return a, b, s
}

// A squad gathered on the bridge stands in a passage; one on the island
// does not; the verdicts match the layout's rule and do not change once
// cached; passage=0 and the fleet never judge.
func TestBlobInPassage(t *testing.T) {
	a, b, s := passageArmy(t)
	bridge := [2]int32{56 * 16, 16 * 16}
	island := [2]int32{20 * 16, 12 * 16}
	for pass := 0; pass < 2; pass++ { // the second pass reads the cache
		if !a.blobInPassage(b, s, bridge[0], bridge[1]) {
			t.Errorf("pass %d: the bridge is not a passage", pass)
		}
		if a.blobInPassage(b, s, island[0], island[1]) {
			t.Errorf("pass %d: the island is a passage", pass)
		}
	}
	mc := &a.rcls[a.defCls[0]].mc
	if !b.K.Map.InPassage(mc, 55, 15, mc.FootX, mc.FootZ) {
		t.Error("the layout's rule does not call the bridge a passage")
	}
	a.P.Passage = false
	if a.blobInPassage(b, s, bridge[0], bridge[1]) {
		t.Error("passage=0 judged the bridge")
	}
	a.P.Passage = true
	f := &a.sq[sqNaval]
	f.id = sqNaval
	if a.blobInPassage(b, f, bridge[0], bridge[1]) {
		t.Error("the fleet judged the bridge")
	}
}

// A waiting point on the bridge walks back to open ground on the home
// island, no nearer home than the least distance; with no room on the home
// side it goes forward past the bridge; the stage point skips the bridge.
func TestOutOfPassage(t *testing.T) {
	a, b, s := passageArmy(t)
	x, z := a.outOfPassage(b, s, 56*16, 16*16, 80*16, 16*16, gatherMinHome)
	if x >= 46*16 || a.blobInPassage(b, s, x, z) || aikit.Dist(x, z, b.HomeX, b.HomeZ) < gatherMinHome {
		t.Errorf("moved to (%d,%d), want open ground on the west island at least %d from home", x, z, gatherMinHome)
	}
	if fx, _ := a.outOfPassage(b, s, 56*16, 16*16, 80*16, 16*16, 900); fx < 66*16 {
		t.Errorf("no room on the home side: moved to x %d, want the east island", fx)
	}
	if a.stats.passage != 2 {
		t.Errorf("moves counted %d, want 2", a.stats.passage)
	}
	// A stage point walked back from a target on the east island toward
	// the squad on the west island lands off the bridge.
	m := b.K.Map
	a.static = aikit.NewGrid(m)
	a.known = make([]uint8, m.SectorW*m.SectorH)
	for i := range a.known {
		a.known[i] = 1
	}
	s.cx, s.cz = 8*16, 16*16
	s.tx, s.tz = 80*16, 16*16
	s.present.dps = 100
	a.P.Passage = false
	a.stagePoint(b, s)
	sx0 := s.sx
	a.P.Passage = true
	if !a.blobInPassage(b, s, sx0, s.sz) {
		t.Fatalf("fixture: the stage point without the rule (%d) does not touch the bridge", sx0)
	}
	a.stagePoint(b, s)
	if a.blobInPassage(b, s, s.sx, s.sz) || s.sx >= sx0 || s.sx == s.cx {
		t.Errorf("stage point (%d,%d), want open ground short of %d on the squad's side", s.sx, s.sz, sx0)
	}
}
