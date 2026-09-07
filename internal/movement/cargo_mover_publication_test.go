package movement

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Carried motion publishes the carrier's entire velocity triple to the cargo
// mover, including the save reader [04 R-FAC-02 §2][08 R-SAVE-02 §8].
func TestCarriedVerticalVelocityReachesMoverSave(t *testing.T) {
	for _, tt := range []struct {
		name          string
		flyingCarrier bool
		flyingCargo   bool
		hasMover      bool
	}{
		{name: "flight carrier ground cargo", flyingCarrier: true, hasMover: true},
		{name: "flight carrier flying cargo", flyingCarrier: true, flyingCargo: true, hasMover: true},
		{name: "ground carrier", hasMover: true},
		{name: "mover-less carrier"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const vertical int32 = 1 << 16
			wantVertical := vertical
			if !tt.hasMover {
				wantVertical = 0
			}
			sys := NewSystem(syntheticTerrainForIntegrate(), Template(), NewOccupancyGrid())
			w := newMovementFixtureWorld(4)
			sys.BindWorld(w)
			carrierDef := &content.UnitDef{UnitName: "carrier", CanFly: tt.flyingCarrier, BMCode: 1, FootprintX: 1, FootprintZ: 1, MaxVelocity: 32}
			cargoDef := &content.UnitDef{UnitName: "cargo", CanFly: tt.flyingCargo, BMCode: 1, FootprintX: 1, FootprintZ: 1, MaxVelocity: 32}
			carrier, err := w.Create(carrierDef, 0, world.CellToWorld(2), 0, world.CellToWorld(2))
			if err != nil {
				t.Fatal(err)
			}
			cargo, err := w.Create(cargoDef, 0, world.CellToWorld(4), 0, world.CellToWorld(4))
			if err != nil {
				t.Fatal(err)
			}
			sys.EnsureUnit(w.Unit(carrier))
			sys.EnsureUnit(w.Unit(cargo))
			if tt.flyingCarrier {
				sys.SetMoverMode(w.Unit(carrier), 2)
				fl := sys.Flights[carrier]
				fl.VX, fl.VY, fl.VZ = 12345, vertical, -6789
				sys.commitFlightState(w.Unit(carrier), fl)
			} else if tt.hasMover {
				coll := sys.Collisions[carrier]
				coll.VX, coll.VY, coll.VZ, coll.Speed = 12345, vertical, -6789, 32
			} else {
				delete(sys.Collisions, carrier)
			}
			// A stale collision value must not survive the no-mover zeroing arm.
			sys.Collisions[cargo].VY = -vertical
			if !AttachCargoMode(w, carrier, cargo, -1, 0) {
				t.Fatal("attach cargo")
			}
			sys.SyncCarriedMotion(w)
			if got := w.Unit(cargo).Move.VelY; got != numeric.Fixed(wantVertical) {
				t.Fatalf("published cargo vertical velocity = %d, want %d", got, wantVertical)
			}
			data, err := sys.RetailMoverImage(cargo)
			if err != nil {
				t.Fatal(err)
			}
			if got := int32(binary.LittleEndian.Uint32(data[4:8])); got != wantVertical {
				t.Fatalf("saved cargo vertical velocity = %d, want published velocity %d", got, wantVertical)
			}
			restored := NewSystem(syntheticTerrainForIntegrate(), Template(), NewOccupancyGrid())
			restoredWorld := newMovementFixtureWorld(4)
			if _, err := restoredWorld.Create(carrierDef, 0, world.CellToWorld(2), 0, world.CellToWorld(2)); err != nil {
				t.Fatal(err)
			}
			restoredCargo, err := restoredWorld.Create(cargoDef, 0, world.CellToWorld(4), 0, world.CellToWorld(4))
			if err != nil {
				t.Fatal(err)
			}
			if restoredCargo != cargo {
				t.Fatalf("restored cargo handle = %d, want %d", restoredCargo, cargo)
			}
			restored.BindWorld(restoredWorld)
			restored.EnsureUnit(restoredWorld.Unit(restoredCargo))
			if err := restored.RestoreMover(restoredCargo, data); err != nil {
				t.Fatal(err)
			}
			if got := restored.Collisions[restoredCargo].VY; got != wantVertical {
				t.Fatalf("restored collision vertical velocity = %d, want %d", got, wantVertical)
			}
			if got := restoredWorld.Unit(restoredCargo).Move.VelY; got != numeric.Fixed(wantVertical) {
				t.Fatalf("restored cargo vertical velocity = %d, want %d", got, wantVertical)
			}
		})
	}
}

// The attachment command rejects a carrier that is already carried, so carried
// motion has no nested chain to traverse [04 R-COB-03 §5].
func TestCarriedMotionRejectsNestedAttachment(t *testing.T) {
	sys := NewSystem(syntheticTerrainForIntegrate(), Template(), NewOccupancyGrid())
	w := newMovementFixtureWorld(6)
	sys.BindWorld(w)
	def := &content.UnitDef{UnitName: "cargo", BMCode: 1, FootprintX: 1, FootprintZ: 1, MaxVelocity: 32}
	carrier, err := w.Create(def, 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatal(err)
	}
	middle, err := w.Create(def, 0, world.CellToWorld(4), 0, world.CellToWorld(4))
	if err != nil {
		t.Fatal(err)
	}
	child, err := w.Create(def, 0, world.CellToWorld(6), 0, world.CellToWorld(6))
	if err != nil {
		t.Fatal(err)
	}
	sys.EnsureUnit(w.Unit(carrier))
	sys.EnsureUnit(w.Unit(middle))
	sys.EnsureUnit(w.Unit(child))
	const vertical int32 = 1 << 16
	sys.Collisions[carrier].VY = vertical
	if !AttachCargoMode(w, carrier, middle, -1, 0) {
		t.Fatal("attach middle")
	}
	if AttachCargoMode(w, middle, child, -1, 0) {
		t.Fatal("nested carrier attachment was accepted")
	}
	sys.SyncCarriedMotion(w)
	if got := w.Unit(middle).Move.VelY; got != numeric.Fixed(vertical) {
		t.Fatalf("carried middle vertical velocity = %d, want %d", got, vertical)
	}
	if childUnit := w.Unit(child); childUnit.Attachment.Carrier != 0 {
		t.Fatalf("rejected nested child retained carrier %d", childUnit.Attachment.Carrier)
	}
}
