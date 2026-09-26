package aikit

import (
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// islands builds a 64×32-cell map at sea level 50: two flat islands
// (height 60) at x 4..19 and x 44..59, deep water (height 10) elsewhere,
// and a shallow ford (height 45) from x 20..43 on rows 26..29.
func islands() *MapInfo {
	w, h := int32(64), int32(32)
	m := &MapInfo{CellW: w, CellH: h, WorldW: w * 16, WorldH: h * 16, SeaLevel: 50}
	m.cellLo = make([]uint8, w*h)
	m.cellHi = make([]uint8, w*h)
	m.cellVoid = make([]bool, w*h)
	for z := int32(0); z < h; z++ {
		for x := int32(0); x < w; x++ {
			v := uint8(10)
			switch {
			case z >= 4 && z < 24 && ((x >= 4 && x < 20) || (x >= 44 && x < 60)):
				v = 60
			case z >= 26 && z < 30 && x >= 20 && x < 44:
				v = 45
			}
			m.cellLo[z*w+x], m.cellHi[z*w+x] = v, v
		}
	}
	return m
}

func centre(cx, cz int32) (int32, int32) { return cx*16 + 8, cz*16 + 8 }

// The contract the brains rely on: land units see the two islands as
// separate regions, a class that wades the ford joins them only through
// it, and ships share one sea that touches both coasts.
func TestReachIslands(t *testing.T) {
	m := islands()
	land := m.Reach(MoveClass{MinDepth: -10000, MaxDepth: 0, MaxSlope: 10, MaxWaterSlope: 255, FootX: 2, FootZ: 2})
	ax, az := centre(10, 10)
	bx, bz := centre(50, 10)
	a, b := land.At(ax, az), land.At(bx, bz)
	if a == 0 || b == 0 || a == b {
		t.Fatalf("land regions %d and %d: want two distinct islands", a, b)
	}
	if land.Size(a) != 15*19 {
		t.Errorf("island anchors = %d, want %d (16×20 cells, 2×2 footprint)", land.Size(a), 15*19)
	}
	// Dist is how close region a comes to the far island's centre: its
	// east coast, about 500 world units away, and nothing within 200.
	if d := land.Dist(a, bx, bz, 2000); d < 450 || d > 550 || land.Dist(a, bx, bz, 200) != -1 {
		t.Errorf("west island to east centre: %d", d)
	}
	// Wading to depth 12 crosses the ford, which lies south of both islands:
	// it does not touch them (rows 24..25 are deep), so still two regions.
	wade := m.Reach(MoveClass{MinDepth: -10000, MaxDepth: 12, MaxSlope: 10, MaxWaterSlope: 255, FootX: 2, FootZ: 2})
	if wade.At(ax, az) == wade.At(bx, bz) {
		t.Error("wading class joins the islands across deep water")
	}
	sea := m.Reach(MoveClass{MinDepth: 3, MaxDepth: 10000, MaxSlope: 255, MaxWaterSlope: 255, FootX: 3, FootZ: 3})
	if sea.Regions() != 1 {
		t.Errorf("sea regions = %d, want 1", sea.Regions())
	}
	s := sea.At(centre(30, 10))
	if d := sea.Dist(s, ax, az, 400); d < 0 || d > 200 {
		t.Errorf("sea to west island centre: %d, want a coast within 200", d)
	}
	// The same class twice is one computation.
	if m.Reach(sea.Class) != sea {
		t.Error("Reach is not cached per class")
	}
	mx, mz := centre(32, 10)
	r1, r2 := land.Near2(mx, mz, 100)
	if r1 != 0 || r2 != 0 {
		t.Errorf("mid-sea point touches land regions %d %d", r1, r2)
	}
}

func TestDepthSite(t *testing.T) {
	m := islands()
	hx, hz := centre(10, 10)
	// A shipyard-like footprint needing 30 deep water: open sea, not the ford.
	x, z, ok := m.DepthSite(hx, hz, 4, 4, 30, 10000, 255, 1000, nil, 0)
	if !ok {
		t.Fatal("no deep site near the island")
	}
	cx, cz := x/16, z/16
	for j := cz - 2; j < cz+2; j++ {
		for i := cx - 2; i < cx+2; i++ {
			if m.cellHi[j*m.CellW+i] != 10 {
				t.Fatalf("site (%d,%d) covers a cell of height %d", x, z, m.cellHi[j*m.CellW+i])
			}
		}
	}
	// Dry land for a factory within the island.
	if _, _, ok := m.DepthSite(hx, hz, 8, 8, -10000, 0, 10, 600, nil, 0); !ok {
		t.Error("no dry site on the island")
	}
	if n := m.CountCells(hx, hz, 2000, -10000, 0, 10); n != 2*16*20 {
		t.Errorf("dry cells = %d, want %d", n, 2*16*20)
	}
	sea := m.Reach(MoveClass{MinDepth: 3, MaxDepth: 10000, MaxSlope: 255, MaxWaterSlope: 255, FootX: 3, FootZ: 3})
	sites := m.DepthSites(hx, hz, 4, 4, 30, 10000, 255, 1000, sea, nil)
	if len(sites) != 1 || sites[0].Region != sea.At(x, z) {
		t.Errorf("per-region sites %+v, want one in the sea region", sites)
	}
}

// benchMap is a 544×864-cell map (the largest stock water maps) with
// alternating raised and sunken blocks and a little ripple.
func benchMap() *MapInfo {
	w, h := int32(544), int32(864)
	m := &MapInfo{CellW: w, CellH: h, SeaLevel: 85, WorldW: w * 16, WorldH: h * 16}
	m.cellLo = make([]uint8, w*h)
	m.cellHi = make([]uint8, w*h)
	m.cellVoid = make([]bool, w*h)
	for z := int32(0); z < h; z++ {
		for x := int32(0); x < w; x++ {
			base := int32(40)
			if (x/60+z/60)%3 == 0 {
				base = 100
			}
			m.cellLo[z*w+x] = uint8(base)
			m.cellHi[z*w+x] = uint8(base + (x+z)%3)
		}
	}
	return m
}

// BenchmarkReach is one class's region map (a brain builds about a dozen
// in Init).
func BenchmarkReach(b *testing.B) {
	m := benchMap()
	c := MoveClass{MinDepth: -10000, MaxDepth: 12, MaxSlope: 15, MaxWaterSlope: 255, FootX: 2, FootZ: 2}
	m.buildReach(c)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.buildReach(c)
	}
}

