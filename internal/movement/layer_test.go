package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/world"
)

// layerTerrain builds a WxH terrain with per-cell height and derived 2×2
// heights settable per test [04 §6.1]. Derived values are written directly
// (map load derives them from the height bytes; the classifier reads the
// derived pair [fmt tnt][04 §6.1 R-DOC04-B]). Default height 30 with sea 20
// makes the flat map land; water cells are authored per test.
func layerTerrain(w, h int32, sea uint8) *world.Terrain {
	t := &world.Terrain{
		CellW:    w,
		CellH:    h,
		SeaLevel: sea,
		Plot:     make([]world.PlotCell, w*h),
	}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
		t.Plot[i].SetHeight(30)
		t.Plot[i].SetMinHeight(30)
		t.Plot[i].SetMaxHeight(30)
	}
	return t
}

// setDerived writes the height byte and the derived 2×2 pair of one cell.
func setDerived(t *world.Terrain, x, z int32, hmin, hmax uint8) {
	c := t.PlotAt(x, z)
	c.SetHeight(hmin)
	c.SetMinHeight(hmin)
	c.SetMaxHeight(hmax)
}

// setFeatureIndex writes a real feature-table index into one cell and binds
// the table entry.
func setFeatureIndex(t *world.Terrain, x, z int32, idx uint16, def *content.FeatureDef) {
	t.PlotAt(x, z).SetFeature(idx)
	for len(t.FeatureDefs) <= int(idx) {
		t.FeatureDefs = append(t.FeatureDefs, nil)
	}
	t.FeatureDefs[idx] = def
}

// Stock compiled classes from the research's table [04 §6.1 R-DOC04-A]:
// template priors carry omitted keys, so compiled values equal authored ones.
var (
	kbotsSS2 = Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 32, BadSlope: 16, MaxWaterSlope: 255, BadWaterSlope: 127}
	tankDS2  = Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 100, MinWaterDepth: -10000, MaxSlope: 32, BadSlope: 16, MaxWaterSlope: 255, BadWaterSlope: 127}
	spid3    = Profile{FootPrintX: 3, FootPrintZ: 3, MaxWaterDepth: 16, MinWaterDepth: -10000, MaxSlope: 255, BadSlope: 127, MaxWaterSlope: 255, BadWaterSlope: 127}
	boats4   = Profile{FootPrintX: 4, FootPrintZ: 4, MaxWaterDepth: 10000, MinWaterDepth: 3, MaxSlope: 255, BadSlope: 127, MaxWaterSlope: 255, BadWaterSlope: 127}
)

