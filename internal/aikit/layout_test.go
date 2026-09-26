package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
)

// grid returns an exit picture with every macro cell free and one seed in
// the middle of the window.
func testGrid() *exitGrid {
	g := &exitGrid{}
	g.ensure(exitWindow * exitWindow)
	for k := range g.cost {
		g.cost[k] = gridFree
	}
	for k := range g.who {
		g.who[k] = -1
	}
	c := int32(exitRadius)
	g.seeds = append(g.seeds[:0], c*exitWindow+c)
	return g
}

// ring walls the seed in with a square of the given cost at Chebyshev
// distance r, recording blocker b on every wall cell.
func (g *exitGrid) ring(r int32, cost int32, b int32) {
	c := int32(exitRadius)
	for j := c - r; j <= c+r; j++ {
		for i := c - r; i <= c+r; i++ {
			if absI32(i-c) != r && absI32(j-c) != r {
				continue
			}
			k := j*exitWindow + i
			g.cost[k] = cost
			g.who[int(k)*blkPerCell] = b
		}
	}
}

// The guard's contract: a footprint that would close the last gap in a
// wall around the exit cuts it; one elsewhere does not; a sealed exit
// cannot be cut further.
func TestExitGuardCuts(t *testing.T) {
	g := testGrid()
	g.ring(5, gridBlocked, -1)
	// Open a two-cell-wide gap east of the seed (macro cells i=c+5, j=c,c+1).
	c := int32(exitRadius)
	for _, j := range []int32{c, c + 1} {
		g.cost[j*exitWindow+c+5] = gridFree
	}
	g.sealed = !g.flood(nil)
	if g.sealed {
		t.Fatal("exit with a gap reported sealed")
	}
	// Window origin 0: macro cell (i, j) is plot cells (2i, 2j).
	g.ox, g.oz = 0, 0
	if cut, _ := g.cuts(2*(c+5), 2*c, 2, 4, nil); !cut {
		t.Error("filling the gap does not cut the exit")
	}
	if cut, _ := g.cuts(2*(c+20), 2*(c+20), 4, 4, nil); cut {
		t.Error("a site far outside the wall cuts the exit")
	}
	g.cost[c*exitWindow+c+5], g.cost[(c+1)*exitWindow+c+5] = gridBlocked, gridBlocked
	g.sealed = !g.flood(nil)
	if cut, _ := g.cuts(2*(c+1), 2*c, 2, 2, nil); cut || !g.sealed {
		t.Error("a sealed exit reported as cut again")
	}
}

// Opening a sealed exit takes the cheapest way out: through a wall of
// trees (cost 1 each) rather than a wall of buildings, and lists the
// blockers nearest the exit first.
func TestCheapestOpening(t *testing.T) {
	g := testGrid()
	g.ring(3, 1, 0) // trees
	g.ring(6, 9, 1) // own buildings, dearer
	g.ring(9, gridBlocked, -1)
	c := int32(exitRadius)
	g.cost[c*exitWindow+c+9] = 1 // a single tree in the outer wall
	g.who[int(c*exitWindow+c+9)*blkPerCell] = 2
	g.blk = []gridBlocker{{feature: true, cost: 1}, {cost: 9}, {feature: true, cost: 1}}
	if g.flood(nil) {
		t.Fatal("walled exit reported open")
	}
	got := g.cheapestOpening(nil, 4)
	want := []int32{0, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("blockers %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("blockers %v, want %v (nearest the exit first)", got, want)
		}
	}
}

