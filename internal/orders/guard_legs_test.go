package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Legs 1 and 2 of the guard's phase 1 [04 R-UNIT-06 §1], with the standing-fire
// correction of [04 R-STANCE-01 §3]. Every gate asserted here is one of those
// sections' terms; nothing about the RE-ARM BIT PRODUCERS is asserted, because
// §1 records them as Unknown and nothing in this build raises `0x10`.

// guardLegsFixture extends the ground-guard fixture with a third unit that
// stands in for the ward's engagement target.
type guardLegsFixture struct {
	*guardFixture
	enemy *units.Unit
}

func newGuardLegsFixture(t *testing.T) *guardLegsFixture {
	t.Helper()
	f := &guardLegsFixture{guardFixture: newGuardFixture(t, 1, 1)}
	f.enemy = &units.Unit{
		Handle: 3,
		Owner:  1,
		Def:    &content.UnitDef{UnitName: "enemy", MaxDamage: 100, FootprintX: 1, FootprintZ: 1},
		Alive:  true,
		X:      numeric.Fixed(210 << 16),
		Z:      numeric.Fixed(300 << 16),
	}
	f.enemy.MaxHealth, f.enemy.Health = 100, 100
	// The guard can attack, and the resolver's code-3 armed branch needs the
	// state word's armed bit [04 R-ORD-02 §1].
	f.guard.Def.CanAttack = true
	f.guard.Def.CanMove = true
	f.guard.Move.Mode = 1
	f.guard.Flags |= units.ArmedStatus
	b := QueueForUnit(f.guard).Binding()
	b.Lookup = func(h pool.Handle) *units.Unit {
		switch h {
		case 2:
			return f.ward
		case 3:
			return f.enemy
		}
		return nil
	}
	// Leg 1's diplomacy term reads the ATTACKER's row toward the guard
	// [04 R-UNIT-06 §1 as corrected by RWU-19-13], so the fixture answers by
	// owner: the ward (owner 0) is the guard's own, the attacker (owner 1) is
	// not. The base fixture's blanket "nothing is hostile" would decline leg 1.
	b.Hostility = func(a, t *units.Unit) bool { return a != nil && t != nil && a.Owner != t.Owner }
	// Leg 1 reads row A of the ATTACKER indexed by the guard's owner
	// [04 R-UNIT-06 §1][05 R-SHARE-01 §1] — a one-directional read, not the
	// symmetric Hostility predicate above. Here: nobody has declared toward
	// anybody, so a different owner is hostile.
	b.World = &WorldQueryAdapter{DeclaresAlliance: func(from, toward uint8) bool { return from == toward }}
	QueueForUnit(f.guard).SetBinding(b)
	QueueForUnit(f.enemy).SetBinding(&QueueBinding{})
	// The ward's recorded-attacker link: the unit that last damaged it
	// [04 R-UNIT-06 §5 part 1].
	f.ward.EngagementTarget = f.enemy.Handle
	return f
}

// armSlotAutonomous puts a slot in the state the guard's own admit phase leaves
// it in: enabled, and bit 4 of the control byte SET, which [04 R-UNIT-06 §5
// part 3] establishes means "this slot belongs to autonomous acquisition".
func armSlotAutonomous(u *units.Unit, idx int, w *content.WeaponDef) {
	u.InstallWeapon(idx, w)
	u.Slots[idx].OrderControl |= units.OrderControlInhibit
}

func (f *guardLegsFixture) queueNames() []string {
	var names []string
	for _, n := range QueueForUnit(f.guard).primary {
		names = append(names, DescriptorFor(n.ID).Name)
	}
	return names
}

