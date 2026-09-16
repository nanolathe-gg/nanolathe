package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The `Teleport` row's contract [04 R-ORD-01 §2]: for every live unit other
// than itself whose position lies inside this unit's model bounding box —
// inclusive on all three axes — the new position is `goal + (its position −
// my position)`; the teleport effect is emitted from old to new; then the unit
// is placed through the position setter. The teleporter itself never moves.
//
// The box is the definition's min/max triple [02 R-CAT-01 §7]: X and Z from
// the FOOTPRINT (`±(Footprint << 20) / 2` in 16.16), Y from the model-top walk
// with a zeroed lower bound.

const tpFx = 65536 // one world unit in 16.16

// teleportPlacement is one recorded call on the movement or presentation seam.
type teleportPlacement struct {
	what  string // "effect" or "place"
	unit  pool.Handle
	fromX numeric.Fixed
	fromY numeric.Fixed
	fromZ numeric.Fixed
	toX   numeric.Fixed
	toY   numeric.Fixed
	toZ   numeric.Fixed
}

// teleportFixture wires one teleporter and a fixed candidate list onto a
// binding whose ForEachUnit walks that list in slot order [I1], recording every
// seam call in order.
func teleportFixture(t *testing.T, def *content.UnitDef, others []*units.Unit) (*Queue, *units.Unit, *[]teleportPlacement) {
	t.Helper()
	rng.SeedGlobal(1, 0)
	teleporter := &units.Unit{
		Handle: 1, Def: def, Alive: true,
		X: 70 * tpFx, Y: 40 * tpFx, Z: 90 * tpFx,
		Health: 3000, MaxHealth: 3000,
	}
	log := &[]teleportPlacement{}
	walk := append([]*units.Unit{teleporter}, others...)
	binding := &QueueBinding{
		SimRNG: rng.Global.Sim,
		World: &WorldQueryAdapter{
			ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
				for _, u := range walk {
					if visit(u.Handle, u) {
						return
					}
				}
			},
		},
		Movement: &MovementGoalAdapter{
			PlaceUnit: func(req PlaceRequest) bool {
				*log = append(*log, teleportPlacement{
					what: "place", unit: req.Unit,
					toX: req.X, toY: req.Y, toZ: req.Z,
				})
				for _, u := range walk {
					if u.Handle == req.Unit {
						u.X, u.Y, u.Z = req.X, req.Y, req.Z
					}
				}
				return true
			},
		},
		Presentation: &PresentationAdapter{
			Teleport: func(moved *units.Unit, fx0, fy0, fz0, tx, ty, tz numeric.Fixed) bool {
				*log = append(*log, teleportPlacement{
					what: "effect", unit: moved.Handle,
					fromX: fx0, fromY: fy0, fromZ: fz0,
					toX: tx, toY: ty, toZ: tz,
				})
				return true
			},
		},
	}
	q := &Queue{binding: binding}
	BindQueue(teleporter, q)
	for _, u := range others {
		BindQueue(u, q)
	}
	return q, teleporter, log
}

// teleportCandidate is one live unit at a world point, in whole world units.
func teleportCandidate(handle pool.Handle, x, y, z int64) *units.Unit {
	return &units.Unit{
		Handle: handle, Def: &content.UnitDef{}, Alive: true,
		X: numeric.Fixed(x * tpFx), Y: numeric.Fixed(y * tpFx), Z: numeric.Fixed(z * tpFx),
		Health: 100, MaxHealth: 100,
	}
}

