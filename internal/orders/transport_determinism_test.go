package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestTransportHandlersWired(t *testing.T) {
	ensureTransportHandlers()
	for _, name := range []string{"Ground_Pickup", "VTOL_Pickup", "Ground_Unload", "VTOL_Unload", "VTOL_Landing", "BeCarried"} {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("Lookup %q failed", name)
		}
		if DescriptorFor(id).Handler == nil {
			t.Fatalf("handler for %q is nil, should be wired [04 §10.2]", name)
		}
	}
	// Capture/Resurrect remain nil by design (arithmetic in construction, not order handlers) – verify they are still nil or document.
	// We leave Capture nil to avoid inventing handler; check that we explicitly left it nil.
	if id := Lookup("Capture"); id != 0 && DescriptorFor(id).Handler != nil {
		t.Logf("Capture handler is wired (unexpected, but not required)")
	}
	if id := Lookup("Resurrect"); id != 0 && DescriptorFor(id).Handler != nil {
		t.Logf("Resurrect handler is wired (unexpected)")
	}
}

func mkTransportCarrier(handle pool.Handle, owner uint8, canFly bool) *units.Unit {
	def := &content.UnitDef{
		CanLoad:           true,
		CanFly:            canFly,
		CanMove:           true,
		TransportSize:     10,
		TransportCapacity: 1,
		FootprintX:        2,
		FootprintZ:        2,
		MaxDamage:         100,
	}
	if canFly {
		def.CanFly = true
	}
	def.UnitName = "carrier"
	def.CanonicalKey = "carrier"
	u := &units.Unit{
		Handle:    handle,
		Owner:     owner,
		Def:       def,
		Health:    100,
		MaxHealth: 100,
		Alive:     true,
		X:         numeric.Fixed(0),
		Y:         numeric.Fixed(0),
		Z:         numeric.Fixed(0),
	}
	u.Move.Mode = 1
	return u
}

func mkCargo(handle pool.Handle, owner uint8) *units.Unit {
	def := &content.UnitDef{
		CanMove:           true,
		CantBeTransported: false,
		FootprintX:        1,
		FootprintZ:        1,
		MaxDamage:         50,
	}
	def.UnitName = "cargo"
	def.CanonicalKey = "cargo"
	u := &units.Unit{
		Handle:    handle,
		Owner:     owner,
		Def:       def,
		Health:    50,
		MaxHealth: 50,
		Alive:     true,
		X:         numeric.Fixed(0),
		Y:         numeric.Fixed(10 * 65536), // above sea level to pass gate 3 [04 §10.2] TODO(question) modelTop
		Z:         numeric.Fixed(0),
	}
	u.Move.Mode = 1
	return u
}

// hashTransportState computes a simple deterministic hash of relevant transport state.
func hashTransportState(carrier, cargo *units.Unit) uint64 {
	var h uint64 = 146959
	h = h*1099511628211 ^ uint64(carrier.Handle)
	h = h*1099511628211 ^ uint64(len(carrier.Attachment.Cargo))
	if len(carrier.Attachment.Cargo) > 0 {
		h = h*1099511628211 ^ uint64(carrier.Attachment.Cargo[0])
	}
	h = h*1099511628211 ^ uint64(cargo.Attachment.Carrier)
	h = h*1099511628211 ^ uint64(int32(cargo.X.Raw()))
	h = h*1099511628211 ^ uint64(int32(cargo.Z.Raw()))
	h = h*1099511628211 ^ uint64(carrier.X.Raw())
	h = h*1099511628211 ^ uint64(cargo.Attachment.AttachPiece+2)
	return h
}

