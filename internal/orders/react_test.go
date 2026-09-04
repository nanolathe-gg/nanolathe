package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestObserverNoticeRaisesPendingBitOnObservers locks part 1 of the
// damage-intake reaction routine [06 R-WPN-04 §2 part 1]: every order record
// observing the damaged unit receives event code 16, and an event code IS a
// pending bit — the handler ORs it into the pending word, so the notice is
// pending bit 0x10, the producer [04 R-ORD-01 §6] could not locate and
// [04 R-MOV-03 §7] closed. A record whose descriptor does not carry the
// target-observer bit 0x200 never observes its target and stays asleep.
func TestObserverNoticeRaisesPendingBitOnObservers(t *testing.T) {
	cat := &content.Catalog{}
	w := newOrdersFixtureWorld(20, cat)
	def := &content.UnitDef{UnitName: "reactflea", CanMove: true, CanAttack: true, MaxDamage: 100}
	victimH, _ := w.Create(def, 0, 0, 0, 0)
	watcherH, _ := w.Create(def, 1, 0, 0, 0)
	strangerH, _ := w.Create(def, 1, 0, 0, 0)
	victim, watcher, stranger := w.Unit(victimH), w.Unit(watcherH), w.Unit(strangerH)

	chase := Lookup("Attack_Chase")
	if chase == 0 {
		t.Fatal("Attack_Chase is unavailable")
	}
	// Attack_Chase's static mask is 0x280 — bit 9 (target) and bit 7 (the
	// under-attack silence) [04 §3.1].
	QueueForUnit(watcher).Push(chase, Node{Owner: watcherH, Target: victimH})
	// The stranger observes somebody else.
	QueueForUnit(stranger).Push(chase, Node{Owner: strangerH, Target: watcherH})

	ObserverNotice(w, victim)
	if watcher.Pending&observerNotice == 0 {
		t.Fatalf("the observer notice left pending=%#x; the record observing the victim must wake with 0x10 [04 R-MOV-03 §7]", watcher.Pending)
	}
	if stranger.Pending&observerNotice != 0 {
		t.Fatal("a record observing another unit must not wake [06 R-WPN-04 §2]")
	}

	// A record whose descriptor lacks bit 0x200 is never linked onto the
	// target's observer list [04 R-MOV-03 §7]. `Wait` carries mask 0x4.
	wait := Lookup("Wait")
	if wait == 0 {
		t.Fatal("Wait is unavailable")
	}
	stranger.Pending = 0
	QueueForUnit(stranger).PurgeUnprotected()
	QueueForUnit(stranger).SetPrimary(nil)
	QueueForUnit(stranger).Push(wait, Node{Owner: strangerH, Target: victimH})
	ObserverNotice(w, victim)
	if stranger.Pending&observerNotice != 0 {
		t.Fatalf("a record whose static mask lacks 0x200 observed its target: pending=%#x [04 R-MOV-03 §7]", stranger.Pending)
	}
}

// TestFrontPrimaryGateMaskAndUnderAttackSilence locks part 4's read
// [06 R-WPN-04 §2 part 4]: the gate-mask word of the victim's FRONT PRIMARY
// order, zero when it has none, and bit 7 silences the notice. Bit 7 is
// statically set on `Attack_NoMove`, `Attack_Chase` and `AttackSpecial`
// [04 §3.1], so a unit already attacking never announces `Under Attack`.
func TestFrontPrimaryGateMaskAndUnderAttackSilence(t *testing.T) {
	cat := &content.Catalog{}
	w := newOrdersFixtureWorld(8, cat)
	def := &content.UnitDef{UnitName: "reactgate", CanMove: true, MaxDamage: 100}
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)

	if got := FrontPrimaryGateMask(u); got != 0 {
		t.Fatalf("an empty front segment gave gate mask %#x, want 0", got)
	}
	if UnderAttackSilenced(u) {
		t.Fatal("a unit with no front order must not silence the notice")
	}

	move := Lookup("Move_Ground")
	QueueForUnit(u).Push(move, Node{Owner: h})
	if UnderAttackSilenced(u) {
		t.Fatalf("Move_Ground (mask %#x) must not silence the notice: bit 7 is not in it [04 §3.1]", FrontPrimaryGateMask(u))
	}

	QueueForUnit(u).SetPrimary(nil)
	chase := Lookup("Attack_Chase")
	QueueForUnit(u).Push(chase, Node{Owner: h})
	if !UnderAttackSilenced(u) {
		t.Fatalf("Attack_Chase (mask %#x) carries bit 7 and must silence the notice [06 R-WPN-04 §2]", FrontPrimaryGateMask(u))
	}
}

