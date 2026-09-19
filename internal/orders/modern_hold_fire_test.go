package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: docs/DESIGN_UNITS_ORDERS_COB.md "Modern Hold Fire".
func TestModernHoldFireKeepsGuardFollowing(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			modern bool
			stance uint32
			join   bool
		}{{"modern hold", true, 0, false}, {"strict hold", false, 0, true}, {"modern return", true, 1, true}} {
			t.Run(fmt.Sprintf("%s/air%v", tc.name, air), func(t *testing.T) {
				f := newGuardLegsFixture(t)
				f.guard.Def.CanFly = air
				f.guard.Flags = (f.guard.Flags &^ (units.StandingFieldMask << units.StandingFireShift)) | tc.stance<<units.StandingFireShift
				q := QueueForUnit(f.guard)
				b := q.Binding()
				b.Rules = modeRules(tc.modern)
				airInstalls := 0
				b.Movement.InstallAir = func(AirGoalRequest) bool { airInstalls++; return true }
				q.SetBinding(b)
				n := guardNode(f.guardFixture)
				n.Phase = 1
				if air {
					n.ID = Lookup("VTOL_Follow")
					n.Phase = 2
				}
				n.Param1 = 64
				q.primary = []*Node{n}
				random := *f.sim
				code := guardHandler(f.guard, n, 0x10, 100)
				if tc.join {
					if code != Code(3) || len(q.primary) != 2 || q.primary[1] != n || q.primary[0].Target != f.enemy.Handle {
						t.Fatalf("expected combat join above retained guard: code=%d queue=%v", code, f.queueNames())
					}
				} else {
					if code != Code(2) || len(q.primary) != 1 || q.primary[0] != n || n.Target != f.ward.Handle || n.Deadline != 130 {
						t.Fatalf("Hold Fire lost guard maintenance: code=%d queue=%v", code, f.queueNames())
					}
					if (!air && len(f.installs) != 1) || (air && airInstalls != 1) {
						t.Fatal("held guard did not keep moving")
					}
					if *f.sim != random {
						t.Fatal("suppressed guard join changed RNG")
					}
				}
			})
		}
	}
}

func TestModernStandingFirePreservesExplicitAttackOrder(t *testing.T) {
	for _, modern := range []bool{true, false} {
		f := newGuardLegsFixture(t)
		q := QueueForUnit(f.guard)
		b := q.Binding()
		b.Rules = modeRules(modern)
		q.SetBinding(b)
		f.guard.InstallWeapon(0, &content.WeaponDef{ID: 1, LineOfSight: true})
		slot := f.guard.SlotAt(0)
		slot.Flags &^= units.SlotFlagAutonomous
		slot.Target = units.Target{Kind: units.TargetUnit, Unit: f.enemy.Handle}
		attack := &Node{ID: Lookup("Attack_Chase"), Owner: f.guard.Handle, Target: f.enemy.Handle}
		q.primary = []*Node{attack}
		for _, stance := range []uint32{0, 1, 2} {
			if code := standingFireOrderHandler(f.guard, &Node{Param1: stance}, 0, 100); code != Code(5) {
				t.Fatal("standing command did not complete")
			}
			if len(q.primary) != 1 || q.primary[0] != attack || attack.Target != f.enemy.Handle || slot.Target.Unit != f.enemy.Handle || slot.Target.Kind != units.TargetUnit {
				t.Fatal("stance command discarded explicit attack or manual target")
			}
		}
	}
}

func TestModernManualAttackIssuedWhileHeldRemainsQueued(t *testing.T) {
	for _, order := range []string{"Attack_NoMove", "Suppress"} {
		t.Run(order, func(t *testing.T) {
			q, u := gateFixture()
			q.Binding().Rules = &ModernRules{}
			u.Alive = true
			u.Flags &^= units.StandingFieldMask << units.StandingFireShift
			for i := 0; i < units.NumSlots; i++ {
				u.SlotAt(i).Weapon.LineOfSight = true
			}
			q.Push(Lookup(order), Node{Owner: u.Handle, Target: 7, GoalX: u.X, GoalZ: u.Z, GoalSupplied: true})
			q.Pump(u, 1)
			if q.LenPrimary() != 1 {
				t.Fatal("held explicit attack was rejected")
			}
			head := q.primary[0]
			target := u.SlotAt(0).Target
			if target.Kind == units.TargetNone {
				t.Fatal("held order did not install target")
			}
			svc := &combat.Service{Rules: &combat.ModernRules{}}
			for tick := uint32(2); tick < 100; tick++ {
				sum := svc.StepWeaponsForUnit(u, tick, nil, nil, nil, nil, nil, nil, nil)
				if sum.Fired != 0 || u.Pending&units.PendingCouldNotFire != 0 {
					t.Fatal("held attack fired or produced failure event")
				}
				q.Pump(u, tick)
				if q.LenPrimary() != 1 || q.primary[0] != head || u.SlotAt(0).Target != target {
					t.Fatal("held weapon/order visits lost explicit attack")
				}
			}
		})
	}
}
