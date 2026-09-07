package features

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// spreadTree is a resting sprite that can catch fire: flammable, with a
// resolvable burn sequence and a spread chance of 100 so a surviving
// candidate ignites on its draw.
func spreadTree(name string) *content.FeatureDef {
	tree := featureDef(name, 0, 0, 10)
	tree.Filename = "trees"
	tree.Flamable = true
	tree.SeqNameBurn = "burn"
	tree.SpreadChance = 100
	tree.SparkTime = 150
	return tree
}

// spreadService populates a 12x8 map through the real stamp — so every anchor,
// source and target alike, owns an Instance — with still air.
func spreadService(t *testing.T, seed uint32, defs ...*content.FeatureDef) *Service {
	t.Helper()
	terrain := newEmptyTerrain(12, 8)
	terrain.FeatureDefs = append(terrain.FeatureDefs, defs...)
	sim := rng.SimulationFromState(seed)
	wind := world.NewWind(0, 0)
	wind.DirX, wind.DirZ = 0, 0
	svc := NewService(terrain, &sim, nil, wind)
	stubSequences(svc, longBurn(), nil, nil)
	return svc
}

// TestFireSpreadReachesRestingNeighboursWithInstances is FL-02's gate: a
// resting sprite anchor stamped through the ordinary service carries a
// convenience Instance, and that record is NOT retail's "the cell has an
// instance" [05 R-FEAT-01 §11] — a sprite at rest owns no slot [05 R-FEAT-01
// §3 step 5]. The burn event must reach it: one draw, and with spreadchance
// 100 the neighbour ignites. Excluded from the draw, in the same pass: a
// neighbour already burning (it has a record), one playing its death
// animation, one that is not flammable, and a 3D definition (always attached).
func TestFireSpreadReachesRestingNeighboursWithInstances(t *testing.T) {
	tree := spreadTree("tree")
	dying := spreadTree("dyingtree")
	dying.SeqNameDie = "die"
	inert := featureDef("rock", 0, 0, 10)
	inert.Filename = "rocks"
	inert.Flamable = false
	wreck := defP1("wreck", 1, 1, "wreck.3do", "")
	wreck.Flamable = true
	svc := spreadService(t, 5, tree, dying, inert, wreck)
	stubSequences(svc, longBurn(), []int32{100}, nil)
	// Source at (5,3); candidates inside the 7x7 window: a resting tree at
	// (7,3), a burning tree at (5,5), a dying tree at (3,3), a rock at (6,4)
	// and a 3D wreck at (4,2).
	for _, p := range []struct {
		cx, cz int
		def    *content.FeatureDef
	}{{5, 3, tree}, {7, 3, tree}, {5, 5, tree}, {3, 3, dying}, {6, 4, inert}, {4, 2, wreck}} {
		if svc.spawnFeatureAt(p.cx, p.cz, p.def) == nil {
			t.Fatalf("spawn %s at (%d,%d) rejected", p.def.CanonicalKey, p.cx, p.cz)
		}
	}
	if svc.InstanceAt(7, 3) == nil {
		t.Fatal("the resting target has no Instance; the fixture must populate through the service")
	}
	startBurning(svc, svc.InstanceAt(5, 5), longBurn(), 1000)
	if !svc.transitionFeatureAt(3, 3, dying, false) {
		t.Fatal("death transition did not attach")
	}
	startBurning(svc, svc.InstanceAt(5, 3), longBurn(), 1)

	sim := svc.sim()
	before := sim.Draws()
	svc.TickLifecycle(1)
	// Exactly one candidate survives the cheap gates — the resting tree at
	// (7,3) — so the event draws once for it and its ignition draws once for
	// the countdown.
	if got := sim.Draws() - before; got != 2 {
		t.Fatalf("the burn event drew %d times, want 2 (one candidate, plus its ignition) [05 R-FEAT-01 §11]", got)
	}
	if got := svc.InstanceAt(7, 3); got == nil || !got.IsBurning {
		t.Fatal("the resting neighbour with an Instance did not ignite")
	}
	if got := svc.InstanceAt(3, 3); got == nil || got.IsBurning || !got.IsAnimating {
		t.Fatal("a neighbour playing its death animation was ignited")
	}
	if got := svc.InstanceAt(6, 4); got == nil || got.IsBurning {
		t.Fatal("a non-flammable neighbour was ignited")
	}
	if got := svc.InstanceAt(4, 2); got == nil || got.IsBurning {
		t.Fatal("a 3D neighbour was ignited")
	}
	// The event ran once: the countdown stays at zero and the next visit
	// draws nothing more.
	before = sim.Draws()
	svc.TickLifecycle(2)
	if got := sim.Draws() - before; got != 0 {
		t.Fatalf("the visit after the event drew %d times, want 0", got)
	}
}