// TestTeleportMovesEnclosedUnitsByTheGoalDelta locks the row's arithmetic:
// the box test on all three axes (inclusive at both ends), the displacement
// `goal + (its position − my position)`, and the two facts a summary is most
// likely to lose — the teleporter never moves, and the effect is emitted once
// per moved unit BEFORE that unit's position commit [04 R-ORD-01 §2]
// [03 R-LAYER §4].
func TestTeleportMovesEnclosedUnitsByTheGoalDelta(t *testing.T) {
	// Footprint 2x2 gives ±(2<<20)/2 = ±16 world units on X and Z; the
	// model-top walk gives a Y span of [y, y+40] [02 R-CAT-01 §7]. Around
	// (70, 40, 90) that is X in [54, 86], Y in [40, 80], Z in [74, 106].
	def := &content.UnitDef{FootprintX: 2, FootprintZ: 2, ModelTopFixed: 40 * tpFx}

	inside := teleportCandidate(2, 70, 45, 90)
	onMaxX := teleportCandidate(3, 86, 40, 90) // inclusive upper X bound
	onMaxY := teleportCandidate(4, 70, 80, 106)
	pastMaxX := teleportCandidate(5, 86, 40, 90)
	pastMaxX.X++ // one 16.16 step past the bound
	belowMinY := teleportCandidate(6, 70, 40, 90)
	belowMinY.Y-- // the lower Y bound is the teleporter's own Y, zeroed
	dead := teleportCandidate(7, 70, 45, 90)
	dead.Alive = false

	others := []*units.Unit{inside, onMaxX, onMaxY, pastMaxX, belowMinY, dead}
	q, teleporter, log := teleportFixture(t, def, others)

	q.Push(Lookup("Teleport"), Node{
		Owner: teleporter.Handle,
		GoalX: 200 * tpFx, GoalY: 50 * tpFx, GoalZ: 300 * tpFx,
		GoalSupplied: true,
	})
	q.Pump(teleporter, 0)

	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the record completed on its single visit [04 R-ORD-01 §2]", q.LenPrimary())
	}
	// The teleporter itself never moves [04 R-ORD-01 §2][04 R-SPEC-01 §2].
	if teleporter.X != 70*tpFx || teleporter.Y != 40*tpFx || teleporter.Z != 90*tpFx {
		t.Fatalf("teleporter moved to (%d,%d,%d); it never moves [04 R-ORD-01 §2]",
			teleporter.X, teleporter.Y, teleporter.Z)
	}

	// goal − my position = (130, 10, 210) in whole world units.
	want := []struct {
		unit    *units.Unit
		x, y, z int64
	}{
		{inside, 200, 55, 300}, // 200+(70−70), 50+(45−40), 300+(90−90)
		{onMaxX, 216, 50, 300}, // 200+(86−70)
		{onMaxY, 200, 90, 316}, // 50+(80−40), 300+(106−90)
	}
	for _, w := range want {
		if w.unit.X != numeric.Fixed(w.x*tpFx) || w.unit.Y != numeric.Fixed(w.y*tpFx) || w.unit.Z != numeric.Fixed(w.z*tpFx) {
			t.Fatalf("unit %d at (%d,%d,%d), want (%d,%d,%d) [04 R-ORD-01 §2]",
				w.unit.Handle, w.unit.X/tpFx, w.unit.Y/tpFx, w.unit.Z/tpFx, w.x, w.y, w.z)
		}
	}
	// Strictly outside on one axis, and a dead unit, are not visited at all.
	for _, u := range []*units.Unit{pastMaxX, belowMinY, dead} {
		for _, rec := range *log {
			if rec.unit == u.Handle {
				t.Fatalf("unit %d outside the box (or not live) got a %s call [04 R-ORD-01 §2]", u.Handle, rec.what)
			}
		}
	}

	// One effect and one commit per moved unit, effect first, and the effect's
	// endpoints are the OLD and the new position [03 R-LAYER §4].
	if len(*log) != 6 {
		t.Fatalf("seam calls = %d, want 2 per moved unit for 3 moved units [03 R-LAYER §4]", len(*log))
	}
	for i, handle := range []pool.Handle{2, 3, 4} {
		effect, place := (*log)[2*i], (*log)[2*i+1]
		if effect.what != "effect" || place.what != "place" {
			t.Fatalf("call pair %d is (%s,%s), want (effect,place) [03 R-LAYER §4]", i, effect.what, place.what)
		}
		if effect.unit != handle || place.unit != handle {
			t.Fatalf("call pair %d addresses (%d,%d), want unit %d in pool order [I1]", i, effect.unit, place.unit, handle)
		}
		if effect.toX != place.toX || effect.toY != place.toY || effect.toZ != place.toZ {
			t.Fatalf("unit %d: the effect's endpoint is not the committed position [04 R-ORD-01 §2]", handle)
		}
		if effect.fromX == effect.toX && effect.fromY == effect.toY && effect.fromZ == effect.toZ {
			t.Fatalf("unit %d: the effect runs from old to new, so a moved unit's endpoints differ [03 R-LAYER §4]", handle)
		}
	}
}

// TestTeleportWithoutASeamStillCompletes locks the handler's dependency shape:
// the row is a single visit that completes whatever the binding offers, so a
// queue whose movement or presentation seam is unbound neither panics nor parks
// [04 R-ORD-01 §2]. The presentation seam in particular is expected to be nil
// until the strip-5 flame producer exists [03 R-LAYER §4].
func TestTeleportWithoutASeamStillCompletes(t *testing.T) {
	def := &content.UnitDef{FootprintX: 2, FootprintZ: 2, ModelTopFixed: 40 * tpFx}
	inside := teleportCandidate(2, 70, 45, 90)
	q, teleporter, _ := teleportFixture(t, def, []*units.Unit{inside})
	q.binding.Movement.PlaceUnit = nil
	q.binding.Presentation.Teleport = nil

	q.Push(Lookup("Teleport"), Node{Owner: teleporter.Handle, GoalX: 200 * tpFx, GoalY: 50 * tpFx, GoalZ: 300 * tpFx, GoalSupplied: true})
	q.Pump(teleporter, 0)

	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the record completed [04 R-ORD-01 §2]", q.LenPrimary())
	}
	if diags := q.Diagnostics(); len(diags) != 0 {
		t.Fatalf("dispatch recorded %v, want none [04 §3.3]", diags)
	}
	if inside.X != 70*tpFx {
		t.Fatalf("an unbound position setter moved a unit anyway")
	}
}
