package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Shot completion is an order event, selected by commandfire and emitted only
// after a successful non-stockpile executor. A refused or stockpile launch
// must not satisfy an attack order's completion gate [06 §4.2][06 R-WPN-05 §6].
func TestSuccessfulShotPublishesOrderEvent(t *testing.T) {
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
		{name: "stockpile", stockpile: true, wantFired: 1},
		{name: "command stockpile", stockpile: true, commandFire: true, wantFired: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			attachTestCOB(shooter, cob.NewVM(progWithAim([]uint32{0x10021001, 100000, 0x10013000, 0x10065000}, "FirePrimary", 0)))
			weapon := weaponNonTurret(902)
			weapon.Range, weapon.WeaponVelocity, weapon.ReloadTime = 1000, 65536, 30
			weapon.CommandFire, weapon.Stockpile = tc.commandFire, tc.stockpile
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
			var svc Service
			if tc.fullPool {
				for {
					if _, ok := svc.Reserve(); !ok {
						break
					}
				}
			}
			sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, catalog, nil, nil)
			if sum.Fired != tc.wantFired {
				t.Fatalf("fired = %d, want %d", sum.Fired, tc.wantFired)
			}
			if fireQueued != (tc.wantFired != 0) {
				t.Fatalf("FirePrimary queued = %v, expected successful executor = %v", fireQueued, tc.wantFired != 0)
			}
			if shooter.Pending != priorEvent|tc.wantEvent {
				t.Fatalf("order events = %#x, want %#x", shooter.Pending, priorEvent|tc.wantEvent)
			}
		})
	}
}
