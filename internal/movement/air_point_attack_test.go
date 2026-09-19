package movement

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Explicit ground/feature attacks retain their point through the flight phases
// and arm the bomber's first slot at release [04 R-ORD-02 §1][04 R-AIR-01 §8].
func TestBomberPointAttackReleasesBombs(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, feature := range []bool{false, true} {
			t.Run(fmt.Sprintf("modern=%v/feature=%v", modern, feature), func(t *testing.T) {
				sys, w, u := wideAirFixture(t)
				sys.BindAirOrderLegs()
				u.Def.CanAttack = true
				u.Def.Weapon1Def = &content.WeaponDef{ID: 1, Dropped: true, Range: 1000, ReloadTime: 5, EnergyPerShot: 5, MetalPerShot: 7}
				u.InstallWeapon(0, u.Def.Weapon1Def)
				u.Flags |= units.ArmedStatus | 2<<units.StandingFireShift
				q := orders.QueueForUnit(u)
				if modern {
					q.Binding().Rules = &orders.ModernRules{}
				}
				q.Binding().World = &orders.WorldQueryAdapter{SeaLevel: func() uint8 { return 0 }}
				q.Binding().Weapons = &orders.WeaponAdapter{InhibitSlot: combat.InhibitWeaponSlot, ReleaseSlot: combat.ReleaseWeaponSlot, FirePoint: combat.FireWeaponPoint, StopFiring: combat.StopWeaponFiring}
				pos := orders.ResolvePos{X: numeric.FixedFromInt(480), Z: numeric.FixedFromInt(256), HasFeature: feature}
				id := orders.Resolve(3, u, nil, &pos)
				if id != orders.Lookup("AirStrike") {
					t.Fatal("point attack did not resolve to bomber order")
				}
				q.Push(id, orders.NewNodeForOrder(id, 0, pos.X, pos.Y, pos.Z, 0, u.Handle, false))
				n := q.Head()
				ledger := &economy.Service{}
				ledger.Players[0].Stock = [2]float32{10000, 10000}
				catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"bomb": u.Def.Weapon1Def}}
				catalog.RebuildWeaponIndex()
				var svc combat.Service
				for tick := uint32(1); tick <= 3000; tick++ {
					fired := svc.StepWeaponsForUnit(u, tick, w, nil, sys.Terrain, ledger, catalog, q.Binding().SimRNG, nil).Fired
					if fired != 0 {
						target := u.SlotAt(0).Target
						if target.Kind != units.TargetGround || target.X != pos.X || target.Z != pos.Z {
							t.Fatalf("bomb released at wrong goal: %+v", target)
						}
						want := [2]float32{10000 - float32(fired)*7, 10000 - float32(fired)*5}
						if ledger.Players[0].Stock != want {
							t.Fatalf("bomb costs=%v, want %v", ledger.Players[0].Stock, want)
						}
						t.Logf("released at tick %d, phase %d", tick, n.Phase)
						return
					}
					q.Pump(u, tick)
					runMovementTick(sys, tick, w)
					if q.Head() != n {
						t.Fatalf("point attack cancelled before release at tick %d", tick)
					}
					if n.GoalX != pos.X || n.GoalY != pos.Y || n.GoalZ != pos.Z {
						t.Fatal("flight changed clicked goal")
					}
				}
				t.Fatalf("bomber never released, phase=%d", n.Phase)
			})
		}
	}
}
