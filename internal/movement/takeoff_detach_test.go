package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestTakeoffPreambleDetachesCarriedAircraftToMode2 locks the takeoff
// preamble's step 2 fix (WU-19-124): a carried aircraft self-detaches
// requesting mover mode 2 directly [04 R-AIR-01 §6 step 2][04 R-AIR-01 §9],
// so the committed mover mode must read 2 after the preamble runs even though
// the carried unit was never in the grounded mode 1 the mode-less detach and
// step 4's gate depend on. Before the fix the mode-less DetachCargo left the
// child in mode 0 and step 4's `u.Move.Mode&0x3 != 1` gate never fired, so the
// committed mode stayed 0 instead of 2.
func TestTakeoffPreambleDetachesCarriedAircraftToMode2(t *testing.T) {
	ter := syntheticFlat(32, 32)
	grid := NewOccupancyGrid()
	fallback := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := NewSystem(ter, fallback, grid)
	w := newMovementFixtureWorld(16)
	sys.BindWorld(w)

	carrierDef := defForTransport("arm_atlas")
	carrierDef.CanFly = false
	carrierDef.BMCode = 1
	cargoDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("carriedscout")},
		UnitName:         "carriedscout",
		CanFly:           true,
		CanMove:          true,
		BMCode:           1,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        100,
		CruiseAlt:        60,
		MaxVelocity:      4 * 65536,
		Acceleration:     65536 / 4,
		BrakeRate:        65536 / 8,
		TurnRate:         500,
	}

	tx, tz := world.CellToWorld(5), world.CellToWorld(5)
	cx, cz := world.CellToWorld(7), world.CellToWorld(5)
	th, err := w.Create(carrierDef, 0, tx, ter.HeightAt(tx, tz), tz)
	if err != nil {
		t.Fatalf("create carrier: %v", err)
	}
	ch, err := w.Create(cargoDef, 0, cx, ter.HeightAt(cx, cz), cz)
	if err != nil {
		t.Fatalf("create cargo: %v", err)
	}
	sys.EnsureUnit(w.Unit(th))
	sys.EnsureUnit(w.Unit(ch))
	w.Unit(th).Remaining = 0
	w.Unit(ch).Remaining = 0

	// The freshly-created aircraft spawns grounded (mode 1), holding its
	// ground cell, exactly as takeoffFixture's unit does.
	if got := w.Unit(ch).Move.Mode; got != 1 {
		t.Fatalf("fixture: cargo aircraft mover mode=%d, want the grounded 1 before attach", got)
	}
	cargoID := handleRow(sys.Collisions, ch).ID
	pickup := handleRow(sys.Collisions, ch).CachedAnchor
	if occ, ok := grid.OccupantAt(pickup); !ok || occ != cargoID {
		t.Fatalf("fixture: cargo aircraft must hold its ground cell before it is parked, got %d %v", occ, ok)
	}

	// Park it on the carrier at request mode 0 — VTOL_Landing phase 6's
	// "attach the lander itself to the target on the pad piece with request
	// mode 0" [04 R-AIR-01 §6][04 R-AIR-01 §9].
	if !AttachCargoMode(w, th, ch, -1, 0) {
		t.Fatalf("attach failed")
	}
	if got := w.Unit(ch).Move.Mode; got != 0 {
		t.Fatalf("parked mover mode=%d, want the attached 0 [04 R-AIR-01 §9]", got)
	}
	runMovementTick(sys, 1, w)
	for z := int32(0); z < ter.CellH; z++ {
		for x := int32(0); x < ter.CellW; x++ {
			if occ, ok := grid.OccupantAt(Cell{X: x, Z: z}); ok && occ == cargoID {
				t.Fatalf("parked aircraft holds ground cell (%d,%d); mode 0 stamps nothing [04 R-COLL-01 §4]", x, z)
			}
		}
	}

	// Takeoff: the preamble's step 2 self-detach must land the committed
	// mode at 2, not leave it at the parked 0.
	sys.takeoffPreamble(w.Unit(ch), nil)

	cargo := w.Unit(ch)
	if cargo.Attachment.Carrier != 0 {
		t.Fatalf("cargo still attached to carrier %d after the takeoff preamble", cargo.Attachment.Carrier)
	}
	if got := cargo.Move.Mode; got != 2 {
		t.Fatalf("committed mover mode=%d after the takeoff preamble, want 2 [04 R-AIR-01 §6 step 2][04 R-AIR-01 §9]", got)
	}

	// Ground occupancy must stay clear: mode 0 (parked) stamped nothing, and
	// the mode-2 self-detach the preamble performed does not stamp the ground
	// plane either [04 R-COLL-01 §4].
	for z := int32(0); z < ter.CellH; z++ {
		for x := int32(0); x < ter.CellW; x++ {
			if occ, ok := grid.OccupantAt(Cell{X: x, Z: z}); ok && occ == cargoID {
				t.Fatalf("cargo aircraft holds ground cell (%d,%d) after takeoff; a mode-2 mover must not stamp the ground plane [04 R-COLL-01 §4]", x, z)
			}
		}
	}
}
