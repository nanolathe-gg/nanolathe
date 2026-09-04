package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// teleportOccupancyWorldUnit is one world unit in 16.16; a cell is sixteen of
// them [03 §2.1].
const teleportOccupancyWorldUnit = 65536

// teleportOccupancyGroundWord reads the mover half of a cell's occupancy word
// — retail's plot-cell first word [03 §2.2][04 R-COLL-01 §2].
func teleportOccupancyGroundWord(t *world.Terrain, cx, cz int32) int16 {
	cell := t.PlotAt(cx, cz)
	if cell == nil {
		return 0
	}
	return cell.OccupantA()
}

// TestTeleportReleasesTheVacatedFootprintCells is the reason WU-19-142 needed a
// position-setter seam rather than three field writes: the `Teleport` row
// "places [each enclosed unit] through the position setter (re-registers
// occupancy when the footprint cell changes)" [04 R-ORD-01 §2], and a bare
// X/Y/Z write would leave the ground words of the old footprint claimed for the
// rest of the battle — the failure the clear's class-layer maintenance exists
// to prevent [04 R-COLL-01 §4][04 R-MOV-03 §3].
//
// The assertion is a relationship, not a census: every cell of the old
// rectangle is free afterwards and every cell of the new one carries the moved
// unit's identity.
func TestTeleportReleasesTheVacatedFootprintCells(t *testing.T) {
	rng.SeedGlobal(1, 0)
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	unitsPool := newSessionFixtureWorld(4, nil)
	sys := movement.NewSystem(terrain, movement.Template(), movement.NewOccupancyGrid())
	sys.BindWorld(unitsPool)

	// The teleporter's box is its footprint on X and Z and its model-top walk
	// on Y [02 R-CAT-01 §7]: 8x8 cells is ±64 world units around its position.
	gate := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "gate"},
		MaxDamage:        1, FootprintX: 8, FootprintZ: 8,
		ModelTopFixed: 100 * teleportOccupancyWorldUnit,
	}
	rider := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "rider"},
		MaxDamage:        1, FootprintX: 2, FootprintZ: 2,
		BMCode: true, MobilityDomain: content.MobilityGround,
		MaxVelocity: 65536, MaxSlope: 255, MaxWaterDepth: 10000, MinWaterDepth: -10000,
	}

	const centre = 160 * teleportOccupancyWorldUnit // cell 10
	gateHandle, err := unitsPool.Create(gate, 0, centre, 0, centre)
	if err != nil {
		t.Fatalf("create teleporter: %v", err)
	}
	riderHandle, err := unitsPool.Create(rider, 0, centre, 0, centre)
	if err != nil {
		t.Fatalf("create rider: %v", err)
	}
	teleporter := unitsPool.Unit(gateHandle)
	moved := unitsPool.Unit(riderHandle)
	// The rider is registered first so it, not the teleporter, owns the ground
	// words under the box: the two rectangles necessarily overlap, because the
	// box's X/Z extent IS the teleporter's footprint [02 R-CAT-01 §7], and the
	// overlap protocol leaves a contested cell with its first occupant
	// [04 R-COLL-01 §4].
	sys.EnsureUnit(moved)
	sys.EnsureUnit(teleporter)

	before, footX, footZ, ok := sys.CommittedFootprint(riderHandle)
	if !ok {
		t.Fatal("the rider has no committed footprint; the fixture is not exercising occupancy")
	}
	stampedBefore := false
	for dz := int32(0); dz < int32(footZ); dz++ {
		for dx := int32(0); dx < int32(footX); dx++ {
			if teleportOccupancyGroundWord(terrain, before.X+dx, before.Z+dz) == int16(riderHandle) {
				stampedBefore = true
			}
		}
	}
	if !stampedBefore {
		t.Fatal("the rider holds no ground word before the teleport; nothing could be released")
	}

	// The goal is twenty cells away on both axes, so the displaced footprint
	// cannot overlap the vacated one.
	const goal = 480 * teleportOccupancyWorldUnit // cell 30
	binding := &orders.QueueBinding{
		SimRNG: rng.Global.Sim,
		World: &orders.WorldQueryAdapter{
			ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
				stopped := false
				unitsPool.VisitActiveSlots(func(v units.SlotVisit) {
					if stopped {
						return
					}
					if visit(v.Handle, v.Unit) {
						stopped = true
					}
				})
			},
		},
		// The binding under test: the same callback newOrderBinding installs.
		Movement: &orders.MovementGoalAdapter{PlaceUnit: sys.PlaceUnit},
	}
	q := &orders.Queue{}
	q.SetBinding(binding)
	orders.BindQueue(teleporter, q)
	q.Push(orders.Lookup("Teleport"), orders.Node{
		Owner: gateHandle,
		GoalX: goal, GoalY: 0, GoalZ: goal,
	})
	q.Pump(teleporter, 0)

	if moved.X != numeric.Fixed(goal) || moved.Z != numeric.Fixed(goal) {
		t.Fatalf("rider at (%d,%d), want the goal delta applied to (%d,%d) [04 R-ORD-01 §2]",
			moved.X, moved.Z, goal, goal)
	}
	after, _, _, ok := sys.CommittedFootprint(riderHandle)
	if !ok || after == before {
		t.Fatalf("the committed cell pair is still %v; the setter did not re-anchor [04 R-COLL-01 §4]", before)
	}
	for dz := int32(0); dz < int32(footZ); dz++ {
		for dx := int32(0); dx < int32(footX); dx++ {
			if got := teleportOccupancyGroundWord(terrain, before.X+dx, before.Z+dz); got == int16(riderHandle) {
				t.Fatalf("vacated cell (%d,%d) still holds the rider; the old footprint was never released [04 R-COLL-01 §4]",
					before.X+dx, before.Z+dz)
			}
			if got := teleportOccupancyGroundWord(terrain, after.X+dx, after.Z+dz); got != int16(riderHandle) {
				t.Fatalf("destination cell (%d,%d) holds %d, want the rider %d restamped there [04 R-COLL-01 §4]",
					after.X+dx, after.Z+dz, got, riderHandle)
			}
		}
	}
}
