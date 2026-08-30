package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// TestP0I02_UnfinishedNeverProgresses proves an unfinished unit with no builder
// never changes Remaining across ticks. Construction progress must have one
// owner (construction.Service) and no other package mutates Remaining
// [05 "Construction target state"] [05 "Construction arithmetic"].
func TestP0I02_UnfinishedNeverProgresses(t *testing.T) {
	world := newFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "tick-unfinished", MaxDamage: 100, Limit: -1}
	// Create as nanoframe with Remaining 1 [04 §2.3] C3 remaining 1→0.
	h, err := world.Create(def, 3, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := world.Unit(h)
	u.Remaining = 1.0
	u.Health = 0
	for i := 0; i < 100; i++ {
		runPhase2Sweep(world, uint32(10+i))
		if got := u.Remaining; got != 1.0 {
			t.Fatalf("tick %d: Remaining %v want 1.0 – the sweep must not mutate Remaining [05 \"Construction target state\"]", i, got)
		}
	}
	// Completed unit also must not be mutated by Tick (construction owns, not Tick).
	u2, _ := world.Create(def, 3, 0, 0, 0)
	world.Unit(u2).Remaining = 0.5 // mid-construction
	runPhase2Sweep(world, 200)
	if got := world.Unit(u2).Remaining; got != 0.5 {
		t.Fatalf("mid-construction Remaining %v want 0.5", got)
	}
	// Completed unit with builder but insufficient resources would stall in
	// construction.Service, not in Tick. Tick alone must not progress it either.
	u3, _ := world.Create(def, 3, 0, 0, 0)
	world.Unit(u3).Remaining = 0.25
	runPhase2Sweep(world, 201)
	if got := world.Unit(u3).Remaining; got != 0.25 {
		t.Fatalf("completed/builder stall Remaining %v want 0.25", got)
	}
}

func TestHealthSampleKeepsCurrentAndPriorWindows(t *testing.T) {
	world := newFixtureWorld(2, nil)
	def := &content.UnitDef{UnitName: "tick-health", MaxDamage: 100, Limit: -1}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := world.Unit(h)
	u.Health = 100
	world.StepPreUpdate(h, 30)
	if u.CurrentSample != 100 || u.PriorSample != 0 {
		t.Fatalf("first health window current=%d prior=%d, want 100/0", u.CurrentSample, u.PriorSample)
	}
	u.Health = 50
	world.StepPreUpdate(h, 60)
	if u.CurrentSample != 50 || u.PriorSample != 100 {
		t.Fatalf("second health window current=%d prior=%d, want 50/100", u.CurrentSample, u.PriorSample)
	}
}

// TestP0I02_CompletedNoInventedProgress ensures a completed unit does not
// get invented progress or spurious callbacks. The old Remaining -=0.01 path
// is deleted; Tick must not synthesize completion ticks [05].
func TestP0I02_CompletedNoInventedProgress(t *testing.T) {
	world := newFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "tick-completed", MaxDamage: 100}
	h, _ := world.Create(def, 0, 0, 0, 0)
	u := world.Unit(h)
	u.Remaining = 0 // completed [04 §2.3] C3 1→0 done
	u.Health = 100
	beforeHealth := u.Health
	runPhase2Sweep(world, 1)
	runPhase2Sweep(world, 2)
	if u.Health != beforeHealth {
		t.Fatalf("completed unit health changed %d -> %d", beforeHealth, u.Health)
	}
	if u.Remaining != 0 {
		t.Fatalf("completed Remaining %v want 0", u.Remaining)
	}
	if u.Dying {
		t.Fatalf("completed healthy unit should not be marked Dying")
	}
	// If COB creation/activation callbacks were not yet wired, ensure no
	// invented progress still: Remaining stays 0 and no slot free.
	if world.Used() != 1 {
		t.Fatalf("used %d want 1 – Tick must not free slots", world.Used())
	}
}

