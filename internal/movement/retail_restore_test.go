package movement

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestRestoreMoverCopiesStateWithoutUnitMirrorOverwrite(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1}, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	def := &content.UnitDef{UnitName: "air", CanFly: true, BMCode: true, FootprintX: 1, FootprintZ: 1, MaxVelocity: 4 << 16}
	h, err := w.Create(def, 0, world.CellToWorld(2), numeric.Fixed(10<<16), world.CellToWorld(2))
	if err != nil {
		t.Fatal(err)
	}
	sys.BindWorld(w)
	sys.EnsureUnit(w.Unit(h))
	base := make([]byte, 0xB8)
	binary.LittleEndian.PutUint32(base[0xB4:], 2<<4) // packed unit mirror = 2
	if err := units.RetailUnitBase(w.Unit(h), base); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 35)
	for i, v := range []int32{1, -2, 3, 4, -5, 6, 7} {
		binary.LittleEndian.PutUint32(data[i*4:], uint32(v))
	}
	binary.LittleEndian.PutUint16(data[28:], 0xfff7)
	binary.LittleEndian.PutUint32(data[30:], 1234)
	data[34] = 1 | 4 | 0xA0 // mover-side mode = 1, intentionally disagrees
	if err := sys.RestoreMover(h, data); err != nil {
		t.Fatal(err)
	}
	c := sys.Collisions[h]
	if c.VX != 1 || c.VY != -2 || c.VZ != 3 || c.LeanX != 4 || c.LeanY != -5 || c.LeanZ != 6 || c.Speed != 7 || c.TurnResidual != -9 || c.LastStampTick != 1234 || c.Mode != 1 || !c.Blocked || c.SavedStateByte != data[34] {
		t.Fatalf("mover restore lost state: %#v", c)
	}
	if w.Unit(h).Move.Mode != 2 {
		t.Fatalf("mover byte overwrote packed unit mirror: %d", w.Unit(h).Move.Mode)
	}
}

func TestRestoreOccupancyUsesMoverModeAndAllowsOffMap(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1}, NewOccupancyGrid())
	sys.Grid.AttachPlot(terrain)
	w := newMovementFixtureWorld(8)
	def := &content.UnitDef{UnitName: "restore-mode", BMCode: true, FootprintX: 1, FootprintZ: 1}
	h, err := w.Create(def, 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatal(err)
	}
	sys.BindWorld(w)
	sys.EnsureUnit(w.Unit(h))
	restore := func(mode byte, x, z int16) {
		t.Helper()
		data := make([]byte, 35)
		data[34] = mode
		if err := sys.RestoreMover(h, data); err != nil {
			t.Fatalf("RestoreMover mode %d: %v", mode, err)
		}
		if err := sys.RestoreOccupancy(h, x, z); err != nil {
			t.Fatalf("RestoreOccupancy mode %d: %v", mode, err)
		}
	}

	restore(2, 4, 4)
	if _, ok := sys.Grid.OccupantAtPlane(PlaneGround, Cell{X: 4, Z: 4}); ok {
		t.Fatal("airborne restore stamped ground plane")
	}
	if got, ok := sys.Grid.OccupantAtPlane(PlaneAir, Cell{X: 4, Z: 4}); !ok || got != int(h) {
		t.Fatalf("airborne restore air occupant = (%d,%v), want (%d,true)", got, ok, h)
	}
	restore(0, 5, 5)
	if _, ok := sys.Grid.OccupantAtPlane(PlaneAir, Cell{X: 4, Z: 4}); ok {
		t.Fatal("carried restore retained previous air stamp")
	}
	restore(1, 6, 6)
	if got, ok := sys.Grid.OccupantAtPlane(PlaneGround, Cell{X: 6, Z: 6}); !ok || got != int(h) {
		t.Fatalf("grounded restore occupant = (%d,%v), want (%d,true)", got, ok, h)
	}
	restore(3, 7, 7)
	if _, ok := sys.Grid.OccupantAtPlane(PlaneGround, Cell{X: 6, Z: 6}); ok {
		t.Fatal("mode-3 restore retained previous ground stamp")
	}
	restore(1, 20, 20)
	if c := sys.Collisions[h]; c.HasStamp {
		t.Fatal("off-map restore recorded a non-existent stamp")
	}

	building := &content.UnitDef{UnitName: "restore-structure", FootprintX: 1, FootprintZ: 1, YardMap: "o"}
	buildingHandle, err := w.Create(building, 0, world.CellToWorld(3), 0, world.CellToWorld(3))
	if err != nil {
		t.Fatal(err)
	}
	sys.EnsureUnit(w.Unit(buildingHandle))
	if err := sys.RestoreOccupancy(buildingHandle, 8, 8); err != nil {
		t.Fatalf("RestoreOccupancy structure: %v", err)
	}
	if got, ok := sys.Grid.OccupantAtPlane(PlaneGround, Cell{X: 8, Z: 8}); !ok || got != int(buildingHandle) {
		t.Fatalf("structure restore occupant = (%d,%v), want (%d,true)", got, ok, buildingHandle)
	}
}