// sharedTestTerrain is a small archipelago with scattered metal, a few void
// holes and a flat metal field, so the spots, the water census and the
// region maps all have something to disagree about.
func sharedTestTerrain() *world.Terrain {
	rnd := testRand(19)
	w, h := int32(96), int32(80)
	ter := &world.Terrain{CellW: w, CellH: h, SeaLevel: 40, WindMin: 300, WindMax: 1700, Tidal: 0.018, Plot: make([]world.PlotCell, w*h)}
	for z := int32(0); z < h; z++ {
		for x := int32(0); x < w; x++ {
			p := &ter.Plot[z*w+x]
			height := uint8(20 + rnd(10)) // sea
			if (x/24+z/20)%2 == 0 {
				height = uint8(50 + rnd(12)) // island
			}
			p.SetHeight(height)
			p.SetMinHeight(height - uint8(rnd(3)))
			p.SetMaxHeight(height + uint8(rnd(4)))
			switch {
			case x > 60 && z > 50:
				p.SetMetal(uint8(30 + rnd(2))) // a metal field
			case rnd(30) == 0:
				p.SetMetal(uint8(rnd(220)))
			}
			p.SetFeature(world.PlotFeatureNone)
			if rnd(97) == 0 {
				p.SetFeature(world.PlotFeatureVoid)
			}
		}
	}
	return ter
}

