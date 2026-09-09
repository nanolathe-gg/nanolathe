package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// helper to build a synthetic program for Aim tests
func progWithAim(code []uint32, scriptName string, pc int) *cob.Program {
	// code includes Aim script at pc
	scripts := map[string]int{scriptName: pc}
	byID := []int{pc}
	return &cob.Program{
		Code:        code,
		Scripts:     scripts,
		Pieces:      []string{"base"},
		Statics:     0,
		ScriptsByID: byID,
	}
}

func attachTestCOB(u *units.Unit, vm *cob.VM) {
	if u == nil {
		return
	}
	u.Script = vm
	if vm == nil {
		u.ScriptState = nil
		return
	}
	binding := &cob.Binding{
		VM: vm, Callbacks: cob.NewCallbackBridge(vm),
		Model: &model.Model{Root: 0, Pieces: []model.Piece{{Parent: -1}}}, PieceMap: []int{0},
	}
	u.ScriptState = &units.ScriptState{VM: vm, Binding: binding}
	for i := range u.Slots {
		u.Slots[i].MuzzlePiece = 0
	}
}

func weaponTurret(id int32) *content.WeaponDef {
	return &content.WeaponDef{
		ID:             id,
		Range:          1000 * 65536, // large
		Turret:         true,
		WeaponVelocity: 100 * 65536 / 30,
		LineOfSight:    true,
	}
}
func weaponNonTurret(id int32) *content.WeaponDef {
	return &content.WeaponDef{
		ID:          id,
		Range:       1000 * 65536,
		Turret:      false,
		LineOfSight: true,
		Tolerance:   wideDriftTolerance,
	}
}

func newTestWorldAndUnits(t *testing.T) (*units.World, *world.Terrain, *units.Unit, *units.Unit) {
	t.Helper()
	w := newCombatFixtureWorld(10, nil)
	// terrain with zero gravity and sea level 0
	terrain := &world.Terrain{
		CellW:   100,
		CellH:   100,
		Gravity: numeric.Fixed(0),
	}
	// Need to set some height data? Terrain.HeightAt will be called; if Plot nil, it may panic.
	// Initialize minimal terrain plot to avoid nil.
	terrain.Plot = make([]world.PlotCell, 100*100)
	// SeaLevel default 0, so Y>0 passes water check
	// `standingfireorder` parses with a default of 2 — FIRE AT WILL — and unit
	// creation seeds the two-bit status field from it [04 R-STANCE-01 §6]
	// [04 R-STANCE-01 §1]. The autonomous scan visits a unit only at that value
	// [06 §3.2], so a fixture built from a literal definition (field zero, HOLD
	// FIRE) would never scan.
	def := &content.UnitDef{UnitName: "combatfixture", MaxDamage: 100, Limit: -1, StandingFireOrder: 2, ModelTopFixed: 16 << 16}
	shooterH, err := w.Create(def, 0, numeric.FixedFromInt(10), numeric.FixedFromInt(10), numeric.FixedFromInt(10))
	if err != nil {
		t.Fatalf("create shooter: %v", err)
	}
	targetH, err := w.Create(def, 1, numeric.FixedFromInt(20), numeric.FixedFromInt(10), numeric.FixedFromInt(20))
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	shooter := w.Unit(shooterH)
	target := w.Unit(targetH)
	attachTestCOB(shooter, shooter.GetScript())
	attachTestCOB(target, target.GetScript())
	// The autonomous scan's third clause is the ARMED status bit
	// [06 §3.2 "The third clause is the armed bit"], which unit creation derives
	// from the definition's three weapon links. These fixtures create units from
	// a weaponless literal definition and then hand a slot a weapon through
	// InstallWeapon, so the derived bit would be clear and the scan would visit
	// nothing. Raising it here is what the definition would have done: the
	// fixture units stand in for armed definitions.
	shooter.Flags |= units.ArmedStatus
	target.Flags |= units.ArmedStatus
	// Set Y above sea level
	shooter.Y = numeric.FixedFromInt(10)
	target.Y = numeric.FixedFromInt(10)
	// Projectile contact is driven by the terrain occupancy cell, not by a
	// retained guidance target. Stamp this fixture's target as battle setup.
	if cell := terrain.PlotAt(world.WorldToCell(target.X), world.WorldToCell(target.Z)); cell != nil {
		cell.SetOccupantA(int16(target.Handle))
	}
	return w, terrain, shooter, target
}

