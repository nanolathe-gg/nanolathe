package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// orderlessMoverDef is the smallest definition that carries a mover: `bmcode`
// set, a movement record, and the accel/brake/turn words the ground steering
// divides by [04 R-MOV-01 §4].
func orderlessMoverDef(name string) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
		UnitName:         name,
		BMCode:           1,
		CanMove:          true,
		MaxDamage:        100,
		FootprintX:       1,
		FootprintZ:       1,
		MaxVelocity:      2 * 65536,
		Acceleration:     65536,
		BrakeRate:        65536,
		TurnRate:         500,
		MinWaterDepth:    -10000,
		Upright:          true,
	}
}

func orderlessMoverSystem(t *testing.T, n int) (*System, *units.World) {
	t.Helper()
	ter := syntheticTerrainForIntegrate() // flat height 10, sea level 0
	sys := NewSystem(ter, Profile{
		FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12,
		MaxSlope: 255, BadSlope: 255, MaxWaterSlope: 255, BadWaterSlope: 255,
	}, NewOccupancyGrid())
	w := newMovementFixtureWorld(n)
	sys.BindWorld(w)
	return sys, w
}

// TestOrderlessGroundMoverRunsTheMoverTickAndPostMoveCorrection locks the
// contract of [04 R-MOV-03 §1] step 9: the sweep runs the mover tick and the
// post-move correction for every live unit that has a mover, whether or not it
// holds an order. WU-19-28 measured the opposite on this build — an orderless
// ground unit parked at Y = 99 over terrain 10 still read 99 after five ticks,
// because `StepUnit` returned at its empty-queue guard before the ground
// branch. With no route the follower has no waypoint, so it brakes without
// turning [04 R-MOV-01 §3], the commit runs, and the `upright`-without-
// `canhover` branch of [04 R-MOV-01 §5] writes `terrainHeight(XZ) << 16`.
func TestOrderlessGroundMoverRunsTheMoverTickAndPostMoveCorrection(t *testing.T) {
	sys, w := orderlessMoverSystem(t, 4)
	def := orderlessMoverDef("orderless-upright")
	x, z := world.CellToWorld(5), world.CellToWorld(5)
	h, err := w.Create(def, 0, x, numeric.Fixed(99<<16), z)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	// The transform is dirty: the correction's gate is the dirty bit or
	// `canhover`, and nothing else [04 R-MOV-01 §5].
	u.Flags |= unitTransformDirty

	if q := orders.QueueOfUnit(u); q != nil && q.Head() != nil {
		t.Fatal("fixture unit holds an order; this test is about an ORDERLESS mover")
	}
	runMovementTick(sys, 1, w)

	want := sys.Terrain.HeightAt(u.X, u.Z)
	if u.Y != want {
		t.Fatalf("orderless upright mover Y=%d after one tick, want the terrain height %d [04 R-MOV-01 §5]",
			u.Y.Raw()>>16, want.Raw()>>16)
	}
	coll := sys.Collisions[h]
	if coll == nil {
		t.Fatal("no collision record")
	}
	if got, present := sys.Grid.OccupantAtPlane(PlaneGround, coll.CachedAnchor); !present || got != coll.ID {
		t.Fatalf("orderless mover ground word = (%d,%t), want its own id %d [04 R-COLL-01 §4]", got, present, coll.ID)
	}
	// The unit did not move: a no-waypoint follower produces no motion.
	if u.X != x || u.Z != z {
		t.Fatalf("orderless mover drifted to %d,%d from %d,%d; the no-waypoint follower brakes and never turns [04 R-MOV-01 §3]",
			u.X.Raw()>>16, u.Z.Raw()>>16, x.Raw()>>16, z.Raw()>>16)
	}
}

// TestCanhoverTakesItsPostMoveBranchEveryTick is the relationship the gate of
// [04 R-MOV-01 §5] states: the correction runs when the transform is dirty OR
// the definition has `canhover`, so a parked hovercraft takes its branch every
// tick while an identical non-hovering unit takes none. Both units are
// orderless and at rest, so nothing else can be setting the dirty bit — which
// is exactly why the commit's stationary early return of [04 R-COLL-01 §1] has
// to be honoured for this pair to differ at all.
func TestCanhoverTakesItsPostMoveBranchEveryTick(t *testing.T) {
	sys, w := orderlessMoverSystem(t, 4)
	plain := orderlessMoverDef("orderless-plain")
	hover := orderlessMoverDef("orderless-hover")
	hover.CanHover = true

	px, pz := world.CellToWorld(4), world.CellToWorld(4)
	hx, hz := world.CellToWorld(8), world.CellToWorld(8)
	ph, err := w.Create(plain, 0, px, sys.Terrain.HeightAt(px, pz), pz)
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}
	hh, err := w.Create(hover, 0, hx, sys.Terrain.HeightAt(hx, hz), hz)
	if err != nil {
		t.Fatalf("create hover: %v", err)
	}
	pu, hu := w.Unit(ph), w.Unit(hh)
	sys.EnsureUnit(pu)
	sys.EnsureUnit(hu)
	// Settle both, so neither carries a dirty bit into the measured ticks.
	runMovementTick(sys, 1, w)

	const bogus = numeric.Fixed(99 << 16)
	for tick := uint32(2); tick < 8; tick++ {
		pu.Y, hu.Y = bogus, bogus
		runMovementTick(sys, tick, w)
		if pu.Y != bogus {
			t.Fatalf("tick %d: the non-hovering mover's Y was rewritten to %d with a clean transform; the gate is dirty OR canhover [04 R-MOV-01 §5]",
				tick, pu.Y.Raw()>>16)
		}
		if want := sys.Terrain.HeightAt(hu.X, hu.Z); hu.Y != want {
			t.Fatalf("tick %d: the canhover mover's Y=%d, want the branch to run every tick and write %d [04 R-MOV-01 §5]",
				tick, hu.Y.Raw()>>16, want.Raw()>>16)
		}
	}
}