// TestGuardCombatJoinNeedsTheRearmBit locks leg 1's four terms and their order:
// the ward's engagement-target link, the diplomacy term, the satisfied word
// carrying the guard's re-arm bit `0x10`, and the no-chase array.
//
// The re-arm bit is "my target took damage", raised on every record observing
// the victim [04 R-UNIT-06 §5 part 2]; it is supplied here directly, the way the
// pump delivers it out of the owner's pending word.
func TestGuardCombatJoinNeedsTheRearmBit(t *testing.T) {
	f := newGuardLegsFixture(t)
	n := guardNode(f.guardFixture)
	n.Phase = 1
	q := QueueForUnit(f.guard)

	// Without the re-arm bit there is no join, whatever else holds.
	q.primary = nil
	if code := guardHandler(f.guard, n, 0, 100); code != Code(2) {
		t.Fatalf("no re-arm bit: want the maintenance hold 2, got %d", code)
	}
	if len(q.primary) != 0 {
		t.Fatalf("no re-arm bit must spawn nothing, got %v", f.queueNames())
	}

	// With it, the guard resolves the attack and head-inserts it.
	q.primary = nil
	n.DynamicGate = 0x19
	if code := guardHandler(f.guard, n, 0x10, 100); code != Code(3) {
		t.Fatalf("leg 1 should return the wait code 3, got %d", code)
	}
	names := f.queueNames()
	if len(names) == 0 || names[0] != "Attack_Chase" {
		t.Fatalf("leg 1 should head-insert the code-3 attack, got %v", names)
	}
	if q.primary[0].Target != f.enemy.Handle {
		t.Fatalf("leg 1 must attack the ward's engagement target, got %d", q.primary[0].Target)
	}
	if n.DynamicGate != 0 {
		t.Fatalf("leg 1 must clear the record gate, got %#x", n.DynamicGate)
	}
}

// TestGuardCombatJoinRespectsDiplomacyAndNoChase pins the two filter terms.
func TestGuardCombatJoinRespectsDiplomacyAndNoChase(t *testing.T) {
	f := newGuardLegsFixture(t)
	n := guardNode(f.guardFixture)
	n.Phase = 1
	q := QueueForUnit(f.guard)

	// An attacker that HAS declared alliance toward the guard is not joined:
	// the traced byte is `attackerOwner.A[guardOwner] == 0`, so a non-zero
	// declaration closes the leg [04 R-UNIT-06 §1 as corrected by RWU-19-13].
	// The ward's own rows are never consulted.
	b := q.Binding()
	b.World.DeclaresAlliance = func(from, toward uint8) bool { return true }
	q.SetBinding(b)
	q.primary = nil
	if code := guardHandler(f.guard, n, 0x10, 100); code != Code(2) || len(q.primary) != 0 {
		t.Fatalf("an attacker allied to the guard must not be joined: code %d queue %v", code, f.queueNames())
	}

	// The row is the ATTACKER's, one-directionally: a guard whose own side has
	// declared alliance to the attacker, unreciprocated, still joins. That is
	// the case the symmetric predicate would decline, and [04 R-UNIT-06 §1]'s
	// correction names it explicitly.
	b.World.DeclaresAlliance = func(from, toward uint8) bool { return from == f.guard.Owner }
	q.SetBinding(b)
	q.primary = nil
	if code := guardHandler(f.guard, n, 0x10, 100); code != Code(3) {
		t.Fatalf("a one-sided declaration BY the guard must not close leg 1, got %d", code)
	}

	b.World.DeclaresAlliance = func(from, toward uint8) bool { return from == toward }
	q.SetBinding(b)

	// A hostile WARD is irrelevant either way — only the attacker's row is
	// read — so the leg still fires when the ward itself is an enemy's unit.
	f.ward.Owner = 2
	q.primary = nil
	if code := guardHandler(f.guard, n, 0x10, 100); code != Code(3) {
		t.Fatalf("the ward's own alliance must not gate leg 1, got %d", code)
	}
	f.ward.Owner = 0

	// A target in the guard's no-chase array is not joined either.
	f.enemy.Def.UnitMask.Words[0] = 1 << 5
	f.guard.Def.NoChaseCategoryMask.Words[0] = 1 << 5
	q.primary = nil
	if code := guardHandler(f.guard, n, 0x10, 100); code != Code(2) || len(q.primary) != 0 {
		t.Fatalf("a no-chase target must not be joined: code %d queue %v", code, f.queueNames())
	}

	f.guard.Def.NoChaseCategoryMask = content.CategoryMask{}
	q.primary = nil
	if code := guardHandler(f.guard, n, 0x10, 100); code != Code(3) {
		t.Fatalf("with the array cleared the join should fire, got %d", code)
	}
}

