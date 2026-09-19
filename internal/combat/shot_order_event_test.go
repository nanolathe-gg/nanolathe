package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Shot completion is an order event, selected by commandfire and emitted only
// after a successful executor, including stockpile launches. A refused launch
// must not satisfy an attack order's completion gate [06 §4.2][06 R-WPN-05 §6].
func TestSuccessfulShotPublishesOrderEvent(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			for _, tc := range []struct {
				name        string
				commandFire bool
				stockpile   bool
				fullPool    bool
				reload      int32
				wantFired   int
				wantEvent   uint32
			}{
				{name: "ordinary shot", wantFired: 1, wantEvent: 0x400},
				{name: "command shot", commandFire: true, wantFired: 1, wantEvent: 0x800},
				{name: "ordinary pool full", fullPool: true},
				{name: "command pool full", commandFire: true, fullPool: true},
				{name: "reload pending", reload: 2},
				{name: "stockpile", stockpile: true, wantFired: 1, wantEvent: 0x400},
				{name: "command stockpile", stockpile: true, commandFire: true, wantFired: 1, wantEvent: 0x800},
				{name: "stockpile pool full", stockpile: true, fullPool: true},
				{name: "command stockpile pool full", stockpile: true, commandFire: true, fullPool: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w, terrain, shooter, target := newTestWorldAndUnits(t)
					attachTestCOB(shooter, cob.NewVM(progWithAim([]uint32{0x10021001, 100000, 0x10013000, 0x10065000}, "FirePrimary", 0)))
					weapon := weaponNonTurret(902)
					weapon.Range, weapon.WeaponVelocity, weapon.ReloadTime = 1000, 65536, 30
					weapon.CommandFire, weapon.Stockpile = tc.commandFire, tc.stockpile
					weapon.EnergyPerShot, weapon.MetalPerShot = 7, 11
					shooter.InstallWeapon(0, weapon)
					slot := shooter.SlotAt(0)
					slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
					slot.Reload, slot.Ammo = tc.reload, 1
					const priorEvent = uint32(0x10)
					shooter.Pending = priorEvent
					fireQueued := false
					shooter.ScriptState.Binding.Callbacks.SetLifecycleSink(func(event cob.LifecycleEvent) {
						if event.Name == "FirePrimary" && event.Phase == "start" {
							fireQueued = true
							if shooter.Pending != priorEvent {
								t.Fatal("shot event was published before the executor queued FirePrimary")
							}
						}
					})
					catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"event": weapon}}
					catalog.RebuildWeaponIndex()
					svc := Service{Rules: rulesForModern(mode == gameplay.Modern)}
					econ := &economy.Service{}
					econ.Players[shooter.Owner].Stock[economy.Energy] = 100
					econ.Players[shooter.Owner].Stock[economy.Metal] = 100
					random := rng.NewSimulation(77)
					beforeRandom := random
					if tc.fullPool {
						for {
							if _, ok := svc.Reserve(); !ok {
								break
							}
						}
					}
					sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, econ, catalog, &random, nil)
					if sum.Fired != tc.wantFired {
						t.Fatalf("fired = %d, want %d", sum.Fired, tc.wantFired)
					}
					if fireQueued != (tc.wantFired != 0) {
						t.Fatalf("FirePrimary queued = %v, expected successful executor = %v", fireQueued, tc.wantFired != 0)
					}
					if shooter.Pending != priorEvent|tc.wantEvent {
						t.Fatalf("order events = %#x, want %#x", shooter.Pending, priorEvent|tc.wantEvent)
					}
					wantAmmo, wantReload := int32(1), tc.reload
					if wantReload != 0 {
						wantReload--
					}
					wantEnergy, wantMetal := float32(100), float32(100)
					if tc.wantFired != 0 {
						if tc.stockpile {
							wantAmmo--
						} else {
							wantReload = 30
							wantEnergy, wantMetal = 93, 89
						}
					}
					if int32(slot.Ammo) != wantAmmo || slot.Reload != wantReload || econ.Players[shooter.Owner].Stock[economy.Energy] != wantEnergy || econ.Players[shooter.Owner].Stock[economy.Metal] != wantMetal || random != beforeRandom {
						t.Fatalf("shot effects: ammo=%d reload=%d stock=%v RNG=%+v", slot.Ammo, slot.Reload, econ.Players[shooter.Owner].Stock, random)
					}
				})
			}
		})
	}
}