// The row rules: flush same-class neighbours or a street, never a 1–2
// cell gap; rows stay one or two buildings deep and at most 24 long;
// nothing within 3 cells of a factory or in its front lane.
func TestRowRules(t *testing.T) {
	rs := &rowState{}
	add := func(x, z, fx, fz int32, g rowGroup) {
		rs.blds = append(rs.blds, rowBld{x0: x, z0: z, x1: x + fx, z1: z + fz, g: g, blk: -1})
	}
	add(0, 0, 5, 5, grpEnergy)   // a solar
	add(5, 0, 5, 5, grpEnergy)   // flush beside it
	add(40, 0, 6, 6, grpFactory) // a factory; lane z 6..18, x 39..47
	// Blocks as gatherRows would find them.
	rs.blks = []rowBlock{{x0: 0, z0: 0, x1: 10, z1: 5, n: 2, unit: 5}}
	rs.blds[0].blk, rs.blds[1].blk = 0, 0
	cases := []struct {
		name      string
		x, z, f   int32
		g         rowGroup
		ok, joins bool
		deep      bool
	}{
		{"extends the row", 10, 0, 5, grpEnergy, true, true, false},
		{"one-cell gap", 11, 0, 5, grpEnergy, false, false, false},
		{"street away", 13, 0, 5, grpEnergy, true, false, false},
		{"second rank", 0, 5, 5, grpEnergy, true, true, true},
		{"corner contact joins the block", 10, 5, 5, grpEnergy, true, true, true},
		{"maker flush to energy", 10, 0, 3, grpMaker, false, false, false},
		{"maker across a street", 13, 0, 3, grpMaker, true, false, false},
		{"too close to the factory", 34, 0, 4, grpEnergy, false, false, false},
		{"in the factory's lane", 41, 14, 3, grpLoose, false, false, false},
		{"beside the lane", 49, 14, 3, grpLoose, true, false, false},
		// Towers are loose: never packed into a row, two cells from it.
		{"tower flush to a row", 10, 0, 2, grpLoose, false, false, false},
		{"tower one cell from a row", 11, 0, 2, grpLoose, false, false, false},
		{"tower two cells from a row", 12, 0, 2, grpLoose, true, false, false},
	}
	for _, c := range cases {
		r := rowBld{x0: c.x, z0: c.z, x1: c.x + c.f, z1: c.z + c.f, g: c.g}
		ok, joins, deep := rs.rowRules(&r)
		if ok != c.ok || (ok && (joins != c.joins || deep != c.deep)) {
			t.Errorf("%s: ok %v joins %v deep %v, want %v %v %v", c.name, ok, joins, deep, c.ok, c.joins, c.deep)
		}
	}
	// A row stops at 24 cells: four solars make 20, a fifth would be 25.
	rs.blks[0] = rowBlock{x0: 0, z0: 0, x1: 20, z1: 5, n: 4, unit: 5}
	r := rowBld{x0: 20, z0: 0, x1: 25, z1: 5, g: grpEnergy}
	rs.blds = append(rs.blds[:2], rowBld{x0: 10, z0: 0, x1: 15, z1: 5, g: grpEnergy, blk: 0}, rowBld{x0: 15, z0: 0, x1: 20, z1: 5, g: grpEnergy, blk: 0})
	if ok, _, _ := rs.rowRules(&r); ok {
		t.Error("a 25-cell row accepted")
	}
}

// A site in a 10-cell ramp between two cliffs is refused; one beside a
// single cliff, or on open ground, is not.
func TestCorridor(t *testing.T) {
	w, h := int32(48), int32(32)
	m := &MapInfo{CellW: w, CellH: h, WorldW: w * 16, WorldH: h * 16}
	m.cellLo = make([]uint8, w*h)
	m.cellHi = make([]uint8, w*h)
	m.cellVoid = make([]bool, w*h)
	for z := int32(0); z < h; z++ {
		for x := int32(0); x < w; x++ {
			lo, hi := uint8(60), uint8(60)
			if x == 10 || x == 21 {
				lo = 0 // cliffs: the ramp is x 11..20
			}
			m.cellLo[z*w+x], m.cellHi[z*w+x] = lo, hi
		}
	}
	c := MoveClass{MinDepth: -10000, MaxDepth: 20, MaxSlope: 12, MaxWaterSlope: 12, FootX: 2, FootZ: 2}
	cases := []struct {
		name   string
		cx, cz int32
		want   bool
	}{
		{"in the ramp", 15, 10, true},
		{"beside one cliff", 22, 10, false},
		{"open ground", 32, 10, false},
		{"at the map edge", 0, 10, false},
	}
	for _, k := range cases {
		if got := m.corridor(&c, k.cx, k.cz, 2, 2); got != k.want {
			t.Errorf("%s: corridor %v, want %v", k.name, got, k.want)
		}
	}
}

