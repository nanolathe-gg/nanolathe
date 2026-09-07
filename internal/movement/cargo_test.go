package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestScriptCargoCommitRelinksSameCarrierAndDropsAtCurrentPosition(t *testing.T) {
	terrain := syntheticFlat(32, 32)
	grid := NewOccupancyGrid()
	fallback := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := NewSystem(terrain, fallback, grid)
	class := &content.MovementClass{FootprintX: 1, FootprintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys.SetClasses(map[string]*content.MovementClass{content.CanonicalKey("kbot2x2"): class})
	w := newMovementFixtureWorld(8)
	carrierDef := defForTransport("arm_atlas")
	carrierDef.BMCode = true
	cargoDef := defForCargo("armflea", 1)
	cargoDef.BMCode = true
	cargoDef.MovementClass = "kbot2x2"
	x, z := world.CellToWorld(8), world.CellToWorld(8)
	carrierHandle, _ := w.Create(carrierDef, 0, x, terrain.HeightAt(x, z), z)
	cargoHandle, _ := w.Create(cargoDef, 0, world.CellToWorld(12), terrain.HeightAt(x, z), world.CellToWorld(8))
	carrier, cargo := w.Unit(carrierHandle), w.Unit(cargoHandle)
	secondHandle, _ := w.Create(cargoDef, 0, world.CellToWorld(14), terrain.HeightAt(x, z), world.CellToWorld(8))
	second := w.Unit(secondHandle)
	sys.BindWorld(w)
	sys.EnsureUnit(carrier)
	sys.EnsureUnit(cargo)
	sys.EnsureUnit(second)
	if !sys.ScriptAttachCargo(w, carrier.Handle, second.Handle, 2, 0) {
		t.Fatal("second cargo attach failed")
	}

	initial := sys.Collisions[cargo.Handle].CachedAnchor
	if _, ok := grid.OccupantAtPlane(PlaneGround, initial); !ok {
		t.Fatal("fixture cargo did not occupy its initial ground cell")
	}
	// A shipped operand value of zero removes occupancy before the authored
	// low-two-bit mode-2 reattach below.
	if !sys.ScriptAttachCargo(w, carrier.Handle, cargo.Handle, 3, 0) {
		t.Fatal("stock-mode scripted attach failed")
	}
	sys.SyncCarriedMotion(w)
	if _, ok := grid.OccupantAtPlane(PlaneGround, initial); ok {
		t.Fatal("mode-0 attach retained the old ground stamp")
	}
	if !sys.ScriptAttachCargo(w, carrier.Handle, cargo.Handle, 3, -1) || cargo.Move.Mode != 3 {
		t.Fatal("negative script mode must retain low two bits, not the host unchanged sentinel")
	}
	if !sys.ScriptAttachCargo(w, carrier.Handle, cargo.Handle, 3, 6) {
		t.Fatal("first scripted attach failed")
	}
	if !sys.ScriptAttachCargo(w, carrier.Handle, cargo.Handle, -1, 6) {
		t.Fatal("same-carrier scripted reattach failed")
	}
	if got := cargo.Attachment.AttachPiece; got != -1 {
		t.Fatalf("reattach piece = %d, want -1 no-piece [04 R-COB-03 §5]", got)
	}
	if got := carrier.Attachment.Cargo; len(got) != 2 || got[0] != cargo.Handle || got[1] != second.Handle {
		t.Fatalf("same-carrier relink list = %v, want head cargo %d then second %d [04 R-COB-03 §5]", got, cargo.Handle, second.Handle)
	}
	if got := cargo.Move.Mode; got != 2 {
		t.Fatalf("attach mover mode = %d, want third operand low two bits 2", got)
	}

	if sys.ScriptDropCargo(w, carrier.Handle, cargo.Handle) {
		t.Fatal("blocked current-position drop succeeded")
	}
	if cargo.Attachment.Carrier != carrier.Handle || len(carrier.Attachment.Cargo) != 2 {
		t.Fatal("blocked drop changed cargo linkage")
	}
	// The opcode validates the cargo's current hang point, not the order's
	// requested destination. Use a clear point away from the carrier's ground
	// footprint to make that gate observable.
	carrier.X, carrier.Z = world.CellToWorld(16), world.CellToWorld(16)
	sys.SyncCarriedMotion(w)
	carried := sys.Collisions[cargo.Handle].CachedAnchor
	if _, ok := grid.OccupantAtPlane(PlaneAir, carried); !ok {
		t.Fatal("mode-2 carried cargo did not stamp the air plane")
	}
	if !sys.ScriptDropCargo(w, carrier.Handle, cargo.Handle) {
		t.Fatal("scripted drop at carried current position failed")
	}
	if cargo.Attachment.Carrier != 0 || cargo.Attachment.AttachPiece != -1 {
		t.Fatalf("drop linkage = %+v, want uncarried no-piece [04 R-COB-03 §5]", cargo.Attachment)
	}
	if got := cargo.Move.Mode; got != 1 {
		t.Fatalf("drop mover mode = %d, want grounded mode 1", got)
	}
	runMovementTick(sys, 1, w)
	dropped := sys.Collisions[cargo.Handle].CachedAnchor
	if _, ok := grid.OccupantAtPlane(PlaneAir, carried); ok {
		t.Fatal("drop retained the carried air stamp")
	}
	if _, ok := grid.OccupantAtPlane(PlaneGround, dropped); !ok {
		t.Fatal("normal movement after drop did not stamp the ground plane")
	}
}
