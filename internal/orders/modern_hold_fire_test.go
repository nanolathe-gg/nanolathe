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

// An explicit attack issued while held is admitted, and it installs its target
// on a slot the order holds: the control byte's autonomy bit is clear, which is
// the provenance Modern's launch gate reads to let ordered fire through
// (docs/DESIGN_WEAPONS_PROJECTILES.md §2.6.1). The launch itself is locked in
// internal/combat.
func TestModernManualAttackIssuedWhileHeldTakesItsSlot(t *testing.T) {
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
			slot := u.SlotAt(0)
			if slot.Target.Kind == units.TargetNone {
				t.Fatal("held order did not install target")
			}
			if slot.IsAutonomous() {
				t.Fatal("ordered target sits on a slot the unit still owns; Hold Fire would suppress it")
			}
			modern := &combat.ModernRules{}
			if modern.HoldsFire(u, !slot.IsAutonomous()) || !modern.HoldsFire(u, false) {
				t.Fatal("Modern Hold Fire must pass the ordered slot and hold the unit's own")
			}
		})
	}
}

// holdFireStance runs one Standing_FireOrder record the way the panel issues
// it: head-inserted above the running record, handled, then consumed.
func holdFireStance(t *testing.T, q *Queue, u *units.Unit, value uint32) {
	t.Helper()
	id := Lookup("Standing_FireOrder")
	stance := q.PushHead(id, Node{Owner: u.Handle, Param1: value})
	if code := standingFireOrderHandler(u, stance, 0, 100); code != Code(5) {
		t.Fatalf("standing command returned %d", code)
	}
	q.applyPrimaryResultCode(stance, Code(5), 100)
}

// Hold Fire ends the combat a unit began on its own, at the stance write, and
// leaves what it was told to do. Strict 3.1 keeps the engagement, as retail
// does [04 R-STANCE-01 §3], and neither branch draws.
// Nanolathe Modern policy: docs/DESIGN_UNITS_ORDERS_COB.md "Modern Hold Fire".
func TestModernHoldFireRetiresAutomaticAttackAtTheWrite(t *testing.T) {
	for _, modern := range []bool{true, false} {
		t.Run(fmt.Sprintf("modern=%v", modern), func(t *testing.T) {
			q, u, enemy := dangerFixture(modern)
			u.Flags = u.Flags&^(units.StandingFieldMask<<units.StandingFireShift) | 2<<units.StandingFireShift
			patrol := &Node{ID: Lookup("Patrol"), Owner: u.Handle, Phase: 1}
			explicit := &Node{ID: Lookup("Attack_Chase"), Owner: u.Handle, Target: enemy.Handle}
			q.primary = []*Node{patrol, explicit}
			if !autoEngage(u, enemy, false) {
				t.Fatal("fire-at-will unit refused the automatic engagement")
			}
			attack := q.primary[0]
			if attack == patrol || attack.Target != enemy.Handle {
				t.Fatal("fixture did not head-insert the automatic attack")
			}
			// The running attack holds slot 0; slot 1 is still the unit's own.
			assignSlot(u, 0, units.SlotFlagEnabled, units.Target{Kind: units.TargetUnit, Unit: enemy.Handle})
			assignSlot(u, 1, units.SlotFlagEnabled|units.SlotFlagAutonomous, units.Target{Kind: units.TargetNone})
			random := *q.Binding().SimRNG

			holdFireStance(t, q, u, 0)

			if *q.Binding().SimRNG != random {
				t.Fatal("stance write drew randomness")
			}
			slot := u.SlotAt(0)
			if !modern {
				if len(q.primary) != 3 || q.primary[0] != attack || slot.IsAutonomous() || slot.Target.Unit != enemy.Handle {
					t.Fatal("Strict 3.1 lost the running engagement; retail keeps it through Hold Fire")
				}
				return
			}
			if len(q.primary) != 2 || q.primary[0] != patrol || q.primary[1] != explicit || explicit.Target != enemy.Handle {
				t.Fatalf("Hold Fire must retire the automatic attack alone: %d records", len(q.primary))
			}
			if !slot.IsAutonomous() || slot.Target.Kind != units.TargetNone {
				t.Fatal("retired attack did not hand its slot back empty; the launch gate would still pass it")
			}
			if autoEngage(u, enemy, false) || len(q.primary) != 2 {
				t.Fatal("held unit re-engaged on its own")
			}
		})
	}
}