// ridge builds a 128×64-cell map: open ground west (x < 40) and east
// (x ≥ 88) of a cliff band a land class cannot climb, crossed by an
// 8-cell-wide ramp on rows 28..35 and, when two is set, a second on rows
// 8..15. Our start is in the west, the only other start in the east, and
// one metal spot on each side.
func ridge(two bool) *MapInfo {
	w, h := int32(128), int32(64)
	m := &MapInfo{CellW: w, CellH: h, WorldW: w * 16, WorldH: h * 16, SeaLevel: 0, FootX: 2, FootZ: 2}
	m.cellLo = make([]uint8, w*h)
	m.cellHi = make([]uint8, w*h)
	m.cellVoid = make([]bool, w*h)
	for z := int32(0); z < h; z++ {
		for x := int32(0); x < w; x++ {
			lo, hi := uint8(50), uint8(50)
			ramp := z >= 28 && z < 36 || two && z >= 8 && z < 16
			if x >= 40 && x < 88 && !ramp {
				lo, hi = 0, 100
			}
			m.cellLo[z*w+x], m.cellHi[z*w+x] = lo, hi
		}
	}
	m.HomeX, m.HomeZ = centre(16, 32)
	ex, ez := centre(112, 32)
	m.Starts = [][2]int32{{m.HomeX, m.HomeZ}, {ex, ez}}
	for _, c := range [][2]int32{{24, 50}, {104, 50}} {
		x, z := centre(c[0], c[1])
		m.Spots = append(m.Spots, MetalSpot{CellX: c[0] - 1, CellZ: c[1] - 1, X: x, Z: z, Metal: 400})
	}
	return m
}

var landClass = MoveClass{MinDepth: -10000, MaxDepth: 12, MaxSlope: 15, MaxWaterSlope: 255, FootX: 2, FootZ: 2}

// The choke contract the defense plan relies on: the ramp into our side
// is found, it guards our start and our spot (not the enemy's), and every
// site stands on the guarded ground, beside the ramp's mouth on our side,
// outside the passage test — never in the ramp.
func TestChokesRamp(t *testing.T) {
	m := ridge(false)
	cm := m.Chokes(landClass, []MoveClass{landClass})
	if len(cm.Chokes) != 1 {
		t.Fatalf("chokes = %+v, want one (the ramp)", cm.Chokes)
	}
	c := cm.Chokes[0]
	if !c.Home || c.Spots != 1 {
		t.Errorf("ramp guards start %v and %d spots, want our start and our one spot", c.Home, c.Spots)
	}
	if cx, cz := c.X/16, c.Z/16; cx < 36 || cx > 60 || cz < 26 || cz > 37 {
		t.Errorf("choke at cell (%d,%d), want at the ramp's west end", cx, cz)
	}
	if g := cm.Guards(m.HomeX, m.HomeZ); g != 1 {
		t.Errorf("our start's guard mask %b, want the ramp", g)
	}
	if g := cm.Guards(m.Starts[1][0], m.Starts[1][1]); g != 0 {
		t.Errorf("the enemy start is guarded (%b)", g)
	}
	if c.NSites == 0 {
		t.Fatal("no site beside the ramp")
	}
	for i := int32(0); i < c.NSites; i++ {
		x, z := c.Sites[i][0], c.Sites[i][1]
		if x/16 >= 40 {
			t.Errorf("site (%d,%d) is not on our side of the cliff", x, z)
		}
		if cm.Guards(x, z) != 1 {
			t.Errorf("site (%d,%d) is not on the guarded ground", x, z)
		}
		if m.InPassage(&landClass, x/16-1, z/16-1, 3, 3) {
			t.Errorf("site (%d,%d) stands in the passage", x, z)
		}
		if d := Dist(x, z, c.X, c.Z); d > chokeSiteR*16*chokeCell+64 {
			t.Errorf("site (%d,%d) is %d wu from the choke", x, z, d)
		}
		for j := int32(0); j < i; j++ {
			if Dist2(x, z, c.Sites[j][0], c.Sites[j][1]) < chokeSiteGap*chokeSiteGap {
				t.Errorf("sites %d and %d are closer than a tower gap", j, i)
			}
		}
	}
}

