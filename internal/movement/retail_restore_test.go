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