// TestGuardSlotRetargetGatesOnTheFireFieldAlone locks leg 2's own gate as
// [04 R-STANCE-01 §3] corrects it: the standing FIRE field, and not the
// standing MOVE field, which is not read anywhere in either guard handler.
func TestGuardSlotRetargetGatesOnTheFireFieldAlone(t *testing.T) {
	f := newGuardLegsFixture(t)
	n := guardNode(f.guardFixture)
	n.Phase = 1
	armSlotAutonomous(f.guard, 0, &content.WeaponDef{Name: "gun", Range: 1000})

	// Hold fire with hold position: no rebind.
	f.guard.Flags &^= (stanceFieldMask << stanceFireShift) | (stanceFieldMask << stanceMoveShift)
	guardHandler(f.guard, n, 0, 100)
	if f.guard.Slots[0].Target.Kind == units.TargetUnit {
		t.Fatalf("a hold-fire guard must not rebind a slot, got %+v", f.guard.Slots[0].Target)
	}

	// Hold position with return fire: the move field is not consulted, so the
	// rebind still happens. This is the correction; the superseded wording
	// would have skipped the leg because the move field is zero.
	f.guard.Flags |= 1 << stanceFireShift
	guardHandler(f.guard, n, 0, 100)
	if f.guard.Slots[0].Target.Kind != units.TargetUnit || f.guard.Slots[0].Target.Unit != f.enemy.Handle {
		t.Fatalf("the fire field alone must admit the rebind, got %+v", f.guard.Slots[0].Target)
	}
}

// TestGuardSlotRetargetRebindConditions locks which slots are rebound and which
// are left alone [04 R-UNIT-06 §1] leg 2, including the command-fire exclusion.
func TestGuardSlotRetargetRebindConditions(t *testing.T) {
	f := newGuardLegsFixture(t)
	n := guardNode(f.guardFixture)
	n.Phase = 1
	f.guard.Flags |= 2 << stanceFireShift // fire at will

	// A fourth unit, in range of slot 0 and out of range of slot 1.
	near := &units.Unit{
		Handle: 4, Owner: 1, Alive: true,
		Def: &content.UnitDef{UnitName: "near", MaxDamage: 100},
		X:   numeric.Fixed(205 << 16), Z: numeric.Fixed(300 << 16),
	}
	near.MaxHealth, near.Health = 100, 100
	b := QueueForUnit(f.guard).Binding()
	prev := b.Lookup
	b.Lookup = func(h pool.Handle) *units.Unit {
		if h == 4 {
			return near
		}
		return prev(h)
	}
	QueueForUnit(f.guard).SetBinding(b)

	armSlotAutonomous(f.guard, 0, &content.WeaponDef{Name: "long", Range: 1000})
	armSlotAutonomous(f.guard, 1, &content.WeaponDef{Name: "short", Range: 1})
	armSlotAutonomous(f.guard, 2, &content.WeaponDef{Name: "dgun", Range: 1000, CommandFire: true})

	held := units.Target{Kind: units.TargetUnit, Unit: near.Handle}
	f.guard.Slots[0].Target = held
	f.guard.Slots[1].Target = held
	f.guard.Slots[2].Target = held

	guardHandler(f.guard, n, 0, 100)

	// Slot 0 holds a legal in-range target that no bad-target array excludes,
	// so it is left alone.
	if f.guard.Slots[0].Target != held {
		t.Fatalf("slot 0 holds a legal in-range target and must be untouched, got %+v", f.guard.Slots[0].Target)
	}
	// Slot 1's target is out of its weapon's range, so it is rebound.
	if f.guard.Slots[1].Target.Unit != f.enemy.Handle {
		t.Fatalf("slot 1's out-of-range target must be rebound, got %+v", f.guard.Slots[1].Target)
	}
	// Slot 2's weapon is command-fire only and is never touched.
	if f.guard.Slots[2].Target != held {
		t.Fatalf("a command-fire slot must be skipped, got %+v", f.guard.Slots[2].Target)
	}

	// A slot an attack order currently holds — control-byte bit 4 CLEAR — is
	// left alone whatever its target, because it does not belong to autonomous
	// acquisition [04 R-UNIT-06 §5 part 3]. This is the term WU-19-35 skipped.
	f.guard.Slots[1].Target = held
	f.guard.Slots[1].OrderControl &^= units.OrderControlInhibit
	guardHandler(f.guard, n, 0, 100)
	if f.guard.Slots[1].Target != held {
		t.Fatalf("a slot held by an order must be skipped, got %+v", f.guard.Slots[1].Target)
	}
	f.guard.Slots[1].OrderControl |= units.OrderControlInhibit

	// The per-slot bad-target array is the third rebind condition.
	f.guard.Slots[0].Target = held
	near.Def.UnitMask.Words[0] = 1 << 3
	f.guard.Def.BadTargetCategoryWPRIMask.Words[0] = 1 << 3
	guardHandler(f.guard, n, 0, 100)
	if f.guard.Slots[0].Target.Unit != f.enemy.Handle {
		t.Fatalf("a bad-target-categorised slot target must be rebound, got %+v", f.guard.Slots[0].Target)
	}
}