// sameAnalysis reports the first difference between two hosts' analyses,
// the region maps of the classes asked included.
func sameAnalysis(t *testing.T, what string, a, b *MapInfo, classes []MoveClass) {
	t.Helper()
	strip := func(m *MapInfo) MapInfo {
		c := *m
		c.reaches, c.runScratch, c.colScratch, c.shared = nil, nil, nil, nil
		return c
	}
	if !reflect.DeepEqual(strip(a), strip(b)) {
		t.Fatalf("%s: analyses differ", what)
	}
	for _, c := range classes {
		ra, rb := a.Reach(c), b.Reach(c)
		if !reflect.DeepEqual(*ra, *rb) {
			t.Fatalf("%s: class %+v region maps differ", what, c)
		}
	}
}

// The battle's shared analysis gives every host exactly the analysis it
// would have computed alone, whatever its start and extractor footprint,
// however the hosts' preparations interleave (run with -race), and a host
// that read other void cells than the first analyzes the map alone.
func TestSharedMapAnalysisEqualsTheHostsOwn(t *testing.T) {
	ter := sharedTestTerrain()
	void := terrainVoid(ter)
	starts := [][2]int32{{200, 200}, {1300, 200}, {200, 1100}, {1300, 1100}}
	foots := [][2]int32{{2, 2}, {3, 3}, {2, 2}, {0, 0}} // 0 normalizes to 2
	classes := []MoveClass{
		{MinDepth: -10000, MaxDepth: 0, MaxSlope: 3, MaxWaterSlope: 255, FootX: 2, FootZ: 2},
		{MinDepth: -10000, MaxDepth: 12, MaxSlope: 5, MaxWaterSlope: 255, FootX: 3, FootZ: 3},
		{MinDepth: 4, MaxDepth: 10000, MaxSlope: 255, MaxWaterSlope: 255, FootX: 3, FootZ: 3},
	}
	var shared ai.BattleShared
	m := &ai.Manager{Shared: &shared}
	got := make([]*MapInfo, len(starts))
	var wg sync.WaitGroup
	for i := range starts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mi := analyzeMap(sharedAnalysis(m), ter, void, starts, foots[i][0], foots[i][1], starts[i][0], starts[i][1])
			for _, c := range classes {
				mi.Reach(c)
			}
			got[i] = mi
		}(i)
	}
	wg.Wait()
	sh := sharedAnalysis(m)
	for i := range starts {
		if got[i].shared != sh {
			t.Fatalf("host %d did not use the battle's analysis", i)
		}
		alone := analyzeMap(nil, ter, void, starts, foots[i][0], foots[i][1], starts[i][0], starts[i][1])
		if len(alone.Spots) == 0 {
			t.Fatal("the test map offers no spots; the case proves nothing")
		}
		sameAnalysis(t, "host "+strconv.Itoa(i), got[i], alone, classes)
		for _, c := range classes {
			if got[i].Reach(c) != got[0].Reach(c) {
				t.Fatalf("host %d built its own region map for class %+v", i, c)
			}
		}
	}
	// Hosts 0 and 3 share a footprint but not a start: their spot orders are
	// their own.
	if reflect.DeepEqual(got[0].Spots, got[3].Spots) {
		t.Fatal("two starts ordered the spots alike; the case proves nothing")
	}
	if len(sh.sites) != 2 || len(sh.reach) != len(classes) {
		t.Fatalf("battle analysis holds %d footprints and %d classes, want 2 and %d", len(sh.sites), len(sh.reach), len(classes))
	}

	// A later host that read another void cell does not take the battle's
	// snapshot.
	late := append([]bool(nil), void...)
	late[len(late)/2] = !late[len(late)/2]
	own := analyzeMap(sh, ter, late, starts, 2, 2, starts[0][0], starts[0][1])
	if own.shared != nil {
		t.Fatal("a host with other void cells used the battle's snapshot")
	}
	sameAnalysis(t, "late host", own, analyzeMap(nil, ter, late, starts, 2, 2, starts[0][0], starts[0][1]), classes)
}