// TestFireSpreadWindProbeReachesADistantAnchor exercises the wind pass on a
// populated map: with a wind of one tile per probe along +X, the five probes
// visit five distinct tiles east of the origin, and a resting anchor on the
// fifth — outside the 7x7 window — ignites with one draw of its own.
func TestFireSpreadWindProbeReachesADistantAnchor(t *testing.T) {
	tree := spreadTree("tree")
	svc := spreadService(t, 9, tree)
	// 2·wind per probe advances one tile: wind of half a tile in 16.16.
	svc.Wind.DirX = 1 << 15
	svc.Wind.DirZ = 0
	if svc.spawnFeatureAt(2, 3, tree) == nil || svc.spawnFeatureAt(7, 3, tree) == nil {
		t.Fatal("spawn rejected")
	}
	startBurning(svc, svc.InstanceAt(2, 3), longBurn(), 1)
	sim := svc.sim()
	before := sim.Draws()
	svc.TickLifecycle(1)
	// The neighbourhood pass finds nothing (7,3) is dx = +5, outside the
	// window; the wind pass's fifth probe lands on it: one spread draw and
	// one ignition draw.
	if got := sim.Draws() - before; got != 2 {
		t.Fatalf("the wind pass drew %d times, want 2 [05 R-FEAT-01 §11 step 2]", got)
	}
	if got := svc.InstanceAt(7, 3); got == nil || !got.IsBurning {
		t.Fatal("the distant anchor under the wind probe did not ignite")
	}
}

// TestActiveWalkIsLIFONotCellOrder locks the order of [05 R-FEAT-01 §10]
// pass 3: the most recently ignited record is visited first, whatever its
// cell index. Two burning trees whose burn events fall on the same tick spend
// the simulation stream in that order, so the record at the HIGHER cell
// index, ignited last, gets the first draws — and a candidate between them
// is claimed by it, not by the lower-indexed fire a cell-sorted walk would
// have run first.
func TestActiveWalkIsLIFONotCellOrder(t *testing.T) {
	terrain := newEmptyTerrain(12, 4)
	tree := featureDef("tree", 0, 0, 10)
	tree.Filename = "trees"
	tree.Flamable = true
	tree.SeqNameBurn = "burn"
	tree.SpreadChance = 100
	tree.SparkTime = 150
	terrain.FeatureDefs = []*content.FeatureDef{tree}
	sim := rng.SimulationFromState(77)
	svc := NewService(terrain, &sim, nil, nil)
	stubSequences(svc, longBurn(), nil, nil)
	// Three trees in a row; the middle one is the contested candidate.
	for _, cx := range []int{2, 4, 6} {
		if svc.spawnFeatureAt(cx, 1, tree) == nil {
			t.Fatalf("spawn at (%d,1) rejected", cx)
		}
	}
	// Ignite the LOWER cell first, then the higher: LIFO puts the higher at
	// the head.
	startBurning(svc, svc.InstanceAt(2, 1), longBurn(), 1)
	startBurning(svc, svc.InstanceAt(6, 1), longBurn(), 1)
	if got := activeOrder(svc); len(got) != 2 || got[0] != 1*12+6 || got[1] != 1*12+2 {
		t.Fatalf("active list %v, want the later ignition (6,1) at the head then (2,1) [05 R-FEAT-01 §10 pass 3]", got)
	}
	// Both countdowns reach zero on this visit. The head's event runs first:
	// its 7x7 window reaches (4,1) (dx = -2) and, with spreadchance 100, the
	// first surviving candidate ignites on the first draw. Ignition draws
	// once more for the new fire's countdown. Then the lower fire's event
	// finds (4,1) already attached and (6,1) attached, so it draws nothing.
	before := sim.Draws()
	svc.TickLifecycle(1)
	if got := sim.Draws() - before; got != 2 {
		t.Fatalf("the tick drew %d times, want 2: the head's spread draw for (4,1) plus that ignition's countdown draw", got)
	}
	mid := svc.InstanceAt(4, 1)
	if mid == nil || !mid.IsBurning {
		t.Fatal("the contested candidate did not ignite")
	}
	// The new fire went to the HEAD, behind the walk: it was not visited this
	// tick, so its countdown is untouched and the list now reads (4,1),
	// (6,1), (2,1).
	if got := activeOrder(svc); len(got) != 3 || got[0] != 1*12+4 {
		t.Fatalf("active list %v after the tick, want the fresh ignition at the head", got)
	}
	half := tree.SparkTime >> 1
	if mid.BurnCountdown < half || mid.BurnCountdown > 2*half-1 {
		t.Fatalf("the new head's countdown is %d, want an untouched 75..149 — a visit in the same walk would have decremented it", mid.BurnCountdown)
	}
}