// TestLayerClassifierChain locks the six-step chain of [04 §6.1 R-DOC04-B]
// on the stock compiled classes, including the depth expectations for
// KBOTSS2/TANKDS2 and the spider MaxSlope 255 example.
func TestLayerClassifierChain(t *testing.T) {
	// Flat land (hmin=hmax=30) above sea 20: land, slope 0 → clear.
	tr := layerTerrain(8, 8, 20)
	l := NewClassLayer(kbotsSS2, tr, nil)
	if got := l.classifyRect(3, 3, 3, 3); got != LayerClear {
		t.Fatalf("flat land want clear(3) got %d", got)
	}

	// Deep gate, KBOTSS2 MaxWaterDepth 12: sea 20, submerged cell hmin=hmax=0.
	// Depth 20 > 12: hmin(0) < 20−12=8 → blocked [R-DOC04-B stock: land
	// classes block water deeper than authored MaxWaterDepth (KBOTSS2 12)].
	setDerived(tr, 1, 1, 0, 0)
	if got := l.classifyRect(1, 1, 1, 1); got != LayerBlocked {
		t.Fatalf("KBOTSS2 deep water want blocked got %d", got)
	}
	// TANKDS2 MaxWaterDepth 100 passes the same depth [R-DOC04-B stock].
	lt := NewClassLayer(tankDS2, tr, nil)
	if got := lt.classifyRect(1, 1, 1, 1); got != LayerClear {
		t.Fatalf("TANKDS2 deep water want clear got %d", got)
	}
	// Depth exactly at the limit passes (hmin == sea − mwd).
	setDerived(tr, 2, 1, 8, 8)
	if got := l.classifyRect(2, 1, 2, 1); got != LayerClear {
		t.Fatalf("depth == limit must pass, got %d", got)
	}

	// Shallow gate, boats MinWaterDepth 3 [R-DOC04-B stock: boats block
	// unless the whole footprint is submerged past authored MinWaterDepth].
	lb := NewClassLayer(boats4, tr, nil)
	// hmax 18 above the shallow ceiling sea−minwd = 17 → blocked.
	setDerived(tr, 3, 3, 10, 18)
	if got := lb.classifyRect(3, 3, 3, 3); got != LayerBlocked {
		t.Fatalf("boat hmax above shallow ceiling want blocked got %d", got)
	}
	// hmax exactly at the ceiling (17) passes.
	setDerived(tr, 3, 3, 10, 17)
	if got := lb.classifyRect(3, 3, 3, 3); got != LayerClear {
		t.Fatalf("boat hmax == ceiling must pass got %d", got)
	}

	/// Medium split: water branch selects the water slope pair. KBOTSS2 in
	// knee-deep water (hmin 8 >= sea−mwd 8) with slope 200: bws 127 → not
	// clear; mws 255 → not blocked → steep. The same slope on land (ms 32)
	// blocks.
	tr2 := layerTerrain(8, 8, 20)
	lw := NewClassLayer(kbotsSS2, tr2, nil)
	setDerived(tr2, 4, 4, 8, 208) // hmin 8 < sea 20 → water; slope 200
	if got := lw.classifyRect(4, 4, 4, 4); got != LayerSteep {
		t.Fatalf("water slope 200 want steep(1) got %d", got)
	}
	setDerived(tr2, 4, 4, 20, 220) // hmin 20 >= sea → land; slope 200 > 32
	if got := lw.classifyRect(4, 4, 4, 4); got != LayerBlocked {
		t.Fatalf("land slope 200 want blocked got %d", got)
	}

	// Slope tier on land, KBOTSS2 bad 16 / max 32 [R-DOC04-B: equality with
	// the bad threshold is clear; equality with the max threshold is steep].
	tr3 := layerTerrain(8, 8, 20)
	l3 := NewClassLayer(kbotsSS2, tr3, nil)
	setDerived(tr3, 5, 1, 20, 36) // slope 16 == bad → clear
	if got := l3.classifyRect(5, 1, 5, 1); got != LayerClear {
		t.Fatalf("slope == bad want clear(3) got %d", got)
	}
	setDerived(tr3, 5, 2, 20, 37) // slope 17 → steep
	if got := l3.classifyRect(5, 2, 5, 2); got != LayerSteep {
		t.Fatalf("slope bad+1 want steep(1) got %d", got)
	}
	setDerived(tr3, 5, 3, 20, 52) // slope 32 == max → steep, not blocked
	if got := l3.classifyRect(5, 3, 5, 3); got != LayerSteep {
		t.Fatalf("slope == max want steep(1) got %d", got)
	}
	setDerived(tr3, 5, 4, 20, 53) // slope 33 > max → blocked
	if got := l3.classifyRect(5, 4, 5, 4); got != LayerBlocked {
		t.Fatalf("slope > max want blocked(0) got %d", got)
	}

	// Spider MaxSlope 255 climbs any slope [R-DOC04-B stock examples]:
	// sea 0 terrain, full byte-range slope 255 on land → steep, passable.
	tr4 := layerTerrain(8, 8, 0)
	l4 := NewClassLayer(spid3, tr4, nil)
	setDerived(tr4, 2, 2, 0, 255)
	if got := l4.classifyRect(2, 2, 2, 2); got != LayerSteep {
		t.Fatalf("spider slope 255 want steep(1), passable, got %d", got)
	}

	// Feature gate: resolved blocking feature blocks; non-blocking passes;
	// void blocks [04 §6.1 R-DOC04-B step 1][GAP T14].
	tr5 := layerTerrain(8, 8, 20)
	l5 := NewClassLayer(kbotsSS2, tr5, nil)
	setFeatureIndex(tr5, 6, 6, 0, &content.FeatureDef{Blocking: true})
	if got := l5.classifyRect(6, 6, 6, 6); got != LayerBlocked {
		t.Fatalf("blocking feature want blocked got %d", got)
	}
	setFeatureIndex(tr5, 6, 6, 0, &content.FeatureDef{Blocking: false})
	if got := l5.classifyRect(6, 6, 6, 6); got != LayerClear {
		t.Fatalf("non-blocking feature want clear got %d", got)
	}
	tr5.PlotAt(7, 7).SetFeature(world.PlotFeatureVoid)
	if got := l5.classifyRect(7, 7, 7, 7); got != LayerBlocked {
		t.Fatalf("void cell want blocked got %d", got)
	}
}

