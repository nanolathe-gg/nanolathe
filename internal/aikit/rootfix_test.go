package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// DepthSite offers only anchors the placement validator can accept: the
// footprint keeps a cell clear of the map's last row and column.
func TestDepthSiteStaysInsideTheValidator(t *testing.T) {
	w, h := int32(40), int32(30)
	m := &MapInfo{CellW: w, CellH: h, SeaLevel: 10}
	m.cellLo = make([]uint8, w*h)
	m.cellHi = make([]uint8, w*h)
	m.cellVoid = make([]bool, w*h)
	for i := range m.cellLo {
		m.cellLo[i], m.cellHi[i] = 50, 50
	}
	for _, f := range []int32{1, 2, 3, 4} {
		x, z, ok := m.DepthSite(w*16, h*16, f, f, -1000, 0, 10, 400, nil, 0)
		if !ok {
			t.Fatalf("footprint %d: no site", f)
		}
		cx, cz := (x-f*8)/16, (z-f*8)/16
		if cx < 1 || cz < 1 || cx+f >= w-1 || cz+f >= h-1 {
			t.Errorf("footprint %d: anchor (%d, %d) outside the validator's bounds", f, cx, cz)
		}
		sites := m.DepthSites(w*16, h*16, f, f, -1000, 0, 10, 400, m.Reach(MoveClass{MinDepth: -1000, MaxSlope: 10, MaxWaterSlope: 10, FootX: 1, FootZ: 1}), nil)
		for _, s := range sites {
			cx, cz := (s.X-f*8)/16, (s.Z-f*8)/16
			if cx+f >= w-1 || cz+f >= h-1 {
				t.Errorf("footprint %d: DepthSites anchor (%d, %d) outside the validator's bounds", f, cx, cz)
			}
		}
	}
}

// A point west or north of the map is off the grid, not in sector 0.
func TestGridAtOffTheGrid(t *testing.T) {
	g := NewGrid(&MapInfo{SectorW: 4, SectorH: 4})
	for i := range g.V {
		g.V[i] = 9
	}
	for _, p := range [][2]int32{{-1, 5}, {-127, 0}, {5, -64}, {4 * SectorWorld, 0}} {
		if v := g.At(p[0], p[1]); v != 0 {
			t.Errorf("At(%d, %d) = %d, want 0 off the grid", p[0], p[1], v)
		}
	}
	if g.At(0, 0) != 9 {
		t.Error("At(0, 0) left the grid")
	}
}

// Region ids run out at reachOverflow: every region past the last id
// shares it, and there is exactly one size per id.
func TestReachIdOverflow(t *testing.T) {
	w, h := int32(400), int32(400)
	m := &MapInfo{CellW: w, CellH: h, SeaLevel: 10}
	m.cellLo = make([]uint8, w*h)
	m.cellHi = make([]uint8, w*h)
	m.cellVoid = make([]bool, w*h)
	for z := int32(0); z < h; z++ {
		for x := int32(0); x < w; x++ {
			v := uint8(50) // land
			if (x+z)%2 == 1 {
				v = 0 // deep water isolates every land cell
			}
			m.cellLo[z*w+x], m.cellHi[z*w+x] = v, v
		}
	}
	r := m.Reach(MoveClass{MinDepth: -1000, MaxDepth: 0, MaxSlope: 10, MaxWaterSlope: 10, FootX: 1, FootZ: 1})
	if r.Regions() != reachOverflow {
		t.Fatalf("%d region ids, want %d", r.Regions(), reachOverflow)
	}
	var total int32
	for id := int32(1); id <= r.Regions(); id++ {
		total += r.Size(id)
	}
	if total != w*h/2 || r.Size(reachOverflow-1) != 1 || r.Size(reachOverflow) != w*h/2-(reachOverflow-1) {
		t.Errorf("sizes: total %d, last own id %d, shared %d", total, r.Size(reachOverflow-1), r.Size(reachOverflow))
	}
}