// TestGuardKeepsNoLatchAcrossTicks is the negative [04 R-UNIT-06 §1] states
// outright: "there is **no** dedup array and no latch in either guard handler".
// Repeated visits spawn repeatedly, and the retired per-unit arrays stay zero.
func TestGuardKeepsNoLatchAcrossTicks(t *testing.T) {
	f := newGuardLegsFixture(t)
	n := guardNode(f.guardFixture)
	n.Phase = 1
	q := QueueForUnit(f.guard)

	// A damaged, finished ward takes leg 3 on every visit.
	f.ward.Health = 40
	f.guard.Def.Builder = true
	f.guard.Def.CanReclamate = true

	for visit := 0; visit < 3; visit++ {
		q.primary = nil
		if code := guardHandler(f.guard, n, 0, uint32(100*visit)); code != Code(3) {
			t.Fatalf("visit %d: a latch-free leg 3 must fire every time, got %d", visit, code)
		}
		if len(q.primary) == 0 || DescriptorFor(q.primary[0].ID).Name != "RepairUnit" {
			t.Fatalf("visit %d: want RepairUnit, got %v", visit, f.queueNames())
		}
	}

	// The per-unit dedup arrays this build used to keep are gone from
	// units.Unit entirely, so there is no latch state left to assert on: the
	// loop above IS the contract. If a latch is ever reintroduced, the second
	// iteration fails.
}

// TestGuardJoinsAfterItsWardIsHit is the end-to-end shape of legs 1's two
// producers [04 R-UNIT-06 §5]: the damage dispatcher writes the ward's recorded
// attacker, the damage reaction raises pending 0x10 on every record observing
// the ward, and the guard's next admitted visit joins the fight. A link naming
// a slot that is no longer live is dropped at the read, because §5 puts the
// liveness burden on the reader and nothing clears the link on death.
func TestGuardJoinsAfterItsWardIsHit(t *testing.T) {
	f := newGuardLegsFixture(t)
	n := guardNode(f.guardFixture)
	n.Phase = 1
	q := QueueForUnit(f.guard)

	// Before the ward is hit there is no link and the guard only follows.
	f.ward.EngagementTarget = 0
	q.primary = nil
	if code := guardHandler(f.guard, n, 0x10, 100); code != Code(2) || len(q.primary) != 0 {
		t.Fatalf("no recorded attacker: want the maintenance hold, got %d / %v", code, f.queueNames())
	}

	// The dispatcher's write, then the wake bit the observer notice delivers.
	f.ward.EngagementTarget = f.enemy.Handle
	q.primary = nil
	if code := guardHandler(f.guard, n, 0x10, 100); code != Code(3) {
		t.Fatalf("a hit ward must be avenged on the next admitted visit, got %d", code)
	}
	if len(q.primary) == 0 || q.primary[0].Target != f.enemy.Handle {
		t.Fatalf("leg 1 must attack the recorded attacker, got %v", f.queueNames())
	}

	// A link to a dead slot is not a target: the reader tests liveness.
	f.enemy.Alive = false
	q.primary = nil
	if code := guardHandler(f.guard, n, 0x10, 100); code != Code(2) || len(q.primary) != 0 {
		t.Fatalf("a dead recorded attacker must be dropped at the read, got %d / %v", code, f.queueNames())
	}
	f.enemy.Alive = true

	// Leg 2 reads the same link, so it also declines once the slot is dying.
	f.guard.Flags |= 2 << stanceFireShift
	armSlotAutonomous(f.guard, 0, &content.WeaponDef{Name: "gun", Range: 1000})
	f.enemy.Dying = true
	guardHandler(f.guard, n, 0, 100)
	if f.guard.Slots[0].Target.Kind == units.TargetUnit {
		t.Fatalf("leg 2 must not rebind onto a dying attacker, got %+v", f.guard.Slots[0].Target)
	}
	f.enemy.Dying = false
}
