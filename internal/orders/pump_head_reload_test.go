package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Established: Patrol can head-insert an attack before returning wait [04 R-ORD-01 §9, §10].
func TestPatrolInsertedAttackDispatchesSamePass(t *testing.T) {
	def := &content.UnitDef{BMCode: 1, CanAttack: true, CanPatrol: true, SightDistance: 400, MaxDamage: 3000}
	q, u := standingFixture(def)
	u.Flags |= units.ArmedStatus
	u.Flags = (u.Flags &^ (stanceFieldMask << stanceMoveShift)) | (2 << stanceMoveShift)
	u.Flags = (u.Flags &^ (stanceFieldMask << stanceFireShift)) | (2 << stanceFireShift)
	enemy := &units.Unit{Handle: 2, Def: &content.UnitDef{BMCode: 1, MaxDamage: 100}, Alive: true, X: 120 << 16, Z: 90 << 16, Health: 100, MaxHealth: 100}
	q.binding.Lookup = func(h pool.Handle) *units.Unit {
		if h == enemy.Handle {
			return enemy
		}
		return nil
	}
	q.binding.Weapons = &WeaponAdapter{Acquire: func(_ *units.Unit, slot int, _ uint32) (pool.Handle, bool) { return enemy.Handle, slot == 0 }}
	attack := Resolve(3, u, enemy, nil)
	called := false
	q.SetOwnedHandler(attack, func(*units.Unit, *Node, uint32, uint32) (Code, bool) { called = true; return 0, false })
	leg := q.PushHead(Lookup("Patrol"), Node{Owner: u.Handle, Phase: 2, Deadline: -1})
	q.Pump(u, 100)
	if q.primary[0] == leg {
		t.Fatal("fixture did not insert attack")
	}
	if !called {
		t.Fatalf("head-inserted %s did not dispatch in same pump", DescriptorFor(q.primary[0].ID).Name)
	}
}

// Established: re-arming the tail does not block an inserted front head
// [04 R-ORD-01 §10][04 §3.3].
func TestPrimaryRetryReloadsInsertedHead(t *testing.T) {
	q, u := standingFixture(&content.UnitDef{})
	id := Lookup("Wait")
	n := q.PushHead(id, Node{Owner: u.Handle, Phase: 5, Deadline: -1})
	called := false
	q.SetOwnedHandler(id, func(_ *units.Unit, rec *Node, _ uint32, _ uint32) (Code, bool) {
		if rec == n {
			q.PushHead(id, Node{Owner: u.Handle, Deadline: -1})
			return 9, true
		}
		called = true
		return 0, false
	})
	want := expectedDraw(q.binding.SimRNG, 30)
	before := q.binding.SimRNG.Draws()
	q.Pump(u, 100)
	if !called || q.primary[0] == n {
		t.Fatal("retry failed to dispatch the inserted head")
	}
	if n.Phase != 0 || n.DynamicGate != 1 || n.Deadline != int32(130+want) || n.Flags&FlagRetryMark == 0 {
		t.Fatalf("retry effects lost: %+v", n)
	}
	if q.binding.SimRNG.Draws()-before != 1 {
		t.Fatal("retry must draw exactly once")
	}
}

// Established: the satisfied interrupt clears targets before dispatch and does
// not alter slot posture [04 R-ORD-01 §10][04 R-ORDER-02 §3].
func TestPumpInterruptClearsTargetsBeforeHandler(t *testing.T) {
	for _, emptyLast := range []bool{false, true} {
		t.Run(fmt.Sprint(emptyLast), func(t *testing.T) {
			u, vm := cbUnit(cbProgram("TargetCleared"))
			q := &Queue{}
			BindQueue(u, q)
			flags := []uint8{units.SlotFlagEnabled | units.SlotFlagAutonomous, 0, units.SlotFlagEnabled}
			for i, f := range flags {
				u.SlotAt(i).Flags = f
				u.SlotAt(i).Target = units.Target{Kind: units.TargetUnit, Unit: 2}
			}
			if emptyLast {
				u.SlotAt(2).Target = units.Target{Kind: units.TargetNone}
			}
			id := Lookup("Attack_NoMove")
			n := q.PushHead(id, Node{Owner: u.Handle, Target: 2, Phase: 2, Deadline: -1})
			n.DynamicGate = 0x11808
			n.Satisfied = 0x10000
			called := false
			q.SetOwnedHandler(id, func(_ *units.Unit, _ *Node, satisfied uint32, _ uint32) (Code, bool) {
				called = true
				if satisfied != 0x10000 {
					t.Fatalf("interrupt=%x", satisfied)
				}
				for i, f := range flags {
					slot := u.SlotAt(i)
					if slot.Target.Kind != units.TargetNone || slot.Flags != f {
						t.Errorf("slot %d target/posture: %+v", i, slot)
					}
				}
				args := startedArgs(vm)
				count := 3
				if emptyLast {
					count = 2
				}
				if len(args) != count {
					t.Fatalf("callbacks=%v, want %d", args, count)
				}
				for i, arg := range args {
					if !argsEqual(arg, []int32{int32(i)}) {
						t.Errorf("callback %d args=%v", i, arg)
					}
				}
				return 0, false
			})
			q.Pump(u, 100)
			if !called {
				t.Fatal("handler did not run")
			}
		})
	}
}

// Established: a rear record is revisited after handling, while gated records
// are skipped [04 R-ORD-01 §10]. The final countdown delay includes zero
// [04 R-SPEC-01 §13].
func TestSecondarySelfDestructFinalDelay(t *testing.T) {
	for _, tc := range []struct {
		name         string
		state, delay uint32
	}{{"zero", 15, 0}, {"positive", 1, 7}} {
		t.Run(tc.name, func(t *testing.T) {
			q, u := selfDestructFixture(t, &content.UnitDef{})
			q.binding.SimRNG.State = tc.state
			q.PushSecondary(Lookup("SelfDestruct"), Node{Owner: u.Handle, Param2: selfDestructInitialised, Deadline: -1})
			n := q.secondary[0]
			// The blocked record ahead must be skipped on every head reload.
			blocked := &Node{ID: Lookup("BuildWeapon"), DynamicGate: 1, Deadline: -1}
			q.secondary = append([]*Node{blocked}, q.secondary...)
			before := q.binding.SimRNG.Draws()
			q.Pump(u, 100)
			if n.Param1 != 1 || q.binding.SimRNG.Draws()-before != 1 {
				t.Fatal("final countdown must arm and draw once")
			}
			if tc.delay > 0 {
				if u.Health != 3000 || n.Deadline != int32(100+tc.delay) {
					t.Fatalf("positive delay fired early: health=%d deadline=%d", u.Health, n.Deadline)
				}
				q.Pump(u, 100+tc.delay-1)
				if u.Health != 3000 {
					t.Fatal("damage before final deadline")
				}
				q.Pump(u, 100+tc.delay)
			}
			if u.Health > 0 || len(q.secondary) != 1 || q.secondary[0] != blocked {
				t.Fatalf("deadline failed to complete self-destruct: health=%d rear=%d", u.Health, len(q.secondary))
			}
			if blocked.DynamicGate != 1 || blocked.Deadline != -1 {
				t.Fatal("blocked rear record was dispatched")
			}
		})
	}
}
