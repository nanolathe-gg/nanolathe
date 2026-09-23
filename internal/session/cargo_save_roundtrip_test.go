package session

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestRetailSaveRoundTripPreservesScriptedNoPieceCargo exercises the stock
// ARMTSHIP callback that first attaches cargo to its crane and then reattaches
// it to piece 0xFF. The live attachment byte is sign-extended to -1; saving,
// loading, and the later scripted drop must preserve that valid carried state
// [fmt cob][04 R-COB-03 §5][04 R-UNIT-06 §3][08 R-SAVE-02 §6].
func TestRetailSaveRoundTripPreservesScriptedNoPieceCargo(t *testing.T) {
	f := loadRetailFixture(t)
	src := f.session(t)
	src.State = StateBattle
	carrierDef, ok := f.cat.Unit("ARMTSHIP")
	if !ok || carrierDef == nil {
		t.Fatal("retail fixture has no ARMTSHIP")
	}
	const cargoName = "ARMPW"
	cargoDef, ok := f.cat.Unit(cargoName)
	if !ok || cargoDef == nil {
		t.Fatalf("retail fixture has no %s", cargoName)
	}

	anchor := retailUnit(src, 0, retailARM)
	if anchor == nil {
		t.Fatal("retail fixture has no ARM commander anchor")
	}
	carrierHandle, err := src.Units.Create(carrierDef, 0, anchor.X, anchor.Y, anchor.Z)
	if err != nil {
		t.Fatalf("create ARMTSHIP: %v", err)
	}
	cargoX := anchor.X + world.CellToWorld(4)
	cargoHandle, err := src.Units.Create(cargoDef, 0, cargoX, src.World.HeightAt(cargoX, anchor.Z), anchor.Z)
	if err != nil {
		t.Fatalf("create %s: %v", cargoName, err)
	}
	src.Movement.EnsureUnit(src.Units.Unit(carrierHandle))
	src.Movement.EnsureUnit(src.Units.Unit(cargoHandle))
	src.Econ.UnitBuckets(carrierHandle)
	src.Econ.UnitBuckets(cargoHandle)
	carrier, cargo := src.Units.Unit(carrierHandle), src.Units.Unit(cargoHandle)
	if !src.Movement.HasMover(carrierHandle) || !src.Movement.HasMover(cargoHandle) {
		t.Fatalf("new transport pair lacks movers: carrier=%t cargo=%t", src.Movement.HasMover(carrierHandle), src.Movement.HasMover(cargoHandle))
	}
	bridge := carrier.ScriptBridge()
	if bridge == nil {
		t.Fatal("ARMTSHIP has no bound callback bridge")
	}
	result := bridge.DeferredWake("TransportPickup", []int32{int32(cargoHandle)}, nil)
	if !result.Started {
		t.Fatal("ARMTSHIP has no TransportPickup callback")
	}
	for tick := 0; tick < 4096 && (cargo.Attachment.Carrier != carrierHandle || cargo.Attachment.AttachPiece != -1); tick++ {
		bridge.Drain(1)
	}
	if cargo.Attachment.Carrier != carrierHandle || cargo.Attachment.AttachPiece != -1 {
		t.Fatalf("stock pickup linkage = carrier %d piece %d, want carrier %d no-piece -1 [04 R-COB-03 §5]", cargo.Attachment.Carrier, cargo.Attachment.AttachPiece, carrierHandle)
	}
	// Let the carried-position commit publish the attachment's mover mode and
	// clear the cargo's old ground footprint before taking the save.
	src.stepOneSubTick(src.Clock.BeginSubTick())
	if cargo.Attachment.Carrier != carrierHandle || cargo.Attachment.AttachPiece != -1 || cargo.Move.ModeMirror != 0 {
		t.Fatalf("committed pickup = carrier %d piece %d mode mirror %d, want carried no-piece mode 0", cargo.Attachment.Carrier, cargo.Attachment.AttachPiece, cargo.Move.ModeMirror)
	}

	summary := RetailBattleSummary(src, "no-piece cargo", "0", src.Skirmish.UnitLimit)
	inputs, err := src.RetailBattleSaveInputs(summary, save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "CARGO.SAV")
	if err := src.WriteRetailSave(path, inputs); err != nil {
		t.Fatalf("write scripted cargo save: %v", err)
	}
	loaded, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: f.fs, Catalog: f.cat,
		SimSeed: 17, CRTSeed: 19, UnitLimit: src.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatalf("load scripted cargo save: %v", err)
	}
	if loaded.Route != RetailLoadRouteBattleRestoration || loaded.Battle == nil || loaded.Battle.Session == nil {
		t.Fatalf("load result = route %d battle %v", loaded.Route, loaded.Battle)
	}
	dst := loaded.Battle.Session
	restoredCarrierHandle := loaded.Battle.StableUnit[uint16(carrierHandle)]
	restoredCargoHandle := loaded.Battle.StableUnit[uint16(cargoHandle)]
	restoredCarrier, restoredCargo := dst.Units.Unit(restoredCarrierHandle), dst.Units.Unit(restoredCargoHandle)
	if restoredCarrier == nil || restoredCargo == nil {
		t.Fatalf("restored transport pair is missing: carrier=%d cargo=%d", restoredCarrierHandle, restoredCargoHandle)
	}
	if restoredCargo.Attachment.Carrier != restoredCarrierHandle || restoredCargo.Attachment.AttachPiece != -1 || len(restoredCarrier.Attachment.Cargo) == 0 || restoredCarrier.Attachment.Cargo[0] != restoredCargoHandle {
		t.Fatalf("restored linkage = carrier %d piece %d list %v, want no-piece cargo %d", restoredCargo.Attachment.Carrier, restoredCargo.Attachment.AttachPiece, restoredCarrier.Attachment.Cargo, restoredCargoHandle)
	}
	// The drop opcode validates the cargo's current carried point. Move the
	// transport to a clear authored cell and run the carried-position commit,
	// just as ordinary transport movement does before TransportDrop.
	foundDrop := false
	for cellZ := int32(8); cellZ+8 < dst.World.CellH && !foundDrop; cellZ++ {
		for cellX := int32(8); cellX+8 < dst.World.CellW; cellX++ {
			x, z := world.CellToWorld(cellX), world.CellToWorld(cellZ)
			restoredCarrier.X, restoredCarrier.Y, restoredCarrier.Z = x, dst.World.HeightAt(x, z), z
			dst.Movement.SyncCarriedMotion(dst.Units)
			if dst.Movement.ValidateUnloadSite(dst.Units, restoredCargoHandle, restoredCargo.X, restoredCargo.Z, dst.World) {
				foundDrop = true
				break
			}
		}
	}
	if !foundDrop {
		t.Fatalf("retail map %dx%d has no clear scripted drop point for restored %s: mode=%d profile=%+v position=(%d,%d)", dst.World.CellW, dst.World.CellH, restoredCargo.Def.UnitName, restoredCargo.Move.Mode, dst.Movement.ProfileFor(restoredCargoHandle), restoredCargo.X, restoredCargo.Z)
	}

	restoredBridge := restoredCarrier.ScriptBridge()
	if restoredBridge == nil {
		t.Fatal("restored ARMTSHIP has no callback bridge")
	}
	drop := restoredBridge.DeferredWakeArgs("TransportDrop", 1, [4]int32{int32(restoredCargoHandle), 0, 0, 0}, nil)
	if !drop.Started {
		t.Fatal("restored ARMTSHIP has no TransportDrop callback")
	}
	for tick := 0; tick < 4096 && restoredCargo.Attachment.Carrier != 0; tick++ {
		restoredBridge.Drain(1)
	}
	if restoredCargo.Attachment.Carrier != 0 {
		t.Fatalf("restored scripted drop retained carrier %d", restoredCargo.Attachment.Carrier)
	}
	if restoredCargo.Attachment.AttachPiece != -1 || len(restoredCarrier.Attachment.Cargo) != 0 {
		t.Fatalf("restored scripted drop left piece %d cargo list %v", restoredCargo.Attachment.AttachPiece, restoredCarrier.Attachment.Cargo)
	}
}