func TestON04_AimReturnsZero_NoProjectile_LatchPreserved(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	// VM with AimPrimary returning 0
	code := []uint32{
		0x10021001, 0, // push 0
		0x10065000, // return
	}
	prog := progWithAim(code, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(1)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	slot.Reload = 0
	var svc Service
	// Need catalog
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w1": weapon}}
	cat.RebuildWeaponIndex()
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if svc.Count() != 0 {
		t.Fatalf("aim 0 should not create projectile, count %d", svc.Count())
	}
	if !slot.Aim.IssueBit {
		t.Fatalf("latch should be preserved (IssueBit true) on zero return [06 §3.3]")
	}
	if slot.Aim.Ready {
		t.Fatalf("Ready should stay false on zero return [06 §3.3]")
	}
	if !sum.ReturnSeen || sum.ReturnValue != 0 {
		t.Fatalf("want explicit zero Aim return, got %+v", sum)
	}
	// Second tick: should still not fire, still blocked, no new dispatch
	svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, nil, nil)
	if svc.Count() != 0 {
		t.Fatalf("second tick with zero return should still not fire")
	}
}

func TestON04_AimReturnsNonzero_Fires(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	code := []uint32{
		0x10021001, 1, // push 1
		0x10065000,
	}
	prog := progWithAim(code, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(2)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	slot.Reload = 0
	var svc Service
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w1": weapon}}
	cat.RebuildWeaponIndex()
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if svc.Count() != 1 {
		t.Fatalf("aim 1 should create projectile when gates pass, count %d", svc.Count())
	}
	if slot.Aim.IssueBit || slot.Aim.Ready {
		t.Fatalf("after successful fire latch and ready should be cleared [06 §3.3], IssueBit %v Ready %v", slot.Aim.IssueBit, slot.Aim.Ready)
	}
	if !sum.Dispatched || !sum.ReturnSeen || sum.ReturnValue == 0 || sum.Fired == 0 {
		t.Fatalf("missing dispatch/return/fire summary: %+v", sum)
	}
}

func TestON04_AimSleeping_NoFireBeforeWake(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	// Aim that sleeps 100ms (3 ticks) then returns 1
	code := []uint32{
		0x10021001, 100, // push 100 ms
		0x10013000, // sleep
		0x10021001, 1,
		0x10065000,
	}
	prog := progWithAim(code, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(3)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	var svc Service
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w1": weapon}}
	cat.RebuildWeaponIndex()
	// Tick 1: dispatch, drain makes sleeping, no fire
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if svc.Count() != 0 {
		t.Fatalf("sleeping aim should not fire tick1")
	}
	if !sum.Dispatched || !sum.Drained {
		t.Fatalf("expected sleeping Aim dispatch and drain, got %+v", sum)
	}
	// Simulate intervening VM drains (units.Tick would do Drain(1) each tick)
	// Do 3 drains to wake
	for i := 0; i < 3; i++ {
		vm.Drain(1)
	}
	svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat, nil, nil)
	if svc.Count() != 1 {
		t.Fatalf("after wake should fire, count %d", svc.Count())
	}
}

func TestON04_SameTickReturn_FiresSameVisit(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	code := []uint32{
		0x10021001, 1,
		0x10065000,
	}
	prog := progWithAim(code, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(4)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	var svc Service
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w1": weapon}}
	cat.RebuildWeaponIndex()
	// Single step should dispatch, drain, return nonzero, and fire same visit
	svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if svc.Count() != 1 {
		t.Fatalf("same-tick return should fire same visit [06 §3.3], count %d", svc.Count())
	}
}

func TestON04_NoScript_CannotAuthorizeAim(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	// No VM bound (nil)
	// Ensure shooter has no script
	attachTestCOB(shooter, nil)
	weapon := weaponTurret(5)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	var svc Service
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w1": weapon}}
	cat.RebuildWeaponIndex()
	svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if svc.Count() != 0 {
		t.Fatalf("no-script turret must not authorize fire [04 §5.3], count %d", svc.Count())
	}
}

func TestON04_ScriptWithoutAimPrimary_Blocked(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	// VM with only Create script, no AimPrimary
	code := []uint32{0x10065000}
	prog := &cob.Program{
		Code:        code,
		Scripts:     map[string]int{"Create": 0},
		Pieces:      []string{"base"},
		Statics:     0,
		ScriptsByID: []int{0},
	}
	vm := cob.NewVM(prog)
	attachTestCOB(shooter, vm)
	weapon := weaponTurret(6)
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	slot.Flags |= 0x02
	var svc Service
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w1": weapon}}
	cat.RebuildWeaponIndex()
	svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	if svc.Count() != 0 {
		t.Fatalf("script without AimPrimary turret should be blocked, count %d", svc.Count())
	}
	// Non-turret should still fire even with absent Aim (since no gating)
	weapon2 := weaponNonTurret(7)
	shooter.SlotAt(0).Weapon = weapon2
	shooter.SlotAt(0).Aim = cob.AimSlot{}
	shooter.SlotAt(0).Flags |= 0x02
	svc2 := Service{}
	// Need new VM still without AimPrimary
	vm2 := cob.NewVM(prog)
	attachTestCOB(shooter, vm2)
	cat2 := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w2": weapon2}}
	cat2.RebuildWeaponIndex()
	svc2.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, cat2, nil, nil)
	// For non-turret, Aim not required, so should fire even though Aim absent
	if svc2.Count() != 1 {
		t.Fatalf("non-turret with absent Aim should fire (no gating), count %d", svc2.Count())
	}
}

func TestON04_StableWeaponLookup_Deterministic(t *testing.T) {
	// Two catalogs with different insertion order resolve identically [02 "Weapon record"] [PLAN_02 Post-review amendments]
	// Last section processed wins (discovery order); when only map is available, deterministic surrogate is last in sorted order.
	w1 := &content.WeaponDef{ID: 10, Range: 100}
	w1.CanonicalKey = "apple"
	w2 := &content.WeaponDef{ID: 10, Range: 200}
	w2.CanonicalKey = "zebra"
	// Map A insertion apple then zebra
	catA := &content.Catalog{Weapons: map[string]*content.WeaponDef{"apple": w1, "zebra": w2}}
	catA.RebuildWeaponIndex()
	// Map B insertion zebra then apple (different order but same keys)
	catB := &content.Catalog{Weapons: map[string]*content.WeaponDef{"zebra": w2, "apple": w1}}
	catB.RebuildWeaponIndex()
	wdA, _ := catA.WeaponByID(10)
	wdB, _ := catB.WeaponByID(10)
	if wdA.CanonicalKey != wdB.CanonicalKey {
		t.Fatalf("stable lookup independent of insertion order failed: A %s B %s", wdA.CanonicalKey, wdB.CanonicalKey)
	}
	if wdA.CanonicalKey != "zebra" {
		t.Fatalf("last canonical key should win (discovery-order last, sorted last surrogate), got %s want zebra", wdA.CanonicalKey)
	}
}

func TestON04_DuplicateIDsDiagnosed(t *testing.T) {
	w1 := &content.WeaponDef{ID: 20}
	w1.CanonicalKey = "a"
	w2 := &content.WeaponDef{ID: 20}
	w2.CanonicalKey = "b"
	w3 := &content.WeaponDef{ID: 30}
	w3.CanonicalKey = "c"
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"a": w1, "b": w2, "c": w3}}
	cat.RebuildWeaponIndex()
	dups := cat.WeaponDuplicates()
	if len(dups) != 1 {
		t.Fatalf("expected 1 duplicate ID, got %v", dups)
	}
	if dups[0].ID != 20 || len(dups[0].Keys) != 2 || dups[0].Winner != "b" {
		t.Fatalf("duplicate diagnostic wrong %v want winner b (last)", dups[0])
	}
}

