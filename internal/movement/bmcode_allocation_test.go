package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestBMCodeTwoCreatesNeitherMoverNorBuildingOccupancy(t *testing.T) {
	terrain := syntheticFlat(32, 32)
	sys := NewSystem(terrain, captureBootstrapProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	sys.BindWorld(w)
	def := setScratchMovement(&content.UnitDef{UnitName: "byte-two", CanFly: true, CanMove: true, MaxDamage: 100}, captureBootstrapProfile)
	def.BMCode = 2
	x, z := world.CellToWorld(8), world.CellToWorld(8)
	h, err := w.Create(def, 0, x, terrain.HeightAt(x, z), z)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	if u.Flags&units.BuildingClassStatus != 0 {
		t.Fatal("nonzero byte became building class")
	}
	if sys.HasMover(h) || sys.Collisions[h] != nil || sys.Steers[h] != nil || sys.Flights[h] != nil || sys.Routes[h] != nil {
		t.Fatal("byte two acquired mover state")
	}
	anchor := Cell{X: 8, Z: 8}
	for _, plane := range []Plane{PlaneGround, PlaneAir} {
		if _, ok := sys.Grid.OccupantAtPlane(plane, anchor); ok {
			t.Fatal("mover-less non-building stamped occupancy")
		}
	}
}
