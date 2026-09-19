package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A fresh failed solve preserves the stored angles and receiver and continues
// through the ordinary reload/admission gates. Only an attempted shot's failed
// admission raises could-not-fire here [06 §3.3][06 R-WPN-05 §6].
func TestFreshTurretAimFailurePreservesStateAndVisitsFireGates(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			for _, tc := range []struct {
				name        string
				reload      int32
				water       bool
				ready       bool
				wantFailure bool
			}{
				{name: "reloading", reload: 2, ready: true},
				{name: "reload reaches admission", reload: 1, ready: true, wantFailure: true},
				{name: "unready still visits admission", wantFailure: true},
				{name: "water admission passes without a fresh request", water: true, ready: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w, terrain, shooter, target := newTestWorldAndUnits(t)
					attachTestCOB(shooter, cob.NewVM(progWithAim([]uint32{0x10021001, 1, 0x10065000}, "AimPrimary", 0)))
					weapon := weaponTurret(905)
					weapon.Range, weapon.WeaponVelocity = 1000, int32(numeric.FixedFromInt(1))
					weapon.Ballistic, weapon.LineOfSight, weapon.WaterWeapon = true, false, tc.water
					weapon.EnergyPerShot, weapon.MetalPerShot, weapon.Accuracy = 7, 11, 100
					terrain.Gravity = numeric.FixedFromInt(1)
					shooter.InstallWeapon(0, weapon)
					slot := shooter.SlotAt(0)
					slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
					slot.Reload, slot.Ammo = tc.reload, 3
					slot.Aim.Ready = tc.ready
					slot.DesiredYaw, slot.DesiredPitch = 123, 456
					beforeAim, beforeFlags, beforeTarget := slot.Aim, slot.Flags, slot.Target
					const priorEvent = uint32(0x10)
					shooter.Pending = priorEvent
					if _, _, ok := turretAimGeometry(shooter, weapon, 0, shooter.ScriptState.Binding.Callbacks, Vec3{X: target.X, Y: target.Y, Z: target.Z}, terrain); ok {
						t.Fatal("fixture must have no ballistic solution")
					}
					econ := &economy.Service{}
					econ.Players[shooter.Owner].Stock[economy.Energy] = 100
					econ.Players[shooter.Owner].Stock[economy.Metal] = 100
					beforePlayer := econ.Players[shooter.Owner]
					random := rng.NewSimulation(77)
					beforeRandom := random
					svc := Service{Rules: rulesForModern(mode == gameplay.Modern)}
					sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, econ, nil, &random, nil)
					if sum.Dispatched || sum.Fired != 0 || svc.Count() != 0 || slot.DesiredYaw != 123 || slot.DesiredPitch != 456 || slot.Aim != beforeAim || slot.Flags != beforeFlags {
						t.Errorf("fresh failed solve changed stored state: summary=%+v angles=(%d,%d) aim=%+v flags=%#x", sum, slot.DesiredYaw, slot.DesiredPitch, slot.Aim, slot.Flags)
					}
					wantEvent := priorEvent
					if tc.wantFailure {
						wantEvent |= units.PendingCouldNotFire
					}
					if shooter.Pending != wantEvent {
						t.Errorf("events=%#x, want %#x after ordinary fire gates", shooter.Pending, wantEvent)
					}
					wantReload := tc.reload
					if wantReload != 0 {
						wantReload--
					}
					if slot.Reload != wantReload || slot.Ammo != 3 || slot.Target != beforeTarget || econ.Players[shooter.Owner] != beforePlayer || random != beforeRandom {
						t.Fatal("failed solve changed target, ammunition, resources or RNG beyond the normal reload decrement")
					}
				})
			}
		})
	}
}

// The reload store precedes the ordinary fired event. A malformed zero maximum
// health faults after creation, before either the event or debit [06 §4.2].
func TestOrdinaryShotReloadFaultPrecedesFiredEvent(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	weapon := weaponNonTurret(906)
	weapon.Range, weapon.WeaponVelocity, weapon.ReloadTime = 1000, 65536, 30
	weapon.EnergyPerShot, weapon.MetalPerShot = 7, 11
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	shooter.MaxHealth = 0
	const priorEvent = uint32(0x10)
	shooter.Pending = priorEvent
	econ := &economy.Service{}
	econ.Players[shooter.Owner].Stock[economy.Energy] = 100
	econ.Players[shooter.Owner].Stock[economy.Metal] = 100
	beforePlayer := econ.Players[shooter.Owner]
	var svc Service
	defer func() {
		if recover() == nil {
			t.Fatal("expected the established zero maximum health reload fault")
		}
		if svc.Count() != 1 {
			t.Fatal("reload fault must follow projectile creation")
		}
		if shooter.Pending != priorEvent || slot.Reload != 0 || econ.Players[shooter.Owner] != beforePlayer {
			t.Fatalf("reload fault already published or debited: events=%#x reload=%d stock=%v", shooter.Pending, slot.Reload, econ.Players[shooter.Owner].Stock)
		}
	}()
	svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, econ, nil, nil, nil)
}
