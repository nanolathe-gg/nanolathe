package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Both follow handlers keep slot support inside the damage-triggered combat
// join, and only reach it after forced insertion fails [04 R-UNIT-06 §1].
func TestGuardFallbackRequiresFailedDamageJoin(t *testing.T) {
	for _, variant := range []string{"ground", "air"} {
		for _, tc := range []struct {
			name      string
			configure func(*guardLegsFixture)
			wake      uint32
			join      bool
			retarget  bool
		}{
			{name: "maintenance deadline", wake: 1},
			{name: "no recorded attacker", wake: guardCombatJoinBit, configure: func(f *guardLegsFixture) { f.ward.EngagementTarget = 0 }},
			{name: "allied attacker", wake: guardCombatJoinBit, configure: func(f *guardLegsFixture) {
				QueueForUnit(f.guard).Binding().World.DeclaresAlliance = func(_, _ uint8) bool { return true }
			}},
			{name: "excluded category", wake: guardCombatJoinBit, configure: func(f *guardLegsFixture) {
				f.enemy.Def.UnitMask.Words[0] = 1
				f.guard.Def.NoChaseCategoryMask.Words[0] = 1
			}},
			{name: "forced join succeeds", wake: guardCombatJoinBit, join: true, configure: func(f *guardLegsFixture) { f.guard.Def.CanAttack = true }},
			{name: "forced join fails", wake: guardCombatJoinBit, retarget: true},
		} {
			t.Run(variant+"/"+tc.name, func(t *testing.T) {
				f := newGuardLegsFixture(t)
				f.guard.Def.CanFly = variant == "air"
				f.guard.Def.CanAttack = false // make command resolution refuse the join
				f.guard.Flags |= 1 << stanceFireShift
				armSlotAutonomous(f.guard, 0, &content.WeaponDef{Name: "gun", Range: 1000})
				q := QueueForUnit(f.guard)
				n := guardNode(f.guardFixture)
				n.Phase = 1
				if variant == "air" {
					n.ID, n.Phase = Lookup("VTOL_Follow"), 2
				}
				q.primary = []*Node{n}
				if tc.configure != nil {
					tc.configure(f)
				}
				random := *f.sim
				code := guardHandler(f.guard, n, tc.wake, 100)
				if tc.join {
					if code != Code(3) || len(q.primary) != 2 || q.primary[1] != n || q.primary[0].Target != f.enemy.Handle || n.DynamicGate != 0 {
						t.Fatalf("forced join: code=%d queue=%v gate=%#x", code, f.queueNames(), n.DynamicGate)
					}
				} else if code != Code(2) || len(q.primary) != 1 || q.primary[0] != n || n.Deadline != 130 {
					t.Fatalf("maintenance: code=%d queue=%v deadline=%d", code, f.queueNames(), n.Deadline)
				}
				want := units.Target{}
				if tc.retarget {
					want = units.Target{Kind: units.TargetUnit, Unit: f.enemy.Handle}
				}
				if got := f.guard.Slots[0].Target; got != want {
					t.Fatalf("slot target=%+v, want %+v", got, want)
				}
				if *f.sim != random {
					t.Fatal("combat join/fallback advanced RNG")
				}
			})
		}
	}
}

// A nearby target can still fail medium, air or ballistic admission. The
// guard must ask the shared shot gate before retaining it [04 R-UNIT-06 §1].
func TestGuardRetainedTargetNeedsShotAdmission(t *testing.T) {
	for _, variant := range []string{"ground", "air"} {
		for _, admitted := range []bool{false, true} {
			name := "refused"
			if admitted {
				name = "admitted"
			}
			t.Run(variant+"/"+name, func(t *testing.T) {
				f := newGuardLegsFixture(t)
				f.guard.Def.CanFly = variant == "air"
				f.guard.Def.CanAttack = false
				f.guard.Flags |= 1 << stanceFireShift
				armSlotAutonomous(f.guard, 0, &content.WeaponDef{Name: "gun", Range: 1000})
				held := &units.Unit{Handle: 4, Owner: 1, Alive: true, Def: &content.UnitDef{}, X: f.guard.X, Z: f.guard.Z}
				f.guard.Slots[0].Target = units.Target{Kind: units.TargetUnit, Unit: held.Handle}
				b := QueueForUnit(f.guard).Binding()
				lookup := b.Lookup
				b.Lookup = func(h pool.Handle) *units.Unit {
					if h == held.Handle {
						return held
					}
					return lookup(h)
				}
				calls := 0
				b.Weapons = &WeaponAdapter{CanEngage: func(actor *units.Unit, target pool.Handle, slot int) bool {
					calls++
					if actor != f.guard || target != held.Handle || slot != 0 {
						t.Fatalf("shot gate actor=%p target=%d slot=%d", actor, target, slot)
					}
					return admitted
				}}
				n := guardNode(f.guardFixture)
				n.Phase = 1
				if variant == "air" {
					n.ID, n.Phase = Lookup("VTOL_Follow"), 2
				}
				guardHandler(f.guard, n, guardCombatJoinBit, 100)
				want := f.enemy.Handle
				if admitted {
					want = held.Handle
				}
				if got := f.guard.Slots[0].Target.Unit; got != want || calls != 1 {
					t.Fatalf("retained target=%d, want %d; shot gate calls=%d, want 1", got, want, calls)
				}
			})
		}
	}
}
