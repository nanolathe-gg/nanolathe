package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestCarriedCargoReleasesGroundCells locks the load/unload half of the
// carried-position setter [04 R-COLL-01 §4][04 R-FAC-02 §2].
func TestCarriedCargoReleasesGroundCells(t *testing.T) {
	ter := syntheticFlat(32, 32)
	grid := NewOccupancyGrid()
	fallback := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := NewSystem(ter, fallback, grid)
	mc := &content.MovementClass{FootprintX: 1, FootprintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys.SetClasses(map[string]*content.MovementClass{content.CanonicalKey("kbot2x2"): mc})
	w := newMovementFixtureWorld(100)

	transDef := defForTransport("arm_atlas")
	transDef.BMCode = true
	cargoDef := defForCargo("armflea", 1)
	cargoDef.BMCode = true
	cargoDef.MovementClass = "kbot2x2"

	tx, tz := world.CellToWorld(5), world.CellToWorld(5)
	cx, cz := world.CellToWorld(7), world.CellToWorld(5)
	th, _ := w.Create(transDef, 0, tx, ter.HeightAt(tx, tz), tz)
	ch, _ := w.Create(cargoDef, 0, cx, ter.HeightAt(cx, cz), cz)
	sys.EnsureUnit(w.Unit(th))
	sys.EnsureUnit(w.Unit(ch))
	w.Unit(th).Remaining = 0
	w.Unit(ch).Remaining = 0

	cargoID := sys.Collisions[ch].ID
	pickup := sys.Collisions[ch].CachedAnchor
	if occ, ok := grid.OccupantAt(pickup); !ok || occ != cargoID {
		t.Fatalf("fixture: cargo must hold its ground cell before pickup, got %d %v", occ, ok)
	}

	// Bake the pickup rectangle into the class layer the way a path request
	// 200 ticks later would: the cargo's creation-stamp tick is older than the
	// watermark, so the occupant-age gate blocks its cells [04 R-PATH-01 §14].
	sys.BindWorld(w)
	layer := sys.ensureLayerRegistry().For(content.CanonicalKey("kbot2x2"), sys.ProfileFor(ch))
	sys.ensureLayerRegistry().ReviseFor(content.CanonicalKey("kbot2x2"), sys.ProfileFor(ch), th, 200)
	if got := layer.Value(pickup.X, pickup.Z); got != LayerBlocked {
		t.Fatalf("stale cargo anchor = %d, want blocked(0) — fixture did not arm the gate", got)
	}

	// Airborne carrier, then the attach with request mode 0 [04 R-AIR-01 §9].
	sys.SetMoverMode(w.Unit(th), 2)
	if !AttachCargoMode(w, th, ch, 0, 0) {
		t.Fatalf("attach failed")
	}
	runMovementTick(sys, 1, w)
	for z := int32(0); z < ter.CellH; z++ {
		for x := int32(0); x < ter.CellW; x++ {
			if occ, ok := grid.OccupantAt(Cell{X: x, Z: z}); ok && occ == cargoID {
				t.Errorf("carried cargo holds ground cell (%d,%d); mode 0 stamps nothing [04 R-COLL-01 §4]", x, z)
			}
		}
	}

	// The pickup is a footprint clear, so it owes the cells it released a
	// class-layer reclassification [04 R-COLL-01 §4]; otherwise the cell a
	// transport lifted a unit out of keeps blocking path search for the rest
	// of the battle, exactly as a building's cells do [04 R-PATH-01 §14].
	if got := layer.Value(pickup.X, pickup.Z); got == LayerBlocked {
		t.Errorf("pickup cell still classifies blocked after the cargo left the ground plane [04 R-COLL-01 §4]")
	}

	// Carry it away: the carried-position setter follows the carrier.
	dx, dz := world.CellToWorld(12), world.CellToWorld(12)
	w.Unit(th).X, w.Unit(th).Z = dx, dz
	if fl := sys.Flights[th]; fl != nil {
		fl.X, fl.Z = int32(dx.Raw()), int32(dz.Raw())
	}
	if coll := sys.Collisions[th]; coll != nil {
		coll.X, coll.Z = int32(dx.Raw()), int32(dz.Raw())
	}
	runMovementTick(sys, 2, w)

	ok, msg := sys.TryUnload(w, th, ch, dx, dz)
	if !ok {
		t.Fatalf("unload refused: %s", msg)
	}
	runMovementTick(sys, 3, w)
	drop := sys.Collisions[ch].CachedAnchor
	occ, held := grid.OccupantAt(drop)
	if !held || occ != cargoID {
		t.Errorf("released cargo must stamp its ground cell at %+v, got %d %v [04 R-AIR-01 §10]", drop, occ, held)
	}
}