// TestLayerOccupantAgeGate locks classifier step 2 [04 §6.1 R-DOC04-B]: the
// cell's mobile occupant blocks iff its last occupancy-commit tick predates
// the revision watermark; the watermark zero (map load) never blocks.
func TestLayerOccupantAgeGate(t *testing.T) {
	tr := layerTerrain(8, 8, 20)
	grid := NewOccupancyGrid()
	l := NewClassLayer(kbotsSS2, tr, grid)
	// Occupant present at map-load stamp time (watermark 0): never blocks.
	grid.Stamp(Cell{X: 3, Z: 3}, 1, 1, 7)
	l.NoteCommit(7, 5)
	l.stampAll()
	if got := l.Value(3, 3); got != LayerClear {
		t.Fatalf("watermark 0 must never block occupants, got %d", got)
	}
	// Watermark armed to 20: commit tick 5 predates it → blocked.
	l.watermark = 20
	if got := l.classify(3, 3); got != LayerBlocked {
		t.Fatalf("stale occupant want blocked got %d", got)
	}
	// Commit tick inside the window passes the gate.
	l.commits[7] = 25
	if got := l.classify(3, 3); got != LayerClear {
		t.Fatalf("fresh occupant want clear got %d", got)
	}
}

// TestLayerValueTwoNeverStamped scans a stamped layer after map stamp and
// rectangle restamps: value 2 never occurs [04 §6.1 R-DOC04-B].
func TestLayerValueTwoNeverStamped(t *testing.T) {
	tr := layerTerrain(40, 40, 20)
	grid := NewOccupancyGrid()
	l := NewClassLayer(kbotsSS2, tr, grid)
	// Complicate the terrain and restamp a rectangle.
	setDerived(tr, 10, 10, 0, 200)
	grid.Stamp(Cell{X: 20, Z: 20}, 1, 1, 3)
	l.NoteCommit(3, 1)
	l.watermark = 30
	l.RestampRect(18, 18, 22, 22)
	for z := int32(0); z < l.H; z++ {
		for x := int32(0); x < l.W; x++ {
			if v := l.Value(x, z); v == 2 {
				t.Fatalf("value 2 stamped at (%d,%d) [04 §6.1 R-DOC04-B]", x, z)
			}
		}
	}
}