// TestOrderlessMoverAtRestNeverRevalidates locks the stationary early return of
// [04 R-COLL-01 §1]: when the proposal equals the current position and the
// proposed mode equals the committed mirror, the commit step writes nothing —
// no dirty bit, no validator, no clear and no stamp. The occupancy revision is
// the observable: every stamp and clear bumps it [04 §7.4].
func TestOrderlessMoverAtRestNeverRevalidates(t *testing.T) {
	sys, w := orderlessMoverSystem(t, 4)
	def := orderlessMoverDef("orderless-at-rest")
	x, z := world.CellToWorld(6), world.CellToWorld(6)
	h, err := w.Create(def, 0, x, syntheticTerrainForIntegrate().HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	runMovementTick(sys, 1, w)

	coll := sys.Collisions[h]
	anchorBefore := coll.CachedAnchor
	revBefore := sys.Grid.Revision()
	// A Y the correction would overwrite the moment anything set the dirty bit.
	// The same-cell fast path DOES set it; the stationary return does not.
	const bogus = numeric.Fixed(99 << 16)
	u.Y = bogus
	for tick := uint32(2); tick < 12; tick++ {
		runMovementTick(sys, tick, w)
		if u.Y != bogus {
			t.Fatalf("tick %d: the post-move correction ran for a mover at rest with a clean transform [04 R-COLL-01 §1][04 R-MOV-01 §5]", tick)
		}
		if coll.Dirty {
			t.Fatalf("tick %d: the commit set transform-dirty for a mover at rest; the stationary return writes nothing [04 R-COLL-01 §1]", tick)
		}
		if u.Flags&unitTransformDirty != 0 {
			t.Fatalf("tick %d: the unit's transform-dirty bit is set for a mover at rest [04 R-COLL-01 §1]", tick)
		}
	}
	if got := sys.Grid.Revision(); got != revBefore {
		t.Fatalf("occupancy revision %d after ten resting ticks, want %d: a resting mover neither clears nor stamps [04 R-COLL-01 §1]", got, revBefore)
	}
	if coll.CachedAnchor != anchorBefore {
		t.Fatalf("cached cell pair moved to %v from %v while at rest [04 R-COLL-01 §1]", coll.CachedAnchor, anchorBefore)
	}
}

// TestOrderlessMoverTickDrawsNoRandomValues is [04 R-MOV-01 §5]'s "Bounded
// negative": no simulation-RNG entry point appears anywhere in the mover tick,
// the ground steering, the speed update, the position commit, the movement-rate
// classifier, the band classifier or the post-move correction. The mover draws
// no random numbers at all. The idle circle of [04 R-AIR-01 §7] does draw three
// values per visit, but it lives in the `VTOL_Standby` ORDER executor, so an
// orderless aircraft reaches none of it.
func TestOrderlessMoverTickDrawsNoRandomValues(t *testing.T) {
	sys, w := orderlessMoverSystem(t, 6)
	ground := orderlessMoverDef("orderless-rng-ground")
	air := orderlessMoverDef("orderless-rng-air")
	air.CanFly = true
	air.CruiseAlt = 60

	gx, gz := world.CellToWorld(4), world.CellToWorld(4)
	ax, az := world.CellToWorld(10), world.CellToWorld(10)
	gh, err := w.Create(ground, 0, gx, sys.Terrain.HeightAt(gx, gz), gz)
	if err != nil {
		t.Fatalf("create ground: %v", err)
	}
	ah, err := w.Create(air, 0, ax, sys.Terrain.HeightAt(ax, az)+numeric.Fixed(60<<16), az)
	if err != nil {
		t.Fatalf("create air: %v", err)
	}
	sys.EnsureUnit(w.Unit(gh))
	sys.EnsureUnit(w.Unit(ah))

	sim := rng.NewSimulation(0x12345677)
	binding := &orders.QueueBinding{SimRNG: &sim, Lookup: w.Unit}
	orders.QueueForUnit(w.Unit(gh)).SetBinding(binding)
	orders.QueueForUnit(w.Unit(ah)).SetBinding(binding)
	sys.BindAirOrderLegs()

	for tick := uint32(1); tick <= 60; tick++ {
		runMovementTick(sys, tick, w)
	}
	if got := sim.Draws(); got != 0 {
		t.Fatalf("the orderless mover tick drew %d simulation values; the mover draws none [04 R-MOV-01 §5]", got)
	}
}

// TestBuildingHasNoMoverTick keeps the mover gate honest: the sweep runs the
// mover tick only for a unit that HAS a mover, and a building-class record has
// none [04 R-MOV-03 §1] step 9. A building whose transform is dirty must not
// have its Y rewritten by the ground post-move correction.
func TestBuildingHasNoMoverTick(t *testing.T) {
	sys, w := orderlessMoverSystem(t, 4)
	def := orderlessMoverDef("orderless-building")
	def.BMCode = 0
	def.YardMap = "o"
	x, z := world.CellToWorld(5), world.CellToWorld(5)
	h, err := w.Create(def, 0, x, numeric.Fixed(99<<16), z)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	u.Flags |= unitTransformDirty
	runMovementTick(sys, 1, w)
	if u.Y != numeric.Fixed(99<<16) {
		t.Fatalf("building Y rewritten to %d; a building has no mover and no post-move correction [04 R-MOV-03 §1]", u.Y.Raw()>>16)
	}
}