// TestRetaliationOrderSpawnsTheResolvedAttackRecord locks the order branch of
// [08 R-AI-01 §11]: the victim is given an attack order against the attacker
// through the ordinary order service, which is the shared auto-engage issuer
// with force = 0 [04 R-STANCE-01 §3] resolving command code 3
// [04 R-ORD-02 §1]. Hold fire and hold position each refuse it on their own.
func TestRetaliationOrderSpawnsTheResolvedAttackRecord(t *testing.T) {
	cat := &content.Catalog{}
	w := newOrdersFixtureWorld(8, cat)
	def := &content.UnitDef{UnitName: "reactgun", CanAttack: true, MaxDamage: 100}
	victimH, _ := w.Create(def, 0, 0, 0, 0)
	attackerH, _ := w.Create(def, 1, 0, 0, 0)
	victim, attacker := w.Unit(victimH), w.Unit(attackerH)
	// Armed (status bit 31) and immobile (bit 29) resolves `Attack_NoMove`
	// [04 R-ORD-02 §1].
	victim.Flags |= units.ArmedStatus | units.BuildingClassStatus
	victim.Flags |= 2<<units.StandingMoveShift | 2<<units.StandingFireShift
	// The session's seam binds the queue before calling in; do the same here.
	QueueForUnit(victim)

	if !RetaliationOrder(victim, attacker) {
		t.Fatal("an idle armed victim did not receive the retaliation record [08 R-AI-01 §11]")
	}
	primary := QueueForUnit(victim).Primary()
	if len(primary) == 0 {
		t.Fatal("no record was inserted")
	}
	if got := DescriptorFor(primary[0].ID).Name; got != "Attack_NoMove" {
		t.Fatalf("retaliation record = %q, want Attack_NoMove [04 R-ORD-02 §1]", got)
	}
	if primary[0].Target != attackerH {
		t.Fatalf("retaliation record targets %d, want the attacker %d", primary[0].Target, attackerH)
	}

	// A victim that already has a front order takes the per-slot offer instead;
	// only the "no current order" arm is admitted here. The other arm needs the
	// front descriptor's standby interruptible bit, bit 17 of the static mask
	// [04 §3.1][04 R-STANCE-01 §3] — see RetaliationOrder's own contract.
	if RetaliationOrder(victim, attacker) {
		t.Fatal("a victim with a front order must not receive a second retaliation record")
	}

	// Hold fire, then hold position: the issuer refuses on either zero
	// [04 R-STANCE-01 §3].
	QueueForUnit(victim).SetPrimary(nil)
	victim.Flags &^= units.StandingFieldMask << units.StandingFireShift
	if RetaliationOrder(victim, attacker) {
		t.Fatal("hold fire must refuse autonomous engagement [04 R-STANCE-01 §3]")
	}
	victim.Flags |= 2 << units.StandingFireShift
	victim.Flags &^= units.StandingFieldMask << units.StandingMoveShift
	if RetaliationOrder(victim, attacker) {
		t.Fatal("hold position must refuse autonomous engagement [04 R-STANCE-01 §3]")
	}
}

// TestRetaliationOrderRespectsNoChaseAndBadTarget locks the category half of
// the order branch's admission: the attacker's type must be absent from both
// the victim definition's no-chase and bad-target category bitsets
// [08 R-AI-01 §11][06 §3.2].
func TestRetaliationOrderRespectsNoChaseAndBadTarget(t *testing.T) {
	cat := &content.Catalog{}
	w := newOrdersFixtureWorld(8, cat)
	attackerDef := &content.UnitDef{UnitName: "reactvtol", CanAttack: true, MaxDamage: 100}
	attackerDef.UnitMask.Words[0] = 1 << 5
	victimDef := &content.UnitDef{UnitName: "reactgun2", CanAttack: true, MaxDamage: 100}
	victimDef.NoChaseCategoryMask.Words[0] = 1 << 5

	victimH, _ := w.Create(victimDef, 0, 0, 0, 0)
	attackerH, _ := w.Create(attackerDef, 1, 0, 0, 0)
	victim, attacker := w.Unit(victimH), w.Unit(attackerH)
	victim.Flags |= units.ArmedStatus | units.BuildingClassStatus
	victim.Flags |= 2<<units.StandingMoveShift | 2<<units.StandingFireShift
	QueueForUnit(victim)

	if RetaliationOrder(victim, attacker) {
		t.Fatal("a no-chase attacker must not draw a counter-order [08 R-AI-01 §11]")
	}
	victimDef.NoChaseCategoryMask = content.CategoryMask{}
	victimDef.BadTargetCategoryWPRIMask.Words[0] = 1 << 5
	if RetaliationOrder(victim, attacker) {
		t.Fatal("a bad-target attacker must not draw a counter-order [08 R-AI-01 §11]")
	}
	victimDef.BadTargetCategoryWPRIMask = content.CategoryMask{}
	if !RetaliationOrder(victim, attacker) {
		t.Fatal("an admissible attacker must draw the counter-order [08 R-AI-01 §11]")
	}
}

// TestStopCurrentOrderIssuesTheOrdinaryStop locks the throttle's second half
// [08 R-AI-01 §11]: after writing the manager throttle deadline the engine
// clears the damaged unit's current order through the ordinary stop path.
func TestStopCurrentOrderIssuesTheOrdinaryStop(t *testing.T) {
	cat := &content.Catalog{}
	w := newOrdersFixtureWorld(8, cat)
	def := &content.UnitDef{UnitName: "reactbuilder", CanMove: true, CanCapture: true, MaxDamage: 100}
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	move := Lookup("Move_Ground")
	QueueForUnit(u).Push(move, Node{Owner: h})

	StopCurrentOrder(u, 11)
	primary := QueueForUnit(u).Primary()
	if len(primary) == 0 {
		t.Fatal("the stop path inserted no record")
	}
	if got := DescriptorFor(primary[0].ID).Name; got != "Stop" {
		t.Fatalf("front primary order after the stop = %q, want Stop [04 R-ORD-01 §2]", got)
	}
	if primary[0].CreationTick != 11 {
		t.Fatalf("stop record creation tick = %d, want 11 [04 §3.2]", primary[0].CreationTick)
	}
}