// TestLayerPacking locks the packed layout [04 §6.1]: dword index
// (z>>4)·width + x, cell z in shift (z&15)·2.
func TestLayerPacking(t *testing.T) {
	tr := layerTerrain(40, 40, 20)
	l := NewClassLayer(kbotsSS2, tr, nil)
	l.setValue(5, 17, LayerSteep)
	if got := uint8((l.cells[(17>>4)*40+5] >> (uint(17&15) * 2)) & 3); got != LayerSteep {
		t.Fatalf("packing: raw dword read want 1 got %d", got)
	}
	if got := l.Value(5, 17); got != LayerSteep {
		t.Fatalf("Value want 1 got %d", got)
	}
	// Neighbouring cells in the same dword are untouched.
	if got := l.Value(5, 16); got != LayerClear {
		t.Fatalf("neighbour cell disturbed: want 3 got %d", got)
	}
	if got := l.Value(5, 18); got != LayerClear {
		t.Fatalf("neighbour cell disturbed: want 3 got %d", got)
	}
	// OOB reads blocked.
	if got := l.Value(-1, 0); got != LayerBlocked {
		t.Fatalf("OOB want 0 got %d", got)
	}
	if got := l.Value(40, 0); got != LayerBlocked {
		t.Fatalf("OOB want 0 got %d", got)
	}
}

func TestLayerFootprintAggregateAndRing(t *testing.T) {
	tr := layerTerrain(12, 12, 20)
	l := NewClassLayer(kbotsSS2, tr, nil)
	setDerived(tr, 4, 4, 0, 0)
	if got := l.classify(3, 3); got != LayerBlocked {
		t.Fatalf("blocked cell inside 2x2 footprint want blocked got %d", got)
	}
	setDerived(tr, 4, 4, 30, 30)
	setDerived(tr, 2, 3, 20, 37)
	if got := l.classify(3, 3); got != LayerSteep {
		t.Fatalf("non-clear outside ring must demote clear footprint, got %d", got)
	}
}

// TestRevisionWatermarkArithmetic locks max(tick, 31) − 30
// [04 R-PATH-01 §2].
func TestRevisionWatermarkArithmetic(t *testing.T) {
	cases := []struct {
		tick, want uint32
	}{
		{0, 1}, {1, 1}, {29, 1}, {30, 1}, {31, 1}, {32, 2}, {45, 15}, {60, 30}, {61, 31},
	}
	for _, c := range cases {
		if got := revisionWatermark(c.tick); got != c.want {
			t.Fatalf("revisionWatermark(%d) want %d got %d", c.tick, c.want, got)
		}
	}
}

// testAnchors is a deterministic committed-footprint source for tests.
type testFootprint struct {
	anchor Cell
	fx, fz int16
}

type testAnchors map[pool.Handle]testFootprint

func (a testAnchors) CommittedFootprint(h pool.Handle) (Cell, int16, int16, bool) {
	f, ok := a[h]
	return f.anchor, f.fx, f.fz, ok
}

