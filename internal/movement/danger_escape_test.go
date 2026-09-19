package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestDangerRouteAdmitsDetourWithoutMutationsOrAllocations(t *testing.T) {
	s, u := dangerEscapeFixture(t, false)
	x, z := numeric.FixedFromInt(85), numeric.FixedFromInt(53) // fractional final corridor
	s.Grid.Stamp(Cell{X: 3, Z: 2}, 1, 1, 99)
	if s.DangerStepFeasible(u, x, z) || !s.DangerRouteFeasible(u, x, z) {
		t.Fatal("blocked direct corridor did not use a feasible local detour")
	}
	beforeUnit, beforeCollision, beforeRoute := *u, *s.Collisions[u.Handle], *s.Routes[u.Handle]
	revision := s.Grid.Revision()
	if allocations := testing.AllocsPerRun(20, func() { s.DangerRouteFeasible(u, x, z) }); allocations != 0 {
		t.Fatalf("local admission allocated: %v", allocations)
	}
	if !reflect.DeepEqual(*u, beforeUnit) || !reflect.DeepEqual(*s.Collisions[u.Handle], beforeCollision) ||
		!reflect.DeepEqual(*s.Routes[u.Handle], beforeRoute) || s.Grid.Revision() != revision {
		t.Fatal("local admission changed unit, collision, route or occupancy state")
	}
}

func TestDangerRouteRespectsEnclosureTerrainAndRadius(t *testing.T) {
	s, u := dangerEscapeFixture(t, false)
	x, z := u.X+numeric.FixedFromInt(48), u.Z
	for _, cell := range []Cell{{3, 2}, {2, 3}, {1, 2}, {2, 1}} {
		s.Grid.Stamp(cell, 1, 1, 99)
	}
	if s.DangerRouteFeasible(u, x, z) {
		t.Fatal("enclosed unit fabricated a detour")
	}
	s, u = dangerEscapeFixture(t, false)
	// A whole vertical terrain barrier cannot be solved by going around one
	// occupant; every feasible edge still uses the unit's authored profile.
	for cz := int32(0); cz < s.Terrain.CellH; cz++ {
		s.Terrain.PlotAt(3, cz).SetMinHeight(0)
		s.Terrain.PlotAt(3, cz).SetMaxHeight(255)
	}
	p := s.profiles[u.Handle]
	p.MaxSlope, p.BadSlope = 10, 5
	if s.DangerRouteFeasible(u, x, z) {
		t.Fatal("local detour crossed impassable terrain")
	}
	s, u = dangerEscapeFixture(t, false)
	s.Grid.Stamp(Cell{X: 3, Z: 2}, 1, 1, 99)
	if !s.DangerRouteFeasible(u, u.X+numeric.FixedFromInt(64), u.Z) {
		t.Fatal("inclusive 64-unit destination boundary rejected")
	}
	if s.DangerRouteFeasible(u, u.X+numeric.FixedFromInt(64)+1, u.Z) {
		t.Fatal("detour exceeded the 64-unit radius")
	}
	// The broader pre-existing direct-corridor contract remains unchanged.
	if !s.DangerRouteFeasible(u, u.X, u.Z+numeric.FixedFromInt(80)) {
		t.Fatal("detour radius restricted an existing direct corridor")
	}
}

func dangerEscapeFixture(t *testing.T, aircraft bool) (*System, *units.Unit) {
	t.Helper()
	s := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(2)
	d := &content.UnitDef{UnitName: "escape", BMCode: 1, CanMove: true, CanFly: aircraft,
		FootprintX: 1, FootprintZ: 1, MaxVelocity: 65536, MaxWaterDepth: 10000, MinWaterDepth: -10000, MaxSlope: 255, MaxWaterSlope: 255}
	h, err := w.Create(d, 0, numeric.FixedFromInt(40), 0, numeric.FixedFromInt(40))
	if err != nil {
		t.Fatal(err)
	}
	s.BindWorld(w)
	u := w.Unit(h)
	s.EnsureUnit(u)
	return s, u
}

func TestDangerEscapeChecksCorridorAndCorner(t *testing.T) {
	s, u := dangerEscapeFixture(t, false)
	endX, endZ := numeric.FixedFromInt(88), numeric.FixedFromInt(40)
	if !s.DangerStepFeasible(u, endX, endZ) {
		t.Fatal("clear local corridor refused")
	}
	s.Grid.Stamp(Cell{X: 3, Z: 2}, 1, 1, 99)
	if s.DangerStepFeasible(u, endX, endZ) {
		t.Fatal("clear destination hid a blocked intermediate anchor")
	}
	if s.DangerStepFeasible(u, numeric.FixedFromInt(56), numeric.FixedFromInt(56)) {
		t.Fatal("diagonal escape cut through a blocked corner")
	}
	if id, _ := s.Grid.OccupantAt(Cell{X: 3, Z: 2}); id != 99 || u.X != numeric.FixedFromInt(40) {
		t.Fatal("escape query changed occupancy or position")
	}
}

func TestDangerEscapeUsesFlightPlaneAndBounds(t *testing.T) {
	s, u := dangerEscapeFixture(t, true)
	endX, endZ := numeric.FixedFromInt(88), numeric.FixedFromInt(40)
	s.Grid.Stamp(Cell{X: 3, Z: 2}, 1, 1, 99)
	if !s.DangerStepFeasible(u, endX, endZ) {
		t.Fatal("ground occupant blocked flight corridor")
	}
	s.Grid.StampPlane(PlaneAir, Cell{X: 3, Z: 2}, 1, 1, 98)
	if s.DangerStepFeasible(u, endX, endZ) {
		t.Fatal("occupied flight corridor admitted")
	}
	for _, x := range []numeric.Fixed{numeric.FixedFromInt(-24), numeric.FixedFromInt(400), u.X} {
		if s.DangerStepFeasible(u, x, u.Z) {
			t.Fatalf("invalid/nonlocal escape admitted: %d", x)
		}
	}
}

func TestDangerEscapeChecksWholeFootprint(t *testing.T) {
	s, u := dangerEscapeFixture(t, false)
	c := handleRow(s.Collisions, u.Handle)
	c.FootPrintX, c.FootPrintZ = 2, 2
	// Centre (72,40) anchors a two-cell footprint at (4,2). An obstruction
	// under its far edge must reject even when the anchor itself is empty.
	s.Grid.Stamp(Cell{X: 5, Z: 3}, 1, 1, 99)
	if s.DangerStepFeasible(u, numeric.FixedFromInt(72), numeric.FixedFromInt(40)) {
		t.Fatal("escape tested only the footprint anchor")
	}
	if s.DangerStepFeasible(u, numeric.FixedFromInt(-1), numeric.FixedFromInt(40)) {
		t.Fatal("partly off-map footprint admitted")
	}
}