// The stationary guard is the unit's standing assignment: it is restarted, not
// removed, its slot comes back, and while held its phase 1 takes no slot.
func TestModernHoldFireUnbindsStationaryGuard(t *testing.T) {
	for _, modern := range []bool{true, false} {
		t.Run(fmt.Sprintf("modern=%v", modern), func(t *testing.T) {
			q, u, enemy := dangerFixture(modern)
			u.Flags = u.Flags&^(units.StandingFieldMask<<units.StandingFireShift) | 2<<units.StandingFireShift
			guard := &Node{ID: Lookup("Guard_NoMove"), Owner: u.Handle, Phase: 2, DynamicGate: 0x7008, Deadline: -1}
			guard.BindTarget(enemy.Handle)
			q.primary = []*Node{guard}
			assignSlot(u, 0, units.SlotFlagEnabled, units.Target{Kind: units.TargetUnit, Unit: enemy.Handle})
			random := *q.Binding().SimRNG

			holdFireStance(t, q, u, 0)

			slot := u.SlotAt(0)
			if len(q.primary) != 1 || q.primary[0] != guard {
				t.Fatal("stance write removed the standing guard assignment")
			}
			if !modern {
				if guard.Phase != 2 || guard.Target != enemy.Handle || slot.IsAutonomous() || slot.Target.Unit != enemy.Handle {
					t.Fatal("Strict 3.1 unbound the stationary guard; retail keeps its target through Hold Fire")
				}
			} else if guard.Phase != 0 || guard.Target != 0 || guard.DynamicGate != 0 || !slot.IsAutonomous() || slot.Target.Kind != units.TargetNone {
				t.Fatalf("held stationary guard kept its engagement: %+v", *guard)
			}
			// Phase 1 with a target sitting on the unit's own slot: Strict takes
			// the slot and draws its attempt budget; Modern declines while held.
			slot.Flags |= units.SlotFlagAutonomous
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: enemy.Handle}
			guard.Phase = 1
			code := guardNoMoveHandler(u, guard, 0, 200)
			if modern {
				if code != Code(2) || !slot.IsAutonomous() || *q.Binding().SimRNG != random {
					t.Fatal("held stationary guard took a slot or drew")
				}
			} else if code != Code(1) || slot.IsAutonomous() || *q.Binding().SimRNG == random {
				t.Fatal("Strict 3.1 stationary guard lost its retail takeover")
			}
		})
	}
}

// A Modern danger response that is an attack ends at the write too, and the
// assignment it suspended restarts; a withdrawal fires nothing and stays.
func TestModernHoldFireEndsDangerAttackResponse(t *testing.T) {
	for _, attack := range []bool{true, false} {
		q, u, enemy := dangerFixture(true)
		patrol := &Node{ID: Lookup("Patrol"), Owner: u.Handle, Phase: 3}
		q.primary = []*Node{patrol}
		response := Node{Owner: u.Handle}
		name := "Move_Ground"
		if attack {
			name, response.Target = "Attack_NoMove", enemy.Handle
		}
		q.danger.response = q.PushHead(Lookup(name), response)
		q.danger.resume = patrol
		assignSlot(u, 0, units.SlotFlagEnabled, units.Target{Kind: units.TargetUnit, Unit: enemy.Handle})

		holdFireStance(t, q, u, 0)

		if !attack {
			if len(q.primary) != 2 || q.danger.response == nil || u.SlotAt(0).IsAutonomous() {
				t.Fatal("Hold Fire interrupted a withdrawal, which fires nothing")
			}
			continue
		}
		if len(q.primary) != 1 || q.primary[0] != patrol || q.danger.response != nil || patrol.Phase != 0 {
			t.Fatal("danger attack survived Hold Fire or its assignment did not restart")
		}
		if s := u.SlotAt(0); !s.IsAutonomous() || s.Target.Kind != units.TargetNone {
			t.Fatal("ended danger attack did not hand its slot back empty")
		}
	}
}