// TestEarlierVisitCompletionIsSeenByLaterRecord locks the mutation timing:
// a die/reclaim record replaces itself AT its visit, so a fire visited later
// in the same walk scans the successor, not the finished animation — a
// deferred completion queue would have let the fire see (and skip) a cell
// that retail had already replaced.
func TestEarlierVisitCompletionIsSeenByLaterRecord(t *testing.T) {
	terrain := newEmptyTerrain(12, 4)
	tree := featureDef("tree", 0, 0, 10)
	tree.Filename = "trees"
	tree.Flamable = true
	tree.SeqNameBurn = "burn"
	tree.SeqNameDie = "die"
	tree.SpreadChance = 100
	tree.SparkTime = 150
	// The dying tree's successor is itself flammable with a resolvable burn
	// sequence: a fire that scans it after the replacement ignites it.
	stump := featureDef("stump", 0, 0, 10)
	stump.Filename = "trees"
	stump.Flamable = true
	stump.SeqNameBurn = "burn"
	stump.SpreadChance = 100
	stump.SparkTime = 150
	tree.FeatureDeadDef = stump
	terrain.FeatureDefs = []*content.FeatureDef{tree, stump}
	sim := rng.SimulationFromState(3)
	svc := NewService(terrain, &sim, nil, nil)
	stubSequences(svc, longBurn(), []int32{1}, nil)
	if svc.spawnFeatureAt(3, 1, tree) == nil || svc.spawnFeatureAt(5, 1, tree) == nil {
		t.Fatal("spawn rejected")
	}
	// The fire is attached first, the death second, so the walk visits the
	// death first. Its one-frame sequence ends on this visit.
	startBurning(svc, svc.InstanceAt(3, 1), longBurn(), 1)
	if !svc.transitionFeatureAt(5, 1, tree, false) {
		t.Fatal("death transition did not attach")
	}
	if got := activeOrder(svc); len(got) != 2 || got[0] != 1*12+5 {
		t.Fatalf("active list %v, want the death record at the head", got)
	}
	svc.TickLifecycle(1)
	got := svc.InstanceAt(5, 1)
	if got == nil || got.Def != stump {
		t.Fatalf("anchor (5,1) holds %v, want the stump stamped at the death's visit", got)
	}
	if !got.IsBurning {
		t.Fatal("the fire visited after the replacement did not ignite the successor it should have scanned")
	}
}

// TestIgnitionDuringTheWalkBecomesTheHead locks the insertion rule: a record
// ignited by a visit goes to the head of the list and is first visited on the
// NEXT walk, because the walk captured its next link before the visit.
func TestIgnitionDuringTheWalkBecomesTheHead(t *testing.T) {
	terrain := newEmptyTerrain(12, 4)
	tree := featureDef("tree", 0, 0, 10)
	tree.Filename = "trees"
	tree.Flamable = true
	tree.SeqNameBurn = "burn"
	tree.SpreadChance = 100
	tree.SparkTime = 2 // half = 1: a bound below two draws nothing and the countdown is 1
	terrain.FeatureDefs = []*content.FeatureDef{tree}
	sim := rng.SimulationFromState(11)
	svc := NewService(terrain, &sim, nil, nil)
	stubSequences(svc, longBurn(), nil, nil)
	if svc.spawnFeatureAt(3, 1, tree) == nil || svc.spawnFeatureAt(4, 1, tree) == nil {
		t.Fatal("spawn rejected")
	}
	startBurning(svc, svc.InstanceAt(3, 1), longBurn(), 1)
	svc.TickLifecycle(1)
	neighbour := svc.InstanceAt(4, 1)
	if neighbour == nil || !neighbour.IsBurning {
		t.Fatal("the neighbour did not ignite")
	}
	if got := activeOrder(svc); len(got) != 2 || got[0] != 1*12+4 || got[1] != 1*12+3 {
		t.Fatalf("active list %v, want the ignition at the head", got)
	}
	// Ignited with countdown 1 and NOT visited in the walk that ignited it.
	if neighbour.BurnCountdown != 1 {
		t.Fatalf("the new head's countdown is %d after the ignition's walk, want the untouched 1", neighbour.BurnCountdown)
	}
	// Its own event runs on the next walk, first.
	svc.TickLifecycle(2)
	if neighbour.BurnCountdown != 0 {
		t.Fatalf("the new head's countdown is %d after the next walk, want 0", neighbour.BurnCountdown)
	}
}