// A base with two ways in is guarded by both together: neither ramp alone
// closes it off, so the analysis tries them jointly.
func TestChokesTwoRamps(t *testing.T) {
	m := ridge(true)
	cm := m.Chokes(landClass, []MoveClass{landClass})
	if len(cm.Chokes) != 2 {
		t.Fatalf("chokes = %+v, want the two ramps", cm.Chokes)
	}
	rows := [2]bool{}
	for _, c := range cm.Chokes {
		if !c.Home {
			t.Errorf("choke at (%d,%d) does not guard our start", c.X, c.Z)
		}
		rows[0] = rows[0] || c.Z/16 < 20
		rows[1] = rows[1] || c.Z/16 >= 24
	}
	if !rows[0] || !rows[1] {
		t.Errorf("chokes %+v are not one per ramp", cm.Chokes)
	}
	if g := cm.Guards(m.HomeX, m.HomeZ); g != 3 {
		t.Errorf("our start's guard mask %b, want both ramps", g)
	}
}

// Open ground has no chokes, and a start with no land route to an enemy
// has none either.
func TestChokesNone(t *testing.T) {
	m := ridge(false)
	for i := range m.cellLo {
		m.cellLo[i], m.cellHi[i] = 50, 50
	}
	if cm := m.Chokes(landClass, []MoveClass{landClass}); len(cm.Chokes) != 0 {
		t.Errorf("open map: chokes %+v", cm.Chokes)
	}
	m = ridge(false)
	for z := int32(0); z < m.CellH; z++ {
		for x := int32(40); x < 88; x++ {
			m.cellLo[z*m.CellW+x], m.cellHi[z*m.CellW+x] = 0, 100
		}
	}
	if cm := m.Chokes(landClass, []MoveClass{landClass}); len(cm.Chokes) != 0 {
		t.Errorf("no land route: chokes %+v", cm.Chokes)
	}
}

// A new-row search checks each candidate against the buildings its bucket
// lists (bucketRows) rather than every gathered building; the answers must
// be rowRules's own for every candidate, footprint and building group.
func TestRowBucketsMatchAllBuildings(t *testing.T) {
	rnd := testRand(3)
	for trial := 0; trial < 6; trial++ {
		rs := &rowState{}
		for i := 0; i < 90; i++ {
			fx, fz := 2+rnd(5), 2+rnd(5)
			x, z := rnd(200)-100, rnd(200)-100
			rs.blds = append(rs.blds, rowBld{x0: x, z0: z, x1: x + fx, z1: z + fz, g: rowGroup(rnd(5)), blk: -1})
		}
		rs.groupBlocks()
		fx, fz := 1+rnd(6), 1+rnd(6)
		cx0, cz0 := rnd(40)-20, rnd(40)-20
		rs.bucketRows(cx0-rowSearchR, cz0-rowSearchR, fx, fz)
		for j := -int32(rowSearchR); j <= rowSearchR; j++ {
			for i := -int32(rowSearchR); i <= rowSearchR; i++ {
				c := rowBld{x0: cx0 + i, z0: cz0 + j, x1: cx0 + i + fx, z1: cz0 + j + fz, g: rowGroup((i + j) & 3)}
				ok, joins, deep := rs.rowRules(&c)
				b := ((j+rowSearchR)/rowBucket)*rowBuckets + (i+rowSearchR)/rowBucket
				ok2, joins2, deep2 := rs.rowRulesAmong(&c, rs.near[rs.nearAt[b]:rs.nearAt[b+1]], true)
				if ok != ok2 || joins != joins2 || deep != deep2 {
					t.Fatalf("trial %d candidate (%d, %d) %dx%d: bucket answer %v %v %v, want %v %v %v", trial, c.x0, c.z0, fx, fz, ok2, joins2, deep2, ok, joins, deep)
				}
			}
		}
	}
}