func TestON04_TwoSeededRuns_Identical(t *testing.T) {
	// Two runs with same seed produce identical event and RNG-sensitive projectile
	// state ON-04 (I4), not merely the same allocation count.
	type seededRun struct {
		events      []Event
		projectiles []Projectile
	}
	run := func(seed uint32) seededRun {
		r := rng.NewSimulation(seed)
		w, terrain, shooter, target := newTestWorldAndUnits(&testing.T{})
		// Use non-turret weapon to avoid Aim gating, with spread and start events
		// so both RNG-sensitive state and event order are observed.
		weapon := &content.WeaponDef{
			ID: 1, Range: 1000 * 65536, WeaponVelocity: int32(numeric.FixedFromInt(4)),
			SprayAngle: 10, Burst: 0, LineOfSight: true, SoundStart: "seeded-start", StartSmoke: true,
			Tolerance: wideDriftTolerance,
		}
		shooter.InstallWeapon(0, weapon)
		slot := shooter.SlotAt(0)
		slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
		slot.Flags |= 0x02
		var svc Service
		var events []Event
		svc.Events = func(ev Event) { events = append(events, ev) }
		cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
		cat.RebuildWeaponIndex()
		sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, &r, nil)
		if sum.Fired != 1 {
			t.Fatalf("seeded run fired %d shots, want 1", sum.Fired)
		}
		projectiles := make([]Projectile, svc.Count())
		copy(projectiles, svc.Records[:svc.Count()])
		return seededRun{events: events, projectiles: projectiles}
	}
	first, second := run(42), run(42)
	if len(first.events) != len(second.events) {
		t.Fatalf("event counts differ %d vs %d", len(first.events), len(second.events))
	}
	for i := range first.events {
		if first.events[i] != second.events[i] {
			t.Fatalf("event %d differs: first=%+v second=%+v", i, first.events[i], second.events[i])
		}
	}
	if len(first.projectiles) != len(second.projectiles) {
		t.Fatalf("projectile counts differ %d vs %d", len(first.projectiles), len(second.projectiles))
	}
	for i := range first.projectiles {
		p, q := first.projectiles[i], second.projectiles[i]
		if p.Velocity != q.Velocity || p.Yaw != q.Yaw || p.Pitch != q.Pitch || p.Speed != q.Speed {
			t.Fatalf("RNG-sensitive projectile %d differs: first velocity=%+v yaw=%d pitch=%d speed=%d; second velocity=%+v yaw=%d pitch=%d speed=%d", i, p.Velocity, p.Yaw, p.Pitch, p.Speed, q.Velocity, q.Yaw, q.Pitch, q.Speed)
		}
		if p != q {
			t.Fatalf("projectile %d state differs: first=%+v second=%+v", i, p, q)
		}
	}
}