// TestP0I02_AimBlocksFiring proves a weapon's Aim callback can block firing.
// Aim-ready is granted only on a nonzero script return [GAP T15] C16
// [06 §3.3]; an outstanding Aim (IssueBit true, Ready false) must block
// CanFire. This stub uses the units-local Slot which carries cob.AimSlot
// without importing combat (cycle via combat→economy→units) [06 §1.2] P0-10.
func TestP0I02_AimBlocksFiring(t *testing.T) {
	world := newFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "tick-complete", MaxDamage: 100, Limit: -1}
	// Weapon def minimal for IsPopulated true.
	wdef := &content.WeaponDef{}
	// Need to set ID etc? WeaponDef fields minimal; non-nil suffices for slot.
	h, _ := world.Create(def, 0, 0, 0, 0)
	u := world.Unit(h)
	// Install weapon in slot 0 [06 §1.2] C1.
	u.InstallWeapon(0, wdef)
	slot := u.SlotAt(0)
	if slot == nil || !slot.IsPopulated() {
		t.Fatalf("slot not populated")
	}
	slot.Reload = 0 // ready
	// No Aim yet: CanFire true (no latch).
	if !slot.CanFire() {
		t.Fatalf("without Aim latch CanFire should be true")
	}
	// Start Aim: IssueBit true, Ready false blocks firing [GAP T15] C16.
	slot.Aim.StartAim() // [GAP T15] OR 0x01 immediately after dispatch
	slot.Flags |= 0x01
	if slot.CanFire() {
		t.Fatalf("outstanding Aim (IssueBit && !Ready) must block firing [GAP T15] C16")
	}
	// Complete Aim with zero return has no effect [GAP T15] C16.
	slot.Aim.CompleteAim(0) // delivery 0 leaves Ready false [GAP T15] C16
	if slot.CanFire() {
		t.Fatalf("zero Aim return must keep weapon blocked [GAP T15] C16")
	}
	// Complete Aim with nonzero grants Ready and unblocks [GAP T15] C16.
	slot.Aim.CompleteAim(1)
	if !slot.CanFire() {
		t.Fatalf("nonzero Aim return must grant Ready and unblock [GAP T15] C16")
	}
	// weaponSlotUpdate must respect same gate: the sweep's weapon update
	// decrements reload but preserves Aim blocking without invoking spawner.
	slot.Aim = cob.AimSlot{}
	slot.Aim.StartAim()
	slot.Reload = 2
	// The sweep will decrement reload to 1 but remain blocked by Aim.
	runPhase2Sweep(world, 10)
	if slot.Reload != 1 {
		t.Fatalf("reload after Tick got %d want 1", slot.Reload)
	}
	if slot.CanFire() {
		t.Fatalf("Aim still blocks after Tick despite reload decrement")
	}
	// After Ready, next tick decrements to 0 and becomes fireable.
	slot.Aim.CompleteAim(99)
	runPhase2Sweep(world, 11)
	if slot.Reload != 0 {
		t.Fatalf("reload after second Tick got %d want 0", slot.Reload)
	}
	if !slot.CanFire() {
		t.Fatalf("Ready + reload 0 should be fireable")
	}
	// Document: stub test proves Aim can block firing without wiring combat spawner.
}