// A row is extended only from buildings within the row-extension radius of
// the request point: at the default (700 wu) a row 500 wu away absorbs the
// building; at a brain's 250 wu (Kit.SetRowNear) it does not, and the
// executor starts a new row at the point instead. The setting outlives
// the batch it was set in and reaches the executor at apply.
func TestRowNearRadius(t *testing.T) {
	rs := &rowState{}
	// A two-solar row with its centre about 500 wu east of the request
	// point (cells of 16 wu).
	for _, x := range []int32{40, 45} {
		rs.blds = append(rs.blds, rowBld{x0: x, z0: 10, x1: x + 5, z1: 15, g: grpEnergy, blk: -1})
	}
	rs.groupBlocks()
	px, pz := int32(40*16-500), int32(12*16)
	if rs.extendCands(grpEnergy, 5, 5, px, pz, rowNear); len(rs.cands) == 0 {
		t.Fatal("default radius: the row 500 wu away is not extended")
	}
	if rs.extendCands(grpEnergy, 5, 5, px, pz, 250); len(rs.cands) != 0 {
		t.Errorf("250 wu radius: %d extension anchors, want none", len(rs.cands))
	}
	if rs.extendCands(grpEnergy, 5, 5, 40*16-100, pz, 250); len(rs.cands) == 0 {
		t.Error("250 wu radius: the row 100 wu away is not extended")
	}
	b := &batch{}
	k := &Kit{out: b}
	k.SetRowNear(250)
	b.reset()
	e := &executor{m: &ai.Manager{}}
	e.apply(b, 1, nil, &Persona{})
	if e.rowNear != 250 {
		t.Errorf("executor row radius %d after a reset batch, want 250", e.rowNear)
	}
	(&Kit{}).SetRowNear(250) // no output (Init): dropped, no panic
}

// A factory avoids facing a metal spot: its kept front lane (and a cell
// around it) covering one fails the first search (laneSpot); and under the
// layout rules no extractor stands in an own factory's lane (inOwnLane),
// standing or accepted, since it would wall the pad in.
func TestFactoryLaneSpots(t *testing.T) {
	m := &MapInfo{CellW: 64, CellH: 64, FootX: 3, FootZ: 3}
	m.Spots = []MetalSpot{{CellX: 20, CellZ: 30}}
	e := &executor{mapInfo: m}
	// Factory at cells x 16..23, z 20..25: its lane is z 26..37, x 15..24.
	if !e.laneSpot(16, 20, 8, 6) {
		t.Error("a factory lane over a metal spot is not seen")
	}
	if e.laneSpot(30, 20, 8, 6) {
		t.Error("a factory lane clear of the spot is refused")
	}
	fac := &UnitInfo{Role: RoleFactory, FootX: 8, FootZ: 6}
	e.obs = &Obs{Own: []OwnUnit{{Info: fac, X: 16*16 + 64, Z: 20*16 + 48}}}
	if !e.inOwnLane(20, 30, 3, 3, 1) || e.inOwnLane(20, 40, 3, 3, 1) {
		t.Error("standing factory: extractor in its lane not seen, or one beyond it refused")
	}
	e.obs = nil
	e.pending[0] = pendingSite{cx: 16, cz: 20, fx: 8, fz: 6, g: grpFactory, tick: 1}
	if !e.inOwnLane(20, 30, 3, 3, 2) || e.inOwnLane(20, 30, 3, 3, 1+pendingTTL) {
		t.Error("accepted factory site: its lane is not kept while pending, or kept after")
	}
}
