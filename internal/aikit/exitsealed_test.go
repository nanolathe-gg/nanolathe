package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// noExtra is an empty, non-nil extra set: flood stops at the window edge
// and leaves the recorded reach alone.
var noExtra = []int32{}

// pocketMap is broken ground: square cliff blocks a land class cannot
// climb, dense enough to wall off pockets of every size, from generator g.
func pocketMap(g *Rand, w, h int32, density int32) (*MapInfo, *world.Terrain) {
	m := &MapInfo{CellW: w, CellH: h, WorldW: w * 16, WorldH: h * 16}
	m.cellLo = make([]uint8, w*h)
	m.cellHi = make([]uint8, w*h)
	m.cellVoid = make([]bool, w*h)
	for i := range m.cellLo {
		m.cellLo[i], m.cellHi[i] = 50, 50
	}
	for n := int32(0); n < w*h*density/1000; n++ {
		x0, z0, s := g.Intn(w), g.Intn(h), 1+g.Intn(6)
		for z := z0; z < z0+s && z < h; z++ {
			for x := x0; x < x0+s && x < w; x++ {
				m.cellLo[z*w+x], m.cellHi[z*w+x] = 0, 100
			}
		}
	}
	t := &world.Terrain{CellW: w, CellH: h, Plot: make([]world.PlotCell, w*h)}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	return m, t
}

// testFactory is a factory of the given footprint whose units walk out
// with a 2×2 land class.
func testFactory(fx, fz int32) *UnitInfo {
	walker := &UnitInfo{Role: RoleMobile, FootX: 2, FootZ: 2, Def: &content.UnitDef{MaxSlope: 15, MaxWaterDepth: 20, MinWaterDepth: -10000}}
	return &UnitInfo{Role: RoleFactory, FootX: fx, FootZ: fz, Builds: []*UnitInfo{walker}}
}

// The guard's own-exit test answers exactly what drawing the planned
// factory's window and walking it answered, on ground broken into pockets
// of every size, for anchors of both parities across the whole map and
// beyond its edges, three footprints and several ticks (the free regions
// are relabelled on each key).
func TestExitSealedIsTheWindowWalk(t *testing.T) {
	g := NewRand(20260924, 1)
	var sealed, open int
	for round := 0; round < 3; round++ {
		m, ter := pocketMap(&g, 120, 96, [...]int32{6, 12, 25}[round])
		e := &executor{m: &ai.Manager{Terrain: ter}, mapInfo: m}
		var ref exitGrid
		for fi, f := range []*UnitInfo{testFactory(6, 6), testFactory(5, 7), testFactory(8, 5)} {
			e.lastTick = uint32(1 + fi)
			for cz := int32(-4 + fi); cz < m.CellH+4; cz += 2 {
				for cx := int32(-4 + round); cx < m.CellW+4; cx += 2 {
					fcx, fcz := cx*16+f.FootX*8, cz*16+f.FootZ*8
					got, gotOK := e.exitSealed(nil, f, fcx, fcz)
					wantOK := e.buildExitGrid(&ref, nil, f, 0, fcx, fcz, false)
					want := wantOK && !ref.flood(noExtra)
					if gotOK != wantOK || gotOK && got != want {
						t.Fatalf("round %d factory %d at cell (%d, %d): exitSealed %v (ok %v), window walk %v (ok %v)", round, fi, cx, cz, got, gotOK, want, wantOK)
					}
					if want {
						sealed++
					} else {
						open++
					}
				}
			}
		}
	}
	if sealed < 1000 || open < 1000 {
		t.Fatalf("the fixture exercised %d sealed and %d open exits; want both", sealed, open)
	}
}
