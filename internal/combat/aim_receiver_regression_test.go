package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestAimReceiverReplacesReadinessAfterDriftRetry follows one real COB Aim
// receiver through success, a turret drift refusal, and its replacement
// request. A delivered zero must replace the earlier permission; it cannot
// inherit it [06 §3.3][06 R-WPN-03 §2].
func TestAimReceiverReplacesReadinessAfterDriftRetry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		zeroResult bool
		exhaust    bool
		wantFire   bool
	}{
		{name: "explicit-zero", zeroResult: true},
		{name: "failed-start-zero", exhaust: true},
		{name: "explicit-nonzero", wantFire: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			prog := &cob.Program{
				Code: []uint32{
					0x10021004, 0, 0x10065000, // AimPrimary: return static 0.
					0x10021001, 1, 0x10023004, 0, 0x10065000, // SetOne.
					0x10021001, 0, 0x10023004, 0, 0x10065000, // SetZero.
				},
				Scripts:     map[string]int{"AimPrimary": 0, "SetOne": 3, "SetZero": 8},
				ScriptsByID: []int{0, 3, 8},
				Pieces:      []string{"base"},
				Statics:     1,
			}
			vm := cob.NewVM(prog)
			attachTestCOB(shooter, vm)
			if !vm.StartByName("SetOne", nil) {
				t.Fatal("could not initialize Aim return")
			}
			vm.Drain(1)

			weapon := weaponTurret(901)
			weapon.Tolerance = 1
			weapon.EnergyPerShot = 7
			weapon.MetalPerShot = 11
			shooter.InstallWeapon(0, weapon)
			slot := shooter.SlotAt(0)
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
			slot.Reload = 2
			catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"aim-receiver": weapon}}
			catalog.RebuildWeaponIndex()
			econ := &economy.Service{}
			econ.Players[shooter.Owner].Stock[economy.Energy] = 100
			econ.Players[shooter.Owner].Stock[economy.Metal] = 100
			var svc Service

			// The first real Aim returns nonzero, but reload holds fire.
			if sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, econ, catalog, nil, nil); !sum.ReturnSeen || !slot.Aim.Ready || sum.Fired != 0 {
				t.Fatalf("first Aim did not grant held readiness: %+v aim=%+v", sum, slot.Aim)
			}
			// Turn the hull past tolerance. The second visit consumes the last
			// reload tick, then rejects at drift and clears only IssueBit.
			shooter.Move.Heading += 100
			if sum := svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, econ, catalog, nil, nil); sum.Fired != 0 || slot.Aim.IssueBit || !slot.Aim.Ready {
				t.Fatalf("drift did not retain readiness while clearing the request latch: %+v aim=%+v", sum, slot.Aim)
			}
			if tc.zeroResult {
				if !vm.StartByName("SetZero", nil) {
					t.Fatal("could not select zero Aim return")
				}
				vm.Drain(1)
			}
			if tc.exhaust {
				for i := range vm.Threads {
					vm.Threads[i].Status = cob.ThreadSleeping
					vm.Threads[i].Sleep = 100
				}
			}
			energyBefore := econ.Players[shooter.Owner].Stock[economy.Energy]
			metalBefore := econ.Players[shooter.Owner].Stock[economy.Metal]
			sum := svc.StepWeaponsForUnit(shooter, 3, w, nil, terrain, econ, catalog, nil, nil)
			if tc.wantFire {
				if sum.Fired != 1 || svc.Count() != 1 {
					t.Fatalf("nonzero replacement Aim did not fire: %+v count=%d", sum, svc.Count())
				}
				return
			}
			if !sum.ReturnSeen || sum.ReturnValue != 0 || sum.Fired != 0 || svc.Count() != 0 || slot.Aim.Ready {
				t.Fatalf("zero replacement Aim retained permission: %+v count=%d aim=%+v", sum, svc.Count(), slot.Aim)
			}
			if slot.Reload != 0 || slot.Ammo != 0 || econ.Players[shooter.Owner].Stock[economy.Energy] != energyBefore || econ.Players[shooter.Owner].Stock[economy.Metal] != metalBefore {
				t.Fatalf("refused Aim mutated firing state: reload=%d ammo=%d energy=%v metal=%v", slot.Reload, slot.Ammo, econ.Players[shooter.Owner].Stock[economy.Energy], econ.Players[shooter.Owner].Stock[economy.Metal])
			}
		})
	}
}