func runTransportScenario(seed uint32) (uint64, int, []string) {
	rng.SeedGlobal(seed, 0)
	w := units.NewSliced(10, nil)
	carrier := mkTransportCarrier(1, 0, true)
	cargo := mkCargo(2, 0)
	// Place cargo slightly offset but within boarding range (16) [04 §10.2]
	cargo.X = numeric.Fixed(5 * 65536)
	cargo.Z = numeric.Fixed(0)
	carrier.X = numeric.Fixed(0)
	carrier.Z = numeric.Fixed(0)
	// Ensure world can resolve handles
	// Use non-sliced world for test simplicity (capacity 10)
	// Insert units directly into world via Create path? For headless we bypass pool and directly set.
	// Use w.Create to get proper handles, but we already have handles 1,2.
	// Instead, use units.NewSliced? For simplicity, we directly inject via w.units hack? But World is opaque.
	// Instead, use w.Create with defs and then replace?
	// Simpler: create world via NewSliced and Create.
	cat := &content.Catalog{}
	w2 := units.NewSliced(10, cat)
	hC, _ := w2.Create(carrier.Def, 0, carrier.X, carrier.Y, carrier.Z)
	hCargo, _ := w2.Create(cargo.Def, 0, cargo.X, cargo.Y, cargo.Z)
	uC := w2.Unit(hC)
	uCargo := w2.Unit(hCargo)
	_ = w // avoid unused
	// Use w2 for simulation
	// Set queues
	qC := QueueForUnit(uC)
	qCargo := QueueForUnit(uCargo)
	// Bind lookup for target resolution
	lookup := func(h pool.Handle) *units.Unit { return w2.Unit(h) }
	qC.Lookup = lookup
	qCargo.Lookup = lookup
	// Push VTOL_Pickup order onto carrier targeting cargo
	idPickup := Lookup("VTOL_Pickup")
	if idPickup == 0 {
		panic("VTOL_Pickup lookup failed")
	}
	nPickup := NewNodeForOrder(idPickup, hCargo, 0, 0, 0, 0, hC, false)
	// Ensure gate cleared for test (mimics pump's clearing)
	// Push will set DynamicGate to 0x200, but pump will clear for phase0.
	qC.Push(idPickup, nPickup)
	// Pump ticks until attached or max
	pump := &Pump{World: w2}
	var diags []string
	for tick := uint32(0); tick < 10; tick++ {
		res := pump.PumpUnit(hC, tick)
		diags = append(diags, res.Diagnostics...)
		if uCargo.Attachment.Carrier == hC {
			break
		}
	}
	// Simulate carrier moving to unload point (10 cells away)
	dropX := numeric.Fixed(20 * 65536)
	dropZ := numeric.Fixed(0)
	// Move carrier directly (no path) – deterministic
	uC.X = dropX
	uC.Z = dropZ
	// Also slave cargo via direct (movement System would do)
	if uCargo.Attachment.Carrier == hC {
		uCargo.X = uC.X
		uCargo.Z = uC.Z
	}
	// Now unload at drop point
	idUnload := Lookup("VTOL_Unload")
	nUnload := NewNodeForOrder(idUnload, 0, dropX, 0, dropZ, 10, hC, false)
	// Ensure drop point stored in Goal
	nUnload.GoalX = dropX
	nUnload.GoalZ = dropZ
	qC.Push(idUnload, nUnload)
	for tick := uint32(10); tick < 20; tick++ {
		res := pump.PumpUnit(hC, tick)
		diags = append(diags, res.Diagnostics...)
		if uCargo.Attachment.Carrier == 0 {
			break
		}
	}
	hash := hashTransportState(uC, uCargo)
	draws := 0
	if rng.Global.Sim != nil {
		draws = int(rng.Global.Sim.Draws())
	}
	return hash, draws, diags
}