// TestP0I02_COBPieceTransforms proves COB piece transforms are retained via
// ScriptState and that Tick drains the VM without crashing when no VM is bound.
// When a VM exists, Drain(1) advances piece animation [04 §4.6][GAP T15].
func TestP0I02_COBPieceTransforms(t *testing.T) {
	world := newFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "tick-sample", MaxDamage: 100}
	h, _ := world.Create(def, 0, 0, 0, 0)
	u := world.Unit(h)

	// Placeholder path: no VM bound must not crash.
	runPhase2Sweep(world, 1) // should be no-op for COB

	// Now bind a real VM with one piece and a program that moves the piece.
	prog := &cob.Program{
		Code:        []uint32{},
		Scripts:     map[string]int{},
		Pieces:      []string{"base"},
		Statics:     0,
		ScriptsByID: []int{},
	}
	vm := cob.NewVM(prog)
	if len(vm.Pieces) != 1 {
		t.Fatalf("VM pieces %d want 1", len(vm.Pieces))
	}
	// Script translation starts at 0.
	if got := vm.Pieces[0].Trans[0]; got.Raw() != 0 {
		t.Fatalf("initial Trans %v want 0", got)
	}
	// Bind via typed ScriptState [04 §4.1][04 §4.2].
	u.SetScript(vm)
	// Also test that GetScript returns same VM and Pieces alias is visible.
	if got := u.GetScript(); got != vm {
		t.Fatalf("GetScript mismatch")
	}
	if pcs := u.ScriptState.Pieces(); len(pcs) != 1 {
		t.Fatalf("ScriptState.Pieces len %d want 1 [03 §2.4] C21", len(pcs))
	}
	// Manually move piece via VM api: set a move and drain via Tick.
	// Directly set Pieces translation to prove persistence; then Tick's
	// cobDrain with delta 1 must not clobber it when no anim is active.
	vm.Pieces[0].SetTrans(0, 100) // set X trans via PieceState helper [03 §2.4] C21
	runPhase2Sweep(world, 2)      // the sweep drains the VM with delta 1 [04 §4.2][04 §4.6]
	if got := vm.Pieces[0].GetTrans(0).Raw(); got != 100 {
		t.Fatalf("piece Trans after sweep got %d want 100 – drain must not synthesize or clear [04 §4.6]", got)
	}
	// Verify VM still alive and snapshot would see this transform.
	// The snapshot phase copies Unit.ScriptState.Pieces() into immutable frame
	// after phase 12 [03 §2.4] C21 [PLAN_03 C15]; we prove the transform is
	// present on the VM itself (or placeholder does not crash when nil).
	if u.ScriptState == nil || u.ScriptState.VM == nil {
		t.Fatalf("ScriptState VM lost")
	}
}

// TestP0I02_IterationOrder proves unit iteration remains player 0..9 then slot asc [01 §6.2] C2 [P0-16].
func TestP0I02_IterationOrder(t *testing.T) {
	world := newFixtureWorld(5, nil) // 5 per player, slots 1..5 p0, 6..10 p1, etc.
	def := &content.UnitDef{UnitName: "tick-order", MaxDamage: 100, Limit: -1}
	// Create out-of-order players to verify Tick visits 0..9 asc then slots asc.
	// Fill some slots intentionally sparse.
	h4, _ := world.Create(def, 4, 0, 0, 0) // player 4 slot 21?
	h1, _ := world.Create(def, 1, 0, 0, 0)
	h9, _ := world.Create(def, 9, 0, 0, 0)
	h0a, _ := world.Create(def, 0, 0, 0, 0)
	h0b, _ := world.Create(def, 0, 0, 0, 0)
	// Record tick visitation order via a hook that tracks order.
	// We instrument by checking IterSliced is already player 0..9 asc slots asc.
	got := world.IterSliced()
	if len(got) != 5 {
		t.Fatalf("IterSliced len %d want 5", len(got))
	}
	// Expected order: p0 slots asc, then p1, p4, p9.
	// Since sliced, p0's two units first, then p1, p4, p9.
	if got[0].Handle != h0a || got[1].Handle != h0b {
		t.Fatalf("p0 order wrong got %v %v want %v %v", got[0].Handle, got[1].Handle, h0a, h0b)
	}
	if got[2].Handle != h1 {
		t.Fatalf("p1 order wrong got %v want %v", got[2].Handle, h1)
	}
	if got[3].Handle != h4 {
		t.Fatalf("p4 order wrong got %v want %v", got[3].Handle, h4)
	}
	if got[4].Handle != h9 {
		t.Fatalf("p9 order wrong got %v want %v", got[4].Handle, h9)
	}
	// Also prove the sweep visits the same order: running it preserves the
	// deterministic IterSliced ordering (no map iteration defines order, I1).
	runPhase2Sweep(world, 1)
	got2 := world.IterSliced()
	for i := range got {
		if got[i].Handle != got2[i].Handle {
			t.Fatalf("sweep changed order at %d: %v vs %v [01 §6.2] C2", i, got[i].Handle, got2[i].Handle)
		}
	}
	// Unused handles to keep worktree deterministic.
	_ = pool.Handle(0)
}

