package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestAirborneRestampAdvancesTheOccupantClock locks the stamp's first action
// [04 R-COLL-01 §4 "stamp, in order"]: "The mover's last-stamp tick (the
// occupant-age clock of [R-DOC04-B]) is set to the current tick" — before the
// bounds test, without reference to the plane being written, guarded only on
// the mover existing [04 R-PATH-01 §14 "writers of the mover's commit tick"].
//
// The build wrote it only when the GROUND plane was touched, so an aircraft
// restamping in the air kept a clock frozen at takeoff and its cells could read
// stale to the first classification after touchdown.
func TestAirborneRestampAdvancesTheOccupantClock(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	sys.BeginTick(1)
	at := world.CellToWorld(6)
	h, err := w.Create(wiringDef(), 0, at, terrain.HeightAt(at, at), at)
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	layer := sys.ensureLayerRegistry().For("", wiringProfile)

	// Leave the ground plane for the air plane; this transition already wrote
	// the clock before the change, because it clears a ground rectangle.
	u.Move.Mode = 2
	sys.syncMoverStamp(u)

	// A purely airborne move: clear the air word at the old pair, stamp it at
	// the new one. No ground cell is touched.
	sys.BeginTick(300)
	coll := handleRow(sys.Collisions, h)
	coll.CachedAnchor = Cell{X: coll.CachedAnchor.X + 2, Z: coll.CachedAnchor.Z}
	sys.syncMoverStamp(u)

	if got, _ := layer.CommitTick(h); got != 300 {
		t.Errorf("last-stamp tick after an airborne restamp = %d, want 300: the stamp writes "+
			"the mover's clock unconditionally [04 R-COLL-01 §4]", got)
	}
}

// stampClockDef is a two-cells-per-tick ground mover on the wiring profile, so
// one step from rest crosses a cell boundary and the commit takes the success
// branch. The same-cell fast path "commits the transform without restamping
// occupancy", so it writes no clock [04 R-COLL-01 §1].
func stampClockDef() *content.UnitDef {
	return setScratchMovement(&content.UnitDef{
		UnitName: "stamp-clock-test", MaxDamage: 100, BMCode: 1, CanMove: true,
		FootprintX: 1, FootprintZ: 1,
		MaxVelocity: int32(2 * worldUnitsPerCell), Acceleration: int32(2 * worldUnitsPerCell),
		BrakeRate: int32(2 * worldUnitsPerCell), TurnRate: 65535,
	}, wiringProfile)
}

// stampClockFixture places that mover at cell (2,2) on the flat integrate
// terrain, optionally with a published straight route east so its next commit
// is a real cross-cell proposal.
func stampClockFixture(t *testing.T, withRoute bool) (*System, *units.World, pool.Handle) {
	t.Helper()
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	at := world.CellToWorld(2)
	h, err := w.Create(stampClockDef(), 0, at, terrain.HeightAt(at, at), at)
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	sys.EnsureUnit(w.Unit(h))
	if withRoute {
		moveID := orders.Lookup("Move_Ground")
		if moveID == 0 {
			t.Fatal("Move_Ground order is unavailable")
		}
		q := orders.QueueForUnit(w.Unit(h))
		q.Push(moveID, orders.Node{GoalX: world.CellToWorld(8), GoalZ: world.CellToWorld(2), GoalSupplied: true})
		// Route points are whole world units, cells of sixteen.
		handleRow(sys.Routes, h).PublishAtRevision([]Point{{X: 32, Z: 32}, {X: 128, Z: 32}}, sys.staticObstacleRevision())
		setHandleRow(&sys.activeOrders, h, &activeMove{order: q.Head(), token: 7})
		sys.nextActivation = 7
	}
	return sys, w, h
}