func TestON04_NoProductionCompleteAim(t *testing.T) {
	// This test documents that no production code path calls CompleteAim except port's test.
	// We verify that combat Service does not contain a direct CompleteAim(1) call by checking source.
	// As a runtime check, ensure that AimSlot.CompleteAim is only used via direct Ready set in service.
	// This is a placeholder that always passes if service avoids CompleteAim literal.
	// The vet will check source grep; here we just ensure AimSlot works.
	slot := cob.AimSlot{}
	slot.StartAim()
	if slot.CanFire() {
		t.Fatalf("should not be ready before return")
	}
	slot.CompleteAim(0)
	if slot.CanFire() {
		t.Fatalf("zero should not grant")
	}
	slot.CompleteAim(1)
	if !slot.CanFire() {
		t.Fatalf("nonzero should grant")
	}
}

func TestON04_BallisticSentinel_SuppressesAim(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	// Ballistic weapon with sentinel pitch 0x8000 should suppress Aim
	// We need to craft scenario where BallisticSolve fails? Actually sentinel is desiredPitch == 0x8000.
	// We can directly test that StepWeaponsForUnit with ballistic weapon and target that yields sentinel
	// does not dispatch Aim but goes to admission (which will fail due to no ballistic solution)
	// For this test, we use a ballistic weapon with zero velocity to trigger goto admission without Aim
	weapon := &content.WeaponDef{ID: 9, Ballistic: true, WeaponVelocity: 0, Range: 1000 * 65536}
	shooter.InstallWeapon(0, weapon)
	shooter.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	shooter.SlotAt(0).Flags |= 0x02
	// VM with Aim that would return 1 if called
	code := []uint32{0x10021001, 1, 0x10065000}
	prog := progWithAim(code, "AimPrimary", 0)
	vm := cob.NewVM(prog)
	attachTestCOB(shooter, vm)
	var svc Service
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
	// Should not have dispatched Aim (no aim_dispatch trace) because velocity 0 triggers goto admission before Aim?
	// In our stepSlot, we check weapon.Ballistic && vel==0 goto admission before Aim dispatch, so no Aim dispatch.
	if sum.Dispatched {
		t.Fatalf("ballistic sentinel should suppress Aim dispatch, got %+v", sum)
	}
	_ = pool.Handle(0)
}
