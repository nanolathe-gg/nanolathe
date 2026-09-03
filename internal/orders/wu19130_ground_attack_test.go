package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestCode3NotHostileArmNeedsNoPosition locks the condition [04 R-ORD-02 §1]
// states for code 3's first arm: "Not hostile (**friendly or no target**)".
// There is no position term in it, and adding one broke the manual D-gun.
//
// `AttackSpecial`'s whole body is "resolve command code 3 against the record's
// target WITH NO POSITION, re-identify this record as the result, set p1 = 2,
// return hold" [04 R-ORD-01 §2]. So the only call that ever reaches this arm
// through the D-gun carries a nil position, and a build that demanded one
// resolved the reject sentinel instead — descriptor 0, an unconditional
// complete [04 R-ORD-01 §12] — leaving a ground D-gun with no visible effect
// at all.
func TestCode3NotHostileArmNeedsNoPosition(t *testing.T) {
	_, u := gateFixture()
	u.Def.CanAttack = true
	u.Flags |= units.ArmedStatus

	if got := resolveAttackAt(u, nil, nil); got != "Suppress" {
		t.Fatalf("code 3 with no target and no position = %q, want Suppress [04 R-ORD-02 §1]", got)
	}
	if got := resolveAttackAt(u, nil, &ResolvePos{}); got != "Suppress" {
		t.Fatalf("code 3 with no target and a position = %q, want Suppress [04 R-ORD-02 §1]", got)
	}
	// The arm runs only under the armed bit; an unarmed unit falls straight to
	// the kamikaze test [04 R-ORD-02 §1].
	u.Flags &^= units.ArmedStatus
	if got := resolveAttackAt(u, nil, &ResolvePos{}); got != "" {
		t.Fatalf("unarmed code 3 = %q, want the reject [04 R-ORD-02 §1]", got)
	}
}

// TestCode3NotHostileArmRejectsAnAntiAirSlotZero locks the reject that opens
// the same arm: "*w0* has `toairweapon` → reject" [04 R-ORD-02 §1], with *w0*
// the acting unit's weapon slot 0.
func TestCode3NotHostileArmRejectsAnAntiAirSlotZero(t *testing.T) {
	_, u := gateFixture()
	u.Def.CanAttack = true
	u.Flags |= units.ArmedStatus
	u.InstallWeapon(0, &content.WeaponDef{Range: 180, ToAirWeapon: true})

	if got := resolveAttackAt(u, nil, &ResolvePos{}); got != "" {
		t.Fatalf("anti-air slot 0 ordered onto ground = %q, want the reject [04 R-ORD-02 §1]", got)
	}
}

// TestAttackSpecialOnGroundBecomesSuppressOnSlotTwo is the D-gun end of the
// same chain, and the reason `Suppress` phase 1 carries a `p1 = 2` arm at all
// [04 R-ORD-01 §3]: `AttackSpecial` is the only writer of that parameter
// [04 R-ORD-01 §2], so if a target-less special attack could not resolve, the
// arm that releases all three slots and binds slot 2 to the goal was
// unreachable.
func TestAttackSpecialOnGroundBecomesSuppressOnSlotTwo(t *testing.T) {
	q, u := gateFixture()
	u.Def.CanAttack = true
	u.Def.CanDGun = true
	u.Flags |= units.ArmedStatus

	id := Resolve(4, u, nil, &ResolvePos{})
	if got := DescriptorFor(id).Name; got != "AttackSpecial" {
		t.Fatalf("code 4 on a candgun unit = %q, want AttackSpecial [04 R-ORD-02 §1]", got)
	}
	goalX, goalZ := numeric.Fixed(120<<16), numeric.Fixed(64<<16)
	q.Push(id, Node{Owner: u.Handle, GoalX: goalX, GoalZ: goalZ})
	q.Pump(u, 40)

	if q.LenPrimary() == 0 {
		t.Fatalf("the ground D-gun record completed silently: it re-identified as descriptor 0 [04 R-ORD-01 §12]")
	}
	n := q.Primary()[0]
	if got := DescriptorFor(n.ID).Name; got != "Suppress" {
		t.Fatalf("record re-identified as %q, want Suppress [04 R-ORD-01 §2][04 R-ORD-02 §1]", got)
	}
	if n.Param1 != 2 {
		t.Fatalf("p1 = %d, want the weapon-slot selection 2 [04 R-ORD-01 §2]", n.Param1)
	}
	if got := u.SlotAt(2).Target; got.Kind != units.TargetGround || got.X != goalX || got.Z != goalZ {
		t.Fatalf("slot 2 target = %+v, want the goal point bound [04 R-ORD-01 §3]", got)
	}
	for _, idx := range []int{0, 1} {
		if u.SlotAt(idx).Target.Kind != units.TargetNone {
			t.Fatalf("slot %d holds a target; the p1 = 2 arm releases all three [04 R-ORD-01 §3]", idx)
		}
	}
}