// Beyond maxFeatures, the features kept are the nearest the start (listed
// in row order), not the first rows'.
func TestRefreshFeaturesKeepsTheNearest(t *testing.T) {
	w, h := int32(120), int32(120)
	ter := &world.Terrain{CellW: w, CellH: h, Plot: make([]world.PlotCell, w*h), FeatureDefs: []*content.FeatureDef{{Blocking: true, FootprintX: 1, FootprintZ: 1}}}
	for i := range ter.Plot {
		ter.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	for z := int32(0); z < h; z += 2 {
		for x := int32(0); x < w; x += 2 {
			ter.Plot[z*w+x].SetFeature(0)
		}
	}
	var o Obs
	home := int32(60*16 + 8)
	e := executor{m: &ai.Manager{Terrain: ter}, mapInfo: &MapInfo{CellW: w, CellH: h, HomeX: home, HomeZ: home}, obs: &o}
	e.refreshFeatures(1)
	if len(o.Features) != maxFeatures {
		t.Fatalf("%d features listed, want %d", len(o.Features), maxFeatures)
	}
	var far int64
	listed := map[[2]int32]bool{}
	for i, f := range o.Features {
		listed[[2]int32{f.X, f.Z}] = true
		if d := Dist2(f.X, f.Z, home, home); d > far {
			far = d
		}
		if i > 0 {
			p := o.Features[i-1]
			if p.Z > f.Z || (p.Z == f.Z && p.X >= f.X) {
				t.Fatalf("features out of row order at %d", i)
			}
		}
	}
	for z := int32(0); z < h; z += 2 {
		for x := int32(0); x < w; x += 2 {
			fx, fz := x*16+8, z*16+8
			if d := Dist2(fx, fz, home, home); d < far && d <= FeatureRadius*FeatureRadius && !listed[[2]int32{fx, fz}] {
				t.Fatalf("feature at (%d, %d) is nearer than one listed but missing", fx, fz)
			}
		}
	}
}

// initBrain issues a command from Init, which has no batch to go to.
type initBrain struct{ countBrain }

func (b *initBrain) Init(k *Kit) {
	b.inits++
	k.Move([]pool.Handle{1}, 100, 100, false)
	_ = k.Emitted()
}

// A command issued from Init is dropped rather than crashing the host.
func TestInitCommandIsDropped(t *testing.T) {
	w := units.NewSliced(4, nil)
	b := &initBrain{}
	h := NewHost(&ai.Manager{Player: 0}, b, PersonaHard)
	for tick := uint32(1); tick < 40; tick++ {
		h.Step(tick, w, computerEconomy())
	}
	h.Close()
	if b.inits != 1 || b.thinks == 0 {
		t.Fatalf("%d inits, %d thinks", b.inits, b.thinks)
	}
}

// tickBrain records the ticks it thinks on.
type tickBrain struct {
	countBrain
	ticks []uint32
}

func (b *tickBrain) Think(k *Kit, o *Obs) { b.ticks = append(b.ticks, k.Tick) }

// Each slot thinks on its own phase of the think period however late its
// host starts, so hosts started together after a load do not all think on
// the same ticks.
func TestThinksKeepTheSlotPhase(t *testing.T) {
	w := units.NewSliced(4, nil)
	for _, start := range []uint32{1, 10001, 54321} {
		seen := map[uint32]uint8{}
		for slot := uint8(0); slot < 4; slot++ {
			econ := computerEconomy()
			econ.Players[slot].Exists, econ.Players[slot].ControllerState = true, 2
			b := &tickBrain{}
			h := NewHost(&ai.Manager{Player: slot}, b, PersonaHard)
			for tick := start; tick < start+120; tick++ {
				h.Step(tick, w, econ)
			}
			h.Close()
			phase := uint32(slot) * 7 % PersonaHard.ThinkEvery
			for _, tk := range b.ticks {
				if tk%PersonaHard.ThinkEvery != phase {
					t.Fatalf("start %d slot %d thought at %d, off its phase %d", start, slot, tk, phase)
				}
				if other, ok := seen[tk]; ok {
					t.Fatalf("start %d: slots %d and %d both thought at %d", start, other, slot, tk)
				}
				seen[tk] = slot
			}
			if len(b.ticks) == 0 || b.ticks[0] >= start+PersonaHard.ThinkEvery {
				t.Fatalf("start %d slot %d: first think %v", start, slot, b.ticks)
			}
		}
	}
}

// The analysis's void cells are the feature word's whole void band, as the
// plot cell and movement read it.
func TestTerrainVoidIsTheVoidBand(t *testing.T) {
	ter := &world.Terrain{CellW: 8, CellH: 1, Plot: make([]world.PlotCell, 8)}
	words := []uint16{world.PlotFeatureNone, world.PlotFeatureFringe, world.PlotFeatureVoid, 0xFFFC, 0xFFFB, 0xFFFA, 0, 0x00FD}
	for i, f := range words {
		ter.Plot[i].SetFeature(f)
	}
	void := terrainVoid(ter)
	for i := range words {
		if void[i] != ter.Plot[i].IsVoid() {
			t.Errorf("feature %#x: void %v, cell says %v", words[i], void[i], ter.Plot[i].IsVoid())
		}
	}
}