// TestSuccessfulCommitWritesTheMoverStampTick locks the mover's own
// occupant-age word, not just this build's per-layer mirror of it. Retail keeps
// ONE such word, on the mover structure, and "the footprint stamp writes it to
// the current tick as its first action ... reached from the occupancy commit
// (every non-stationary proposal)" [04 R-PATH-01 §14][04 R-COLL-01 §4]. The
// build advanced only the ClassLayer mirrors, so the collision record's
// LastStampTick stayed zero for the whole battle and the save box wrote a zero
// clock [04 R-COLL-01 §5 "the save bit"].
//
// The two negative arms are the same section's: the blocked branch leaves "the
// cached cell pair, the mode mirror, the occupancy grid and the mover's
// last-stamp tick ... untouched" [04 R-COLL-01 §1], and the stationary early
// return writes nothing at all.
func TestSuccessfulCommitWritesTheMoverStampTick(t *testing.T) {
	t.Run("success branch stamps", func(t *testing.T) {
		sys, _, h := stampClockFixture(t, true)
		// Allocate the class layer before the commit: a layer allocated later
		// legitimately misses earlier ticks, which would hide the drift this
		// arm is here to catch.
		layer := sys.ensureLayerRegistry().For("", wiringProfile)
		coll := handleRow(sys.Collisions, h)
		if coll.LastStampTick != 0 {
			t.Fatalf("fresh mover already carries a stamp tick %d", coll.LastStampTick)
		}
		if res := stepOnce(sys, h, 57); res.Blocked || !res.Moved {
			t.Fatalf("the fixture step was not a successful commit: %+v", res)
		}
		if coll.LastStampTick != 57 {
			t.Fatalf("last-stamp tick = %d after a successful commit at tick 57, want 57 "+
				"[04 R-COLL-01 §4][04 R-PATH-01 §14]", coll.LastStampTick)
		}
		// The one word and its per-layer mirror must agree: the mirror is what
		// the occupant-age gate reads, the word is what the save box carries.
		if got, ok := layer.CommitTick(h); !ok || got != coll.LastStampTick {
			t.Fatalf("layer mirror = %d/%v, mover word = %d: the two must not drift",
				got, ok, coll.LastStampTick)
		}
	})

	t.Run("blocked branch leaves the clock alone", func(t *testing.T) {
		sys, w, h := stampClockFixture(t, true)
		// A foreign occupant on the proposed footprint makes the validator
		// reject, so the commit takes the blocked branch.
		at := world.CellToWorld(4)
		blocker, err := w.Create(stampClockDef(), 0, at, sys.Terrain.HeightAt(at, at), world.CellToWorld(2))
		if err != nil {
			t.Fatalf("create blocker: %v", err)
		}
		sys.EnsureUnit(w.Unit(blocker))
		coll := handleRow(sys.Collisions, h)
		coll.LastStampTick = 11 // a clock the mover earned on an earlier stamp
		if res := stepOnce(sys, h, 12); !res.Blocked {
			t.Fatalf("proposal into the blocker was not rejected: %+v", res)
		}
		if coll.LastStampTick != 11 {
			t.Fatalf("blocked commit moved the last-stamp tick to %d; the blocked branch "+
				"leaves it untouched [04 R-COLL-01 §1]", coll.LastStampTick)
		}
	})

	t.Run("stationary early return writes nothing", func(t *testing.T) {
		sys, _, h := stampClockFixture(t, false)
		coll := handleRow(sys.Collisions, h)
		coll.LastStampTick = 11
		if res := stepOnce(sys, h, 12); res.Moved {
			t.Fatalf("the routeless fixture moved: %+v", res)
		}
		if coll.LastStampTick != 11 {
			t.Fatalf("a unit at rest advanced its last-stamp tick to %d; the stationary "+
				"early return returns with nothing written [04 R-COLL-01 §1]", coll.LastStampTick)
		}
	})
}

// TestRetailMoverImageRoundTripsANonzeroStampClock is the save-boundary half of
// the same word: "the box's one unnamed 32-bit word ... is the mover's
// last-stamp tick (the occupant-age clock)" [04 R-COLL-01 §5 "the save bit"],
// and the restore hands it straight back to the occupant-age gate. While the
// simulation never wrote the word, every Nanolathe save carried a zero here and
// every restored unit was seeded with a commit tick of 0 — inside the first
// request revision window [old,new), which bakes the whole army's footprints
// into the class layers at once.
func TestRetailMoverImageRoundTripsANonzeroStampClock(t *testing.T) {
	sys, _, h := stampClockFixture(t, true)
	stepOnce(sys, h, 57)
	if got := handleRow(sys.Collisions, h).LastStampTick; got != 57 {
		t.Fatalf("pre-save last-stamp tick = %d, want 57", got)
	}

	image, err := sys.RetailMoverImage(h)
	if err != nil {
		t.Fatalf("save mover: %v", err)
	}

	restored, _, rh := stampClockFixture(t, false)
	if rh != h {
		t.Fatalf("fixture handles differ: %d != %d", rh, h)
	}
	if err := restored.RestoreMover(h, image); err != nil {
		t.Fatalf("restore mover: %v", err)
	}
	coll := handleRow(restored.Collisions, h)
	if coll.LastStampTick != 57 {
		t.Fatalf("restored last-stamp tick = %d, want the saved 57 [04 R-COLL-01 §5]", coll.LastStampTick)
	}

	// The restore's occupancy re-anchor mirrors the saved clock into the class
	// layers, so the gate sees the age the unit actually had rather than zero.
	layer := restored.ensureLayerRegistry().For("", wiringProfile)
	anchor := coll.CachedAnchor
	if err := restored.RestoreOccupancy(h, int16(anchor.X), int16(anchor.Z)); err != nil {
		t.Fatalf("restore occupancy: %v", err)
	}
	if got, ok := layer.CommitTick(h); !ok || got != 57 {
		t.Fatalf("restored layer mirror = %d/%v, want the saved 57", got, ok)
	}
}
