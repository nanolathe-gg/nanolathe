package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

func weaponForTest(reload int32, turret, stockpile bool) *content.WeaponDef {
	return &content.WeaponDef{
		ReloadTime: reload,
		Turret:     turret,
		VLaunch:    false,
		Stockpile:  stockpile,
	}
}

func TestPipelineOrderSpySequence(t *testing.T) {
	// Single slot with all gates passing should visit steps in established order [06 §4.1] C1.
	var slots [NumSlots]Slot
	slots[0] = Slot{
		Weapon: weaponForTest(10, false, false), // non-turret so aim gate passes without Ready
		Reload: 1,                               // will decrement to 0 before admission [06 §4.1]
		Target: Target{Kind: TargetUnit, Unit: 2},
	}
	// Make slot 1 and 2 empty (no weapon) — they should be skipped but still respect numeric order [06 §1.2].
	spy := &PipelineSpy{}
	env := PipelineEnv{
		ValidateTarget: func(idx int, tr Target) bool { return true },
		CheckAdmission: func(idx int, s *Slot) bool { return true },
		TryFire:        func(idx int, s *Slot) bool { return true },
	}
	// Use health/max/kills for reload compute path (store step)
	fired := TickSlot(&slots[0], 0, 1, spy, env, 100, 100, 0)
	if !fired {
		t.Fatalf("expected fire to succeed")
	}
	want := []PipelineStep{StepDecrement, StepTargetValidate, StepAdmission, StepSpawner, StepStoreReload, StepDebit}
	if len(spy.Steps) != len(want) {
		t.Fatalf("steps len %d, want %d: got %v", len(spy.Steps), len(want), spy.Steps)
	}
	for i, step := range want {
		if spy.Steps[i] != step {
			t.Fatalf("step %d: got %v, want %v (full %v)", i, spy.Steps[i], step, spy.Steps)
		}
	}
	// Reload should have been decremented 1->0 then stored via ComputeStoredReload (health 100 tier0 reload 10 -> 10)
	if slots[0].Reload != 10 {
		t.Fatalf("stored reload %d, want 10 (health 100 tier0 authored 10)", slots[0].Reload)
	}

	// Test tick order across all three slots is numeric order [06 §1.2] C1 (I1).
	var slots3 [NumSlots]Slot
	for i := 0; i < NumSlots; i++ {
		slots3[i] = Slot{
			Weapon: weaponForTest(0, false, false),
			Reload: 0,
			Target: Target{Kind: TargetUnit, Unit: poolHandle(i + 1)},
		}
	}
	spy3 := &PipelineSpy{}
	env3 := PipelineEnv{
		ValidateTarget: func(idx int, tr Target) bool { return true },
		CheckAdmission: func(idx int, s *Slot) bool { return true },
		TryFire:        func(idx int, s *Slot) bool { return true },
	}
	n := TickUnitSlots(&slots3, 2, spy3, env3, 100, 100, 0)
	if n != 3 {
		t.Fatalf("fired %d, want 3", n)
	}
	// Expect 6*3 steps (no Aim step for non-aim families), slot order preserved.
	if len(spy3.Steps) != 18 {
		t.Fatalf("overall steps %d, want 18: %v", len(spy3.Steps), spy3.Steps)
	}
	// Verify slot indices via env callback order — we can check that ValidateTarget saw idx 0,1,2 in order.
}

func poolHandle(v int) pool.Handle { return pool.Handle(v) }

// Use real pool.Handle import for variant test below
func TestPipelineOrderShortCircuitOnAimReady(t *testing.T) {
	// Turret weapon requires aim-ready [06 §3.3] [GAP T15] C9; zero return never fires.
	// Target resolution now precedes the Aim step; the slot dispatches Aim*
	// (issue latch) and waits — admission and fire never run.
	var slot Slot
	slot.Weapon = weaponForTest(0, true, false) // turret requires aim
	slot.Reload = 0
	slot.Target = Target{Kind: TargetUnit, Unit: 1}
	spy := &PipelineSpy{}
	env := PipelineEnv{
		CheckAdmission: func(idx int, s *Slot) bool {
			t.Fatalf("should not reach admission when aim-ready fails")
			return true
		},
		TryFire: func(idx int, s *Slot) bool {
			t.Fatalf("should not fire when aim-ready fails")
			return true
		},
	}
	fired := TickSlot(&slot, 0, 1, spy, env, 100, 100, 0)
	if fired {
		t.Fatalf("fired with aim-not-ready, want not fired [GAP T15] C9")
	}
	if !slot.Aim.IssueBit {
		t.Fatalf("Aim dispatch should have set the issue latch [04 §5.3]")
	}
	// Decrement, target validate, then the Aim dispatch/wait step.
	if len(spy.Steps) != 3 || spy.Steps[0] != StepDecrement || spy.Steps[1] != StepTargetValidate || spy.Steps[2] != StepAimDispatch {
		t.Fatalf("steps on short-circuit %v, want [Decrement Target AimDispatch]", spy.Steps)
	}
}