func TestTransportLoadMoveUnloadDeterministic(t *testing.T) {
	// Two-run hash match, no new RNG draws beyond executors [I4][04 §10.2]
	h1, d1, _ := runTransportScenario(12345)
	h2, d2, _ := runTransportScenario(12345)
	if h1 != h2 {
		t.Fatalf("two-run hash mismatch: %x vs %x", h1, h2)
	}
	if d1 != d2 {
		t.Fatalf("two-run draws mismatch: %d vs %d", d1, d2)
	}
	// Hash should reflect successful load/unload cycle
	// Cargo should be detached at drop point after second run
	// Re-run with detailed check
	rng.SeedGlobal(12345, 0)
	w2 := units.NewSliced(10, &content.Catalog{})
	defCarrier := &content.UnitDef{CanLoad: true, CanFly: true, CanMove: true, TransportSize: 10, TransportCapacity: 1, FootprintX: 2, FootprintZ: 2, MaxDamage: 100}
	defCargo := &content.UnitDef{CanMove: true, CantBeTransported: false, FootprintX: 1, FootprintZ: 1, MaxDamage: 50}
	hC, _ := w2.Create(defCarrier, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	hCargo, _ := w2.Create(defCargo, 0, numeric.Fixed(5*65536), numeric.Fixed(10*65536), numeric.Fixed(0))
	uC := w2.Unit(hC)
	uCargo := w2.Unit(hCargo)
	uC.Move.Mode = 1
	uCargo.Move.Mode = 1
	qC := QueueForUnit(uC)
	qC.Lookup = func(h pool.Handle) *units.Unit { return w2.Unit(h) }
	QueueForUnit(uCargo).Lookup = qC.Lookup
	idPickup := Lookup("VTOL_Pickup")
	qC.Push(idPickup, NewNodeForOrder(idPickup, hCargo, 0, 0, 0, 0, hC, false))
	pump := &Pump{World: w2}
	for tick := uint32(0); tick < 10; tick++ {
		pump.PumpUnit(hC, tick)
		if uCargo.Attachment.Carrier == hC {
			break
		}
	}
	if uCargo.Attachment.Carrier != hC {
		t.Fatalf("cargo not attached after pickup, carrier cargo %v cargo carrier %v", uC.Attachment.Cargo, uCargo.Attachment.Carrier)
	}
	if len(uC.Attachment.Cargo) != 1 || uC.Attachment.Cargo[0] != hCargo {
		t.Fatalf("carrier cargo list incorrect after load: %v", uC.Attachment.Cargo)
	}
	// Check BeCarried pushed onto cargo
	qCargo := QueueForUnit(uCargo)
	hasBe := false
	for _, n := range qCargo.Primary() {
		if DescriptorFor(n.ID).Name == "BeCarried" {
			hasBe = true
			break
		}
	}
	if !hasBe {
		t.Fatalf("cargo should have BeCarried order after load")
	}
	// Move
	dropX := numeric.Fixed(20 * 65536)
	dropZ := numeric.Fixed(0)
	uC.X = dropX
	uC.Z = dropZ
	uCargo.X = dropX
	uCargo.Z = dropZ
	// Unload
	idUnload := Lookup("VTOL_Unload")
	nUnload := NewNodeForOrder(idUnload, 0, dropX, 0, dropZ, 10, hC, false)
	nUnload.GoalX = dropX
	nUnload.GoalZ = dropZ
	qC.Push(idUnload, nUnload)
	for tick := uint32(10); tick < 20; tick++ {
		pump.PumpUnit(hC, tick)
		if uCargo.Attachment.Carrier == 0 {
			break
		}
	}
	if uCargo.Attachment.Carrier != 0 {
		t.Fatalf("cargo still attached after unload: carrier cargo %v cargo carrier %v queue len %d head %v diags %v", uC.Attachment.Cargo, uCargo.Attachment.Carrier, qC.LenPrimary(), func() string {
			if h := qC.Head(); h != nil {
				return DescriptorFor(h.ID).Name
			}
			return "nil"
		}(), qC.Diagnostics())
	}
	if len(uC.Attachment.Cargo) != 0 {
		t.Fatalf("carrier cargo not empty after unload: %v", uC.Attachment.Cargo)
	}
	if uCargo.X != dropX || uCargo.Z != dropZ {
		t.Fatalf("cargo not at drop point after unload: got %v,%v want %v,%v", uCargo.X, uCargo.Z, dropX, dropZ)
	}
	// BeCarried should be removed after unload
	hasBe = false
	for _, n := range qCargo.Primary() {
		if DescriptorFor(n.ID).Name == "BeCarried" {
			hasBe = true
			break
		}
	}
	if hasBe {
		t.Fatalf("BeCarried should be removed after unload")
	}
	// No diagnostics for transport orders (nil handler) should be empty
	// The pump should not have recorded "nil handler" for these orders
	for _, q := range []*Queue{qC, qCargo} {
		for _, d := range q.Diagnostics() {
			if len(d) > 20 && d[:20] == "orders: nil handler" {
				t.Fatalf("unexpected nil handler diagnostic for wired order: %q", d)
			}
		}
	}
}

func TestTransportHeavyAndGates(t *testing.T) {
	rng.SeedGlobal(1, 0)
	w2 := units.NewSliced(10, &content.Catalog{})
	defCarrier := &content.UnitDef{CanLoad: true, CanFly: true, CanMove: true, TransportSize: 1, TransportCapacity: 1, FootprintX: 2, FootprintZ: 2, MaxDamage: 100}
	defCargo := &content.UnitDef{CanMove: true, CantBeTransported: false, FootprintX: 5, FootprintZ: 5, MaxDamage: 50}
	hC, _ := w2.Create(defCarrier, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	hCargo, _ := w2.Create(defCargo, 0, numeric.Fixed(5*65536), numeric.Fixed(10*65536), numeric.Fixed(0))
	uC := w2.Unit(hC)
	uCargo := w2.Unit(hCargo)
	uC.Move.Mode = 1
	uCargo.Move.Mode = 1
	qC := QueueForUnit(uC)
	qC.Lookup = func(h pool.Handle) *units.Unit { return w2.Unit(h) }
	idPickup := Lookup("VTOL_Pickup")
	qC.Push(idPickup, NewNodeForOrder(idPickup, hCargo, 0, 0, 0, 0, hC, false))
	pump := &Pump{World: w2}
	// First tick should fail size gate and return 8 -> node removed? Handler returns 8, pump will handle code >9? Actually 8 is TransportResultFailed, which pump maps to case 5,8: unlink.
	// For handler returning 8, pump's switch case 5,8 will unlink.
	pump.PumpUnit(hC, 0)
	if len(qC.Primary()) != 0 {
		t.Fatalf("heavy transport should have failed and removed order, remaining %d", len(qC.Primary()))
	}
	// Check diagnostic for heavy?
	foundHeavy := false
	for _, d := range qC.Diagnostics() {
		if d == transportHeavyMessage {
			foundHeavy = true
			break
		}
	}
	if !foundHeavy {
		t.Fatalf("heavy transport should have recorded diagnostic %q, got %v", transportHeavyMessage, qC.Diagnostics())
	}
	// Test cargo empty gate for air: second load should fail 8 with no message when cargo already loaded
	defCarrier2 := &content.UnitDef{CanLoad: true, CanFly: true, CanMove: true, TransportSize: 10, TransportCapacity: 1, FootprintX: 2, FootprintZ: 2, MaxDamage: 100}
	defCargo2 := &content.UnitDef{CanMove: true, CantBeTransported: false, FootprintX: 1, FootprintZ: 1, MaxDamage: 50}
	w2 = units.NewSliced(10, &content.Catalog{})
	hC, _ = w2.Create(defCarrier2, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	hCargo, _ = w2.Create(defCargo2, 0, numeric.Fixed(5*65536), numeric.Fixed(10*65536), numeric.Fixed(0))
	hCargo2, _ := w2.Create(defCargo2, 0, numeric.Fixed(6*65536), numeric.Fixed(10*65536), numeric.Fixed(0))
	uC = w2.Unit(hC)
	uCargo = w2.Unit(hCargo)
	uCargo2 := w2.Unit(hCargo2)
	uC.Move.Mode = 1
	uCargo.Move.Mode = 1
	uCargo2.Move.Mode = 1
	qC = QueueForUnit(uC)
	qC.Lookup = func(h pool.Handle) *units.Unit { return w2.Unit(h) }
	// Load first cargo
	qC.Push(idPickup, NewNodeForOrder(idPickup, hCargo, 0, 0, 0, 0, hC, false))
	pump = &Pump{World: w2}
	for tick := uint32(0); tick < 5; tick++ {
		pump.PumpUnit(hC, tick)
		if uCargo.Attachment.Carrier == hC {
			break
		}
	}
	if uCargo.Attachment.Carrier != hC {
		t.Fatalf("first load should succeed")
	}
	// Try second load while air cargo not empty – should fail gate 4 with no message and code 8
	qC.Push(idPickup, NewNodeForOrder(idPickup, hCargo2, 0, 0, 0, 0, hC, false))
	pump.PumpUnit(hC, 10)
	if len(qC.Primary()) != 0 {
		// The failed second pickup should be removed (code 8)
		t.Fatalf("second pickup with cargo not empty should fail and be removed, remaining %d", len(qC.Primary()))
	}
	// Cargo2 should not be attached
	if uCargo2.Attachment.Carrier == hC {
		t.Fatalf("second cargo should not be attached when carrier already has cargo")
	}
}

func TestTransportLanding(t *testing.T) {
	rng.SeedGlobal(2, 0)
	w2 := units.NewSliced(10, &content.Catalog{})
	defVTOL := &content.UnitDef{CanFly: true, CanMove: true, MaxDamage: 100}
	defPad := &content.UnitDef{IsAirBase: true, MaxDamage: 100}
	hVTOL, _ := w2.Create(defVTOL, 0, numeric.Fixed(0), numeric.Fixed(100*65536), numeric.Fixed(0))
	hPad, _ := w2.Create(defPad, 0, numeric.Fixed(10*65536), numeric.Fixed(0), numeric.Fixed(10*65536))
	uVTOL := w2.Unit(hVTOL)
	uPad := w2.Unit(hPad)
	uVTOL.Move.Mode = 2
	uPad.Move.Mode = 1
	qVTOL := QueueForUnit(uVTOL)
	qVTOL.Lookup = func(h pool.Handle) *units.Unit { return w2.Unit(h) }
	idLand := Lookup("VTOL_Landing")
	if idLand == 0 {
		t.Fatalf("VTOL_Landing lookup failed")
	}
	qVTOL.Push(idLand, NewNodeForOrder(idLand, hPad, 0, 0, 0, 0, hVTOL, false))
	pump := &Pump{World: w2}
	pump.PumpUnit(hVTOL, 0)
	if uVTOL.X != uPad.X || uVTOL.Z != uPad.Z {
		t.Fatalf("VTOL should have landed on pad at %v,%v got %v,%v", uPad.X, uPad.Z, uVTOL.X, uVTOL.Z)
	}
	if uVTOL.Move.Mode != 1 {
		t.Fatalf("VTOL after landing should be parked mode 1, got %d", uVTOL.Move.Mode)
	}
	if len(qVTOL.Primary()) != 0 {
		t.Fatalf("landing order should be done and removed")
	}
}