// TestLayerRevisionPass locks the crossed-window cohort and temporary
// requester commit of [04 R-MOV-03 §3].
func TestLayerRevisionPass(t *testing.T) {
	tr := layerTerrain(32, 32, 20)
	grid := NewOccupancyGrid()
	w := newMovementFixtureWorld(16)
	def := &content.UnitDef{UnitName: "armflea", MaxDamage: 100, CanMove: true, BMCode: true}
	hReq, err := w.Create(def, 0, world.CellToWorld(2), 30*65536, world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	hFresh, err := w.Create(def, 0, world.CellToWorld(10), 30*65536, world.CellToWorld(10))
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	hStale, err := w.Create(def, 0, world.CellToWorld(20), 30*65536, world.CellToWorld(20))
	if err != nil {
		t.Fatalf("create stale: %v", err)
	}
	anchors := testAnchors{
		hReq:   {anchor: Cell{X: 2, Z: 2}, fx: 1, fz: 1},
		hFresh: {anchor: Cell{X: 10, Z: 10}, fx: 1, fz: 1},
		hStale: {anchor: Cell{X: 20, Z: 20}, fx: 2, fz: 1},
	}
	l := NewClassLayer(kbotsSS2, tr, grid)
	l.NoteCommit(hReq, 50)
	l.NoteCommit(hFresh, 45) // outside the crossed window [0,30)
	l.NoteCommit(hStale, 5)  // inside the crossed window [0,30)
	// A dead unit is skipped even when otherwise eligible.
	fresh := w.Unit(hFresh)
	fresh.Alive = false
	defer func() { fresh.Alive = true }()
	// Corrupt both cohort footprints so the restamp choice is observable.
	for z := int32(10); z < 12; z++ {
		for x := int32(10); x < 12; x++ {
			l.setValue(x, z, LayerBlocked)
		}
	}
	grid.Stamp(Cell{X: 20, Z: 20}, 2, 1, int(hStale))
	l.setValue(20, 20, LayerSteep)
	l.setValue(21, 20, LayerSteep)

	l.Revise(60, hReq, w, anchors)

	if l.Watermark() != 30 {
		t.Fatalf("watermark want 30 got %d", l.Watermark())
	}
	if c, _ := l.CommitTick(hReq); c != 50 {
		t.Fatalf("requester commit tick was not restored: want 50 got %d", c)
	}
	// Outside-window/dead unit: not re-stamped (corruption survives).
	if got := l.Value(10, 10); got != LayerBlocked {
		t.Fatalf("dead unit must not re-stamp, got %d", got)
	}
	// The [0,30) cohort is re-stamped after watermark 30, so its frozen
	// occupancy hard-blocks its anchor.
	if got := l.Value(20, 20); got != LayerBlocked {
		t.Fatalf("crossed-window stale occupant must re-stamp blocked, got %d", got)
	}
	if got := l.Value(21, 20); got != LayerBlocked {
		t.Fatalf("restamp omitted the occupant's second footprint cell, got %d", got)
	}

	// A requester older than the old watermark is re-stamped with its commit
	// temporarily set to the current tick, then receives its real tick back.
	l.NoteCommit(hReq, 5)
	grid.Stamp(Cell{X: 2, Z: 2}, 1, 1, int(hReq))
	l.setValue(2, 2, LayerBlocked)
	l.Revise(61, hReq, w, anchors)
	if c, _ := l.CommitTick(hReq); c != 5 {
		t.Fatalf("requester old commit was not restored: want 5 got %d", c)
	}
	if got := l.Value(2, 2); got == LayerBlocked {
		t.Fatalf("requester blocked itself during temporary-commit restamp")
	}
}

// TestLayerRevisionRestampsEveryOverlappingRequesterAnchor locks the exact
// footprint- and ring-aware invalidation rectangle. For a 3x2 requester class
// and a 2x3 occupant at (8,9), overlapping anchors x=6..9, z=8..11 block;
// the ring-only border x=5/10 and z=7/12 is rewritten steep; the next border
// remains untouched
// [04 R-PATH-01 §2][04 R-MOV-03 §3][fmt tdf][fmt fbi].
func TestLayerRevisionRestampsEveryOverlappingRequesterAnchor(t *testing.T) {
	tr := layerTerrain(24, 24, 20)
	grid := NewOccupancyGrid()
	w := newMovementFixtureWorld(8)
	def := &content.UnitDef{UnitName: "asymmetric-blocker", MaxDamage: 100, BMCode: false}
	h, err := w.Create(def, 1, world.CellToWorld(8), 30*65536, world.CellToWorld(9))
	if err != nil {
		t.Fatalf("create occupant: %v", err)
	}
	if int(h) <= w.Capacity()/pool.PlayerCount {
		t.Fatalf("fixture occupant %d must lie beyond the first player slice %d", h, w.Capacity()/pool.PlayerCount)
	}
	last := pool.Handle(w.TotalRecords() - 1)
	hLast, err := w.CreateWithForcedSlot(def, 9, world.CellToWorld(16), 30*65536, world.CellToWorld(16), last)
	if err != nil {
		t.Fatalf("create final-slot occupant: %v", err)
	}
	if hLast != last {
		t.Fatalf("final-slot occupant = %d, want %d", hLast, last)
	}
	anchors := testAnchors{
		h:     {anchor: Cell{X: 8, Z: 9}, fx: 2, fz: 3},
		hLast: {anchor: Cell{X: 16, Z: 16}, fx: 1, fz: 1},
	}
	profile := tankDS2
	profile.FootPrintX = 3
	profile.FootPrintZ = 2
	l := NewClassLayer(profile, tr, grid)
	if !grid.Stamp(Cell{X: 8, Z: 9}, 2, 3, int(h)) {
		t.Fatal("occupant stamp failed")
	}
	if !grid.Stamp(Cell{X: 16, Z: 16}, 1, 1, int(hLast)) {
		t.Fatal("final-slot occupant stamp failed")
	}
	l.NoteCommit(h, 5)
	l.NoteCommit(hLast, 5)

	// Sentinels immediately beyond all four exact bounds expose an off-by-one
	// expansion: a correct restamp leaves them untouched.
	outside := []Cell{{X: 4, Z: 9}, {X: 11, Z: 9}, {X: 8, Z: 6}, {X: 8, Z: 13}}
	for _, cell := range outside {
		l.setValue(cell.X, cell.Z, LayerBlocked)
	}

	l.Revise(60, 0, w, anchors)

	for z := int32(8); z <= 11; z++ {
		for x := int32(6); x <= 9; x++ {
			if got := l.Value(x, z); got != LayerBlocked {
				t.Fatalf("overlapping requester anchor (%d,%d) = %d, want blocked", x, z, got)
			}
		}
	}
	ringOnly := []Cell{{X: 5, Z: 9}, {X: 10, Z: 9}, {X: 8, Z: 7}, {X: 8, Z: 12}}
	for _, cell := range ringOnly {
		if got := l.Value(cell.X, cell.Z); got != LayerSteep {
			t.Fatalf("ring-only anchor %v = %d, want steep", cell, got)
		}
	}
	for _, cell := range outside {
		if got := l.Value(cell.X, cell.Z); got != LayerBlocked {
			t.Fatalf("non-overlapping anchor %v was rewritten to %d", cell, got)
		}
	}
	if got := l.Value(14, 15); got != LayerBlocked {
		t.Fatalf("last physical slot's overlapping anchor = %d, want blocked", got)
	}
}

func TestLayerRevisionCommitAtWatermarkRemainsNonblocking(t *testing.T) {
	tr := layerTerrain(24, 24, 20)
	grid := NewOccupancyGrid()
	w := newMovementFixtureWorld(8)
	def := &content.UnitDef{UnitName: "watermark-blocker", MaxDamage: 100, BMCode: false}
	h, err := w.Create(def, 0, world.CellToWorld(8), 30*65536, world.CellToWorld(9))
	if err != nil {
		t.Fatalf("create occupant: %v", err)
	}
	anchors := testAnchors{h: {anchor: Cell{X: 8, Z: 9}, fx: 2, fz: 3}}
	profile := tankDS2
	profile.FootPrintX = 3
	profile.FootPrintZ = 2
	l := NewClassLayer(profile, tr, grid)
	if !grid.Stamp(Cell{X: 8, Z: 9}, 2, 3, int(h)) {
		t.Fatal("occupant stamp failed")
	}
	l.NoteCommit(h, 30)

	// The strict [old,new) cohort excludes equality at watermark 30.
	l.Revise(60, 0, w, anchors)
	if got := l.Value(6, 8); got != LayerClear {
		t.Fatalf("commit equal to watermark blocked overlapping anchor: got %d", got)
	}
	// Advancing one tick crosses commit 30 and restamps the overlap rectangle.
	l.Revise(61, 0, w, anchors)
	if got := l.Value(6, 8); got != LayerBlocked {
		t.Fatalf("commit after strict watermark crossing = %d, want blocked", got)
	}
}

// TestClassLayersSharedPerClass locks record+layer sharing [04 §6.1
// R-DOC04-B]: all requests of one class share one record and layer; distinct
// classes own distinct layers.
func TestClassLayersSharedPerClass(t *testing.T) {
	tr := layerTerrain(16, 16, 20)
	c := NewClassLayers(tr, nil, nil, nil)
	a := c.For("kbots2", kbotsSS2)
	b := c.For("kbots2", kbotsSS2)
	if a != b {
		t.Fatalf("same class must share one layer")
	}
	o := c.For("tank2", tankDS2)
	if o == a {
		t.Fatalf("distinct classes must own distinct layers")
	}
	// A revision on the shared layer is observed by the next request.
	c.ReviseFor("kbots2", kbotsSS2, 5, 90)
	if a.Watermark() != 60 {
		t.Fatalf("shared watermark want 60 got %d", a.Watermark())
	}
	if b.Watermark() != 60 {
		t.Fatalf("shared watermark visible through second binding, got %d", b.Watermark())
	}
	if got := c.Names(); len(got) != 2 || got[0] != "kbots2" || got[1] != "tank2" {
		t.Fatalf("allocation order want [kbots2 tank2] got %v", got)
	}
}

// TestLayerOwnerMask locks the owner/building-mask overlay [04 §6.1
// R-DOC04-B]: OOB → 0; requester's bit absent → 2; present → packed terrain
// value. Only 0 hard-blocks: 2 expands. The foot>>2 terms shift the tested
// block toward the footprint's centre half.
func TestLayerOwnerMask(t *testing.T) {
	tr := layerTerrain(32, 32, 20)
	l := NewClassLayer(kbotsSS2, tr, nil)
	// No bits set: every in-bounds cell is a mask miss → 2 (traversable).
	if got := l.Passable(5, 5, 2, 2, 0); got != LayerMaskMiss {
		t.Fatalf("bit miss want 2 got %d", got)
	}
	if got := l.Passable(-1, 5, 2, 2, 0); got != LayerBlocked {
		t.Fatalf("OOB want 0 got %d", got)
	}
	// Set player 0's bits over a 2×2 building at (8,8): one 2×2-cell block.
	l.SetOwnerRect(Cell{X: 8, Z: 8}, 2, 2, 0)
	if got := l.Passable(8, 8, 2, 2, 0); got != LayerClear {
		t.Fatalf("owner bit present want terrain value 3 got %d", got)
	}
	if got := l.Passable(8, 8, 2, 2, 1); got != LayerMaskMiss {
		t.Fatalf("other player's bit absent want 2 got %d", got)
	}
	// footX>>2 shifts the tested block one east: (7>>1 + 1, 8>>1) = (4,4).
	if got := l.Passable(7, 8, 4, 2, 0); got != LayerClear {
		t.Fatalf("footX>>2 block shift want the east block's word, got %d", got)
	}
	// footZ>>2 shifts it one south: (8>>1, 7>>1 + 1) = (4,4).
	if got := l.Passable(8, 7, 2, 4, 0); got != LayerClear {
		t.Fatalf("footZ>>2 block shift want the south block's word, got %d", got)
	}
	l.ClearOwnerRect(Cell{X: 8, Z: 8}, 2, 2, 0)
	if got := l.Passable(8, 8, 2, 2, 0); got != LayerMaskMiss {
		t.Fatalf("cleared bit want 2 got %d", got)
	}
}

// TestLayerSearchConsumptionOnlyZeroBlocks drives the stamped layer through a
// path.Session with the value-form passability: steep (1), mask miss (2) and
// clear (3) all expand — only 0 hard-blocks [04 §6.1 R-DOC04-B]. The start is
// fully enclosed by a ring of the value under test, so success requires
// traversing it. Supported inference: the requester binding (player slot,
// footprint) into Passable is ours; the value contract and only-0-blocks rule
// are established.
func TestLayerSearchConsumptionOnlyZeroBlocks(t *testing.T) {
	tr := layerTerrain(24, 24, 20)
	l := NewClassLayer(kbotsSS2, tr, nil)
	l.SetOwnerRect(Cell{X: 0, Z: 0}, 24, 24, 0) // requester owns the map
	run := func(ring uint8) path.SearchResult {
		// Paint the 8-neighbour enclosure of the start cell.
		for z := int32(4); z <= 6; z++ {
			for x := int32(4); x <= 6; x++ {
				v := LayerClear
				if x != 5 || z != 5 {
					v = ring
				}
				l.setValue(x, z, v)
			}
		}
		cfg := path.SearchConfig{
			Start:     path.Cell{X: 5, Z: 5},
			Goal:      path.PointGoal(path.Cell{X: 20, Z: 20}, 0),
			Scale:     65536,
			HasBounds: true,
			Bounds:    path.Rect{Min: path.Cell{X: 0, Z: 0}, Max: path.Cell{X: 23, Z: 23}},
			PassableValue: func(c path.Cell) uint8 {
				return l.Passable(c.X, c.Z, 2, 2, 0)
			},
		}
		return path.Search(cfg)
	}
	for _, ring := range []uint8{LayerSteep, LayerMaskMiss, LayerClear} {
		res := run(ring)
		if res.Status != 0 || len(res.Points) == 0 {
			t.Fatalf("ring value %d must not block: status %d points %d", ring, res.Status, len(res.Points))
		}
	}
	res := run(LayerBlocked)
	if res.Status != path.StatusRejected {
		t.Fatalf("enclosure of 0 must yield no route, status %d", res.Status)
	}
}

// TestMapLoadStampNeverBlocksOnOccupants locks the map-load property
// [04 §6.1 R-DOC04-B]: with the watermark zero the static layer is terrain
// and features only, even with occupants committed.
func TestMapLoadStampNeverBlocksOnOccupants(t *testing.T) {
	tr := layerTerrain(16, 16, 20)
	grid := NewOccupancyGrid()
	grid.Stamp(Cell{X: 4, Z: 4}, 1, 1, 2)
	l := NewClassLayer(kbotsSS2, tr, grid)
	l.NoteCommit(2, 0) // ancient commit
	if got := l.Value(4, 4); got != LayerClear {
		t.Fatalf("map-load stamp must ignore occupants, got %d", got)
	}
}

// TestNewProfileDepthWordWidth locks the record's 16-bit signed depth store
// [02 §5] 3-4 [04 §6.1 R-DOC04-B]: the classifier reads depths sign-extended
// from 16-bit fields, so NewProfile narrows through the record width. The
// startup template values fit the width unchanged [04 §6.1 R-DOC04-A].
func TestNewProfileDepthWordWidth(t *testing.T) {
	tpl := Template()
	if tpl.MaxWaterDepth != 10000 || tpl.MinWaterDepth != -10000 {
		t.Fatalf("template depths want ±10000 [04 §6.1 R-DOC04-A]")
	}
	mc := &content.MovementClass{
		FootprintX:    2,
		FootprintZ:    2,
		MaxWaterDepth: 40000, // beyond int16: the record store wraps
		MinWaterDepth: -40000,
		MaxSlope:      32, BadSlope: 16, MaxWaterSlope: 255, BadWaterSlope: 127,
	}
	p := NewProfile(mc)
	// Constant conversions of an overflowing literal do not compile; the
	// record store truncates a runtime word, so narrow through a variable.
	tooBig := int32(40000)
	tooSmall := int32(-40000)
	if p.MaxWaterDepth != int32(int16(tooBig)) {
		t.Fatalf("MaxWaterDepth must narrow through the 16-bit field, got %d", p.MaxWaterDepth)
	}
	if p.MinWaterDepth != int32(int16(tooSmall)) {
		t.Fatalf("MinWaterDepth must narrow through the 16-bit field, got %d", p.MinWaterDepth)
	}
}