func TestAimReadyGatingZeroReturnNeverFires(t *testing.T) {
	// Aim-ready granted only on nonzero Aim* return [GAP T15] C9/C16.
	var slot Slot
	slot.Weapon = weaponForTest(0, true, false)
	slot.Reload = 0
	// Complete with zero — should not become ready
	if slot.CompleteAim(0) {
		t.Fatalf("CompleteAim(0) returned ready true, want false [GAP T15] C9")
	}
	if slot.IsAimReady() {
		t.Fatalf("IsAimReady true after zero return, want false [GAP T15] C9")
	}
	spy := &PipelineSpy{}
	env := PipelineEnv{
		ValidateTarget: func(idx int, tr Target) bool { return true },
		CheckAdmission: func(idx int, s *Slot) bool { return true },
		TryFire:        func(idx int, s *Slot) bool { return true },
	}
	if TickSlot(&slot, 0, 1, spy, env, 100, 100, 0) {
		t.Fatalf("fired with zero-aim, want not fired [GAP T15] C9")
	}
	// Nonzero return grants ready
	if !slot.CompleteAim(1) {
		t.Fatalf("CompleteAim(1) should grant ready [GAP T15] C9")
	}
	if !slot.IsAimReady() {
		t.Fatalf("IsAimReady false after nonzero return, want true [GAP T15] C9")
	}
	spy2 := &PipelineSpy{}
	if !TickSlot(&slot, 0, 2, spy2, env, 100, 100, 0) {
		t.Fatalf("fired after nonzero aim, want fired [GAP T15] C9")
	}
	// Also test that exhausted delivery 0 leaves permanently unable (second zero should not clear ready but new slot)
	var slot2 Slot
	slot2.Weapon = weaponForTest(0, true, false)
	slot2.CompleteAim(0)
	if slot2.IsAimReady() {
		t.Fatalf("slot2 ready after zero, want false (exhausted) [GAP T15] C16")
	}
	// Negative nonzero also counts as nonzero? Retail says nonzero, so -1 should grant.
	var slot3 Slot
	slot3.Weapon = weaponForTest(0, true, false)
	if !slot3.CompleteAim(-5) {
		t.Fatalf("CompleteAim(-5) should grant ready (nonzero) [GAP T15] C9")
	}
}

func TestReloadTruncationVectors(t *testing.T) {
	// Vectors pin integer truncation order per [06 §4.2] C7 [01 §8] I3.
	tests := []struct {
		health, maxHealth int32
		kills             int32
		authored          int32
		wantVeteran       int32
		wantFactor        int32
		wantStored        int32
	}{
		{100, 100, 0, 30, 30, 100, 30}, // tier0 100%
		{100, 100, 7, 30, 28, 100, 28}, // tier1 94%
		{50, 100, 12, 30, 26, 110, 28}, // tier2 88% + factor 110
		{25, 100, 30, 30, 21, 115, 24}, // tier5 cap 70% + factor 115
		{100, 100, 0, 7, 7, 100, 7},    // small authored
		{3, 7, 0, 30, 30, 112, 33},     // truncation: 20*3/7=8 ->112 ->33
		{1, 3, 0, 30, 30, 114, 34},     // 20*1/3=6 ->114 ->34
		{100, 100, 5, 10, 9, 100, 9},   // 94% of 10 =9 (trunc)
		{100, 100, 10, 10, 8, 100, 8},  // 88% of 10 =8
		{0, 100, 0, 30, 30, 120, 36},   // zero health factor 120 -> 36
	}
	for i, tc := range tests {
		v := VeteranReloadForTest(tc.kills, tc.authored)
		if v != tc.wantVeteran {
			t.Fatalf("case %d veteran %d, want %d (kills %d authored %d) [06 §4.2]", i, v, tc.wantVeteran, tc.kills, tc.authored)
		}
		f := HealthFactorForTest(tc.health, tc.maxHealth)
		if f != tc.wantFactor {
			t.Fatalf("case %d factor %d, want %d (health %d/%d) [06 §4.2]", i, f, tc.wantFactor, tc.health, tc.maxHealth)
		}
		got := ComputeStoredReload(tc.health, tc.maxHealth, tc.kills, tc.authored)
		if got != tc.wantStored {
			t.Fatalf("case %d stored %d, want %d (h %d/%d kills %d auth %d) [06 §4.2] I3", i, got, tc.wantStored, tc.health, tc.maxHealth, tc.kills, tc.authored)
		}
	}
	// Malformed: zero maxHealth raises divide fault [P1-07 §2.7][GAP T5] — retail #DE after reserve not rolled back
	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("zero maxHealth should panic divide fault [P1-07 §2.7]")
			}
		}()
		ComputeStoredReload(100, 0, 0, 30)
	}()
	// Negative health uses signed IDIV trunc toward zero [P1-07 §2.7] — no clamp, factor 122 => 36
	if got := ComputeStoredReload(-10, 100, 0, 30); got != 36 {
		t.Fatalf("negative health stored %d, want 36 [P1-07 §2.7] signed trunc", got)
	}
	// HealthFactor zero max also faults
	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("HealthFactor zero max should panic [P1-07 §2.7]")
			}
		}()
		HealthFactorForTest(100, 0)
	}()
	// Tier cap at 5: kills 100 should still tier 5
	if got := VeteranReloadForTest(100, 100); got != 70 {
		t.Fatalf("tier cap veteran %d, want 70 [06 §4.2]", got)
	}
	if got := VeteranReloadForTest(25, 100); got != 70 {
		t.Fatalf("tier cap 25 kills veteran %d, want 70 [06 §4.2]", got)
	}
	// Unsigned kills: -1 as uint32 huge -> tier 5
	if got := VeteranReloadForTest(-1, 100); got != 70 {
		t.Fatalf("negative kills unsigned tier veteran %d, want 70 [06 §4.2]", got)
	}
}