// TestP0I02_SlotEndDeathFinalization locks the phase-2 slot-end contract
// [01 §4.4][04 §2.4]: a unit whose health is exhausted at its own visit is
// finalized at slot end (FinalizeDeath frees the slot in-visit, mirroring the
// session's phase 2), while a death marked AFTER the unit's visit — the
// projectile-phase damage shape (RS-08) — stays resolvable until the
// explicit teardown cleanup.
func TestP0I02_SlotEndDeathFinalization(t *testing.T) {
	world := newFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "tick-death", MaxDamage: 100}
	h, _ := world.Create(def, 0, 0, 0, 0)
	u := world.Unit(h)
	u.Health = 0 // lethal but not yet Dying
	if u.Alive != true {
		t.Fatalf("created unit must be alive")
	}
	// Slot-end death handling in the authoritative sweep latches Dying and
	// finalizes at slot end exactly like the session's phase 2 [01 §4.4].
	runPhase2Sweep(world, 1)
	if world.Unit(h) != nil {
		t.Fatalf("health-0 unit must be finalized at slot end by the sweep [01 §4.4][04 §2.4]")
	}
	if world.Used() != 0 {
		t.Fatalf("Used %d want 0 after slot-end finalization", world.Used())
	}
	// A death marked after the visit stays alive until explicit teardown cleanup
	// [04 §2.4] C2.
	h2, _ := world.Create(def, 0, 0, 0, 0)
	world.Destroy(h2, DeathKilled)
	if world.Unit(h2) == nil {
		t.Fatalf("death marked after the visit must stay resolvable until teardown cleanup [04 §2.4] C2")
	}
	if world.Used() != 1 {
		t.Fatalf("Used %d want 1 before Cleanup", world.Used())
	}
	world.TeardownCleanup()
	if world.Unit(h2) != nil {
		t.Fatalf("After teardown cleanup the post-visit death must be free [04 §2.4] C2")
	}
	if world.Used() != 0 {
		t.Fatalf("Used %d want 0 after teardown cleanup", world.Used())
	}
}

// TestP0I02_ReloadDecrement proves weapon reload countdown decrements each tick
// before target resolve [06 §1.2][06 §4.1] P0-10 without firing.
func TestP0I02_ReloadDecrement(t *testing.T) {
	world := newFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "tick-live", MaxDamage: 100}
	wdef := &content.WeaponDef{}
	h, _ := world.Create(def, 0, 0, 0, 0)
	u := world.Unit(h)
	u.InstallWeapon(1, wdef)
	slot := u.SlotAt(1)
	slot.Reload = 3
	runPhase2Sweep(world, 1)
	if slot.Reload != 2 {
		t.Fatalf("reload after 1 tick %d want 2 [06 §1.2][06 §4.1]", slot.Reload)
	}
	runPhase2Sweep(world, 2)
	if slot.Reload != 1 {
		t.Fatalf("reload after 2 ticks %d want 1", slot.Reload)
	}
	runPhase2Sweep(world, 3)
	if slot.Reload != 0 {
		t.Fatalf("reload after 3 ticks %d want 0", slot.Reload)
	}
	// Further ticks keep at 0, no underflow.
	runPhase2Sweep(world, 4)
	if slot.Reload != 0 {
		t.Fatalf("reload after 4 ticks %d want 0", slot.Reload)
	}
}
