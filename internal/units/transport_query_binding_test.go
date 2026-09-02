package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// transportQueryProgram exposes the two transport query opcodes as two
// authored scripts so a test can observe what a script would see. The opcodes
// are the documented COB encoding [04 §4.3][04 §4.4]; nothing here is copied
// retail data. `Carried` pushes a constant identifier and asks whether it is in
// this unit's cargo list; `CarrierOf` asks who is carrying this unit.
func transportQueryProgram(probe uint32) *cob.Program {
	return &cob.Program{
		Code: []uint32{
			// Carried at word 0.
			0x10021001, probe, // push the identifier under test
			0x10044000, // cargo-membership query
			0x10065000, // return
			// CarrierOf at word 4.
			0x10045000, // carrier-identity query
			0x10065000,
			// Create at word 6.
			0x10021001, 0,
			0x10065000,
		},
		Scripts:     map[string]int{"Carried": 0, "CarrierOf": 4, "Create": 6},
		ScriptsByID: []int{0, 4, 6},
		Pieces:      []string{"base"},
	}
}

func transportQueryDef(name string, probe uint32) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
		UnitName:         name,
		MaxDamage:        100,
		Limit:            -1,
		Script:           transportQueryProgram(probe),
	}
}

// runQuery starts one of the two query scripts and returns the value it left
// on the thread's stack.
func runQuery(t *testing.T, u *Unit, script string) int32 {
	t.Helper()
	vm := u.GetScript()
	if vm == nil {
		t.Fatal("fixture unit has no script VM")
	}
	if !vm.StartByName(script, nil) {
		t.Fatalf("could not start %s", script)
	}
	vm.Drain(1)
	for i := range vm.Threads {
		if vm.Threads[i].Stack[0] != 0 {
			return vm.Threads[i].Stack[0]
		}
	}
	return 0
}

// TestTransportQueriesReadTheUnitsOwnLinkage locks the binding half of
// [04 R-COB-03 §5]: the two queries answer from the unit's own cargo list and
// carrier back-pointer, the same fields the attach and drop commit writes
// [04 R-AIR-01 §9]. Before this they were hard-coded to zero, so a carrier
// could not tell its own cargo apart from a stranger.
func TestTransportQueriesReadTheUnitsOwnLinkage(t *testing.T) {
	// The carrier probes for the identifier its first cargo will take.
	carrierDef := transportQueryDef("carrierfixture", 2)
	loneDef := transportQueryDef("lonefixture", 2)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		carrierDef.CanonicalKey: carrierDef,
		loneDef.CanonicalKey:    loneDef,
	}}
	w := newFixtureWorld(4, cat)

	carrierHandle, err := w.Create(carrierDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create carrier: %v", err)
	}
	carrier := w.Unit(carrierHandle)
	loneHandle, err := w.Create(loneDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create lone unit: %v", err)
	}
	lone := w.Unit(loneHandle)

	// An empty list answers 0 for every identifier, and neither unit is
	// carried yet.
	if got := runQuery(t, carrier, "Carried"); got != 0 {
		t.Fatalf("an empty cargo list answered %d, want 0", got)
	}
	if got := runQuery(t, carrier, "CarrierOf"); got != 0 {
		t.Fatalf("an uncarried carrier answered %d, want 0", got)
	}

	// Two cargo units, pushed at the head in attach order — the shared cargo
	// representation internal/movement writes [04 R-COB-03 §5].
	cargoA := pool.Handle(2)
	cargoB := pool.Handle(3)
	carrier.Attachment.Cargo = []pool.Handle{cargoB, cargoA}
	if got := runQuery(t, carrier, "Carried"); got != 1 {
		t.Fatalf("a unit in the cargo list answered %d, want 1", got)
	}

	// The lone unit's list stays empty, so the same identifier is not in it.
	if got := runQuery(t, lone, "Carried"); got != 0 {
		t.Fatalf("a lone unit answered %d for another carrier's cargo, want 0", got)
	}

	// The carrier back-pointer is what the zero-argument query follows.
	lone.Attachment.Carrier = carrierHandle
	if got := runQuery(t, lone, "CarrierOf"); got != int32(carrierHandle) {
		t.Fatalf("carried unit answered %d, want its carrier %d", got, carrierHandle)
	}
}
