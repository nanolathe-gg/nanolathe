//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The long-range artillery and the missile silos AUTHOR standing fire order 0,
// so a Hold Fire that closed ordered launches left them unable to fire at all.
// Nanolathe Modern policy: docs/DESIGN_WEAPONS_PROJECTILES.md §2.6.1 — Hold
// Fire suppresses what the unit would do on its own, never what it is told to
// do. This runs the whole route: human command, order handler, slot, launch.
func TestModernHoldFireObeysExplicitAttackOrders(t *testing.T) {
	f := loadRetailFixture(t)
	for _, tc := range []struct {
		shooter string
		ground  bool
	}{{"ARMBRTHA", false}, {"ARMSILO", true}} {
		t.Run(tc.shooter, func(t *testing.T) {
			s := f.session(t)
			s.SetGameplay(gameplay.Modern)
			stepRetail(s, 2)
			shooter := placeCompleteRetailUnit(t, s, tc.shooter, 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
			enemy := placeCompleteRetailUnit(t, s, "CORSILO", 1, numeric.FixedFromInt(3000), numeric.FixedFromInt(3000))
			if shooter.Flags>>units.StandingFireShift&units.StandingFieldMask != 0 {
				t.Fatalf("%s no longer authors Hold Fire; the case proves nothing", tc.shooter)
			}
			slot := shooter.SlotAt(0)
			if slot.Weapon.Stockpile {
				slot.Ammo = 1
			}
			launches := func(ticks int) bool {
				for i := 0; i < ticks; i++ {
					p := &s.Econ.Players[0]
					p.Capacity[economy.Energy], p.Capacity[economy.Metal] = 1e9, 1e9
					p.Stock[economy.Energy], p.Stock[economy.Metal] = 1e6, 1e6
					s.Step(s.Clock.ScaledAnchor + 1)
					for j := 0; j < s.Combat.Count(); j++ {
						if s.Combat.Records[j].Shooter == shooter.Handle {
							return true
						}
					}
				}
				return false
			}
			if launches(30) {
				t.Fatal("held shooter fired with no order")
			}
			order := HumanOrderCommand{Handles: []pool.Handle{shooter.Handle}, Code: int(input.LatchAttack), Target: enemy.Handle}
			if tc.ground {
				order = HumanOrderCommand{Handles: order.Handles, Code: order.Code, Position: orders.ResolvePos{X: enemy.X, Y: enemy.Y, Z: enemy.Z}}
			}
			if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: order}); err != nil {
				t.Fatal(err)
			}
			if !launches(450) {
				t.Fatalf("%s on authored Hold Fire never obeyed its attack order: slot %+v", tc.shooter, *slot)
			}
		})
	}
}

// The other half of the policy: a unit firing on its own stops when it is told
// to hold fire, from its next weapon visit, and does not re-engage. The missile
// tower idles on the stationary guard, whose phase 1 takes over the target the
// tower acquired — so the engagement being stopped is one an order holds
// [04 R-ORD-01 §3], which the launch gate alone would let fire on. The Strict
// 3.1 side of the stance write is locked where it is decided, in
// internal/orders.
func TestModernHoldFireStopsAutomaticFire(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	s.SetGameplay(gameplay.Modern)
	stepRetail(s, 2)
	shooter := placeCompleteRetailUnit(t, s, "ARMRL", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
	placeCompleteRetailUnit(t, s, "CORSILO", 1, numeric.FixedFromInt(600), numeric.FixedFromInt(800))
	if shooter.Flags>>units.StandingFireShift&units.StandingFieldMask != 2 {
		t.Fatal("ARMRL no longer authors Fire at Will; the case proves nothing")
	}
	lastLaunch := func(ticks int, stopAtFirst bool) (last uint32, any bool) {
		for i := 0; i < ticks; i++ {
			s.Econ.Players[0].Stock[economy.Energy] = 10000
			s.Step(s.Clock.ScaledAnchor + 1)
			for j := 0; j < s.Combat.Count(); j++ {
				if r := &s.Combat.Records[j]; r.Shooter == shooter.Handle && (!any || r.CreationTick > last) {
					last, any = r.CreationTick, true
				}
			}
			if any && stopAtFirst {
				return
			}
		}
		return
	}
	if _, fired := lastLaunch(600, true); !fired {
		t.Fatal("fire-at-will tower never engaged on its own; the case proves nothing")
	}
	if q := orders.QueueForUnit(shooter); q == nil || q.Head() == nil || orders.DescriptorFor(q.Head().ID).Name != "Guard_NoMove" || shooter.SlotAt(0).IsAutonomous() {
		t.Fatal("the tower's engagement is not held by its stationary guard; the case proves nothing")
	}
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive && u.Owner == 0 {
			u.Flags &^= 0x10
		}
	}
	shooter.Flags |= 0x10 // the selection bit the stance broadcast walks [04 R-STANCE-01 §5]
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanStance, Stance: HumanStanceCommand{Fire: true, Value: 0}}); err != nil {
		t.Fatal(err)
	}
	lastLaunch(2, false) // the command is applied and the standing record runs
	if shooter.Flags>>units.StandingFireShift&units.StandingFieldMask != 0 {
		t.Fatal("Hold Fire command did not reach the unit")
	}
	held := s.Clock.GlobalTick
	if last, _ := lastLaunch(400, false); last > held {
		t.Fatalf("held tower launched at tick %d, after Hold Fire landed by tick %d", last, held)
	}
}
