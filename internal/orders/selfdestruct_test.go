package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestSelfDestructFGDispatchesOnSightAndDestroysTheUnit is this unit's first
// test, because `SelfDestructFG` is the one order in the family a live game
// already reaches: the initial-mission interpreter's `d` token queues it
// (internal/mission/initial_mission.go). Before this handler existed the record
// dispatched on sight — its static mask is zero — found no handler, and parked
// for 30 to 44 ticks with a diagnostic, forever.
//
// The row's timeline [04 R-SPEC-01 §13]: the first visit initialises p2 from
// the definition and takes the first countdown step, arming the cancel-current
// gate bit and its own deadline; the visits that follow walk the count down and
// then apply 30000 self-damage with cause 3 and complete.
//
// It is also this package's front-segment tick test (WU-18-7). `SelfDestructFG`
// carries a zero static mask, so it is a PRIMARY record, and the deadline it
// arms is `current tick + 30` measured from the tick the handler ran on
// [04 R-ORD-01 §1]. The first pump is deliberately at a tick that is neither 0
// nor the tick the rear-segment walk would have published: before WU-18-7 the
// handler armed against the rear-segment tick, which no primary walk ever
// wrote, so every step of this countdown was already expired when it was armed
// and one pump ran the whole timeline to the unit's death.
func TestSelfDestructFGDispatchesOnSightAndDestroysTheUnit(t *testing.T) {
	id := Lookup("SelfDestructFG")
	if id == 0 {
		t.Fatal("SelfDestructFG is not in the descriptor table")
	}
	if DescriptorFor(id).Handler == nil {
		t.Fatal("SelfDestructFG has no handler after installation [04 §3.1]")
	}
	q, u := selfDestructFixture(t, nil) // no authored countdown: the field defaults to 5

	const firstTick = 7
	q.Push(id, Node{Owner: u.Handle})
	q.Pump(u, firstTick)

	if diags := q.Diagnostics(); len(diags) != 0 {
		t.Fatalf("dispatch recorded %v, want none", diags)
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("primary length = %d, want the counting record kept", q.LenPrimary())
	}
	n := q.Primary()[0]
	if n.Param2&selfDestructMarkerMask == 0 {
		t.Fatalf("p2 = %#x, want the initialisation marker in its high nibble [04 R-ORD-01 §2]", n.Param2)
	}
	if got := n.Param2 & selfDestructCountField; got != 4 {
		t.Fatalf("remaining count = %d after one step from the default 5, want 4", got)
	}
	if n.DynamicGate&gateCancelBit == 0 {
		t.Fatalf("gate = %#x, want the cancel-current bit armed on every counting visit [04 R-ORD-01 §2]", n.DynamicGate)
	}
	// The deadline setter's own two effects [04 R-ORD-01 §1]: bit 0 in the
	// dynamic gate, and `current tick + n` — measured from THIS pump's tick.
	if n.DynamicGate&1 == 0 || n.Deadline != firstTick+int32(selfDestructStep) {
		t.Fatalf("gate = %#x deadline = %d, want gate bit 0 and %d = tick %d + one countdown step [04 R-ORD-01 §1]", n.DynamicGate, n.Deadline, firstTick+int32(selfDestructStep), firstTick)
	}
	if u.Dying {
		t.Fatal("the unit died on the first countdown step")
	}

	// One step per wake: a pump before the deadline changes nothing.
	before := *n
	q.Pump(u, firstTick+1)
	if q.LenPrimary() != 1 || n.Param2 != before.Param2 || n.Deadline != before.Deadline {
		t.Fatalf("a pump inside the wait advanced the countdown: p2 %#x -> %#x, deadline %d -> %d", before.Param2, n.Param2, before.Deadline, n.Deadline)
	}

	// Let the remaining steps and the damage run, one wake at a time. The last
	// two waits are shorter than a step (the RNG(15) arm and the damage visit),
	// so stepping by a full step covers every one of them.
	visits := 1
	for tick := firstTick + int32(selfDestructStep); q.LenPrimary() > 0 && visits < 16; tick += int32(selfDestructStep) {
		q.Pump(u, uint32(tick))
		visits++
	}

	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d after %d wakes, want the finished record freed [04 R-ORD-01 §2]", q.LenPrimary(), visits)
	}
	if visits < 6 {
		t.Fatalf("the countdown finished in %d wakes; the default field is 5 steps, so a shorter run means a step ran without its deadline [04 R-SPEC-01 §13]", visits)
	}
	if !u.Dying {
		t.Fatal("the countdown finished without applying its damage")
	}
	if u.DeathCause != units.DeathSelfDestruct {
		t.Fatalf("death cause = %d, want the self-destruct cause 3 [06 §12.1]", u.DeathCause)
	}
	if u.Health > 0 {
		t.Fatalf("health = %d, want the 30000 self-damage to be lethal [04 R-SPEC-01 §1]", u.Health)
	}
}

// TestSelfDestructWithNoCountdownFieldFiresOnItsFirstVisit locks the immediate
// arm: an authored `selfdestructcountdown` of 0 leaves the field zero, so the
// counting branch is never entered and the first visit applies the damage
// [04 R-ORD-01 §2][04 R-SPEC-01 §13]. This is also the shape `Attack_Kamikaze`
// and `Standby_Mine` reach by spawning the record with p1 = 1.
func TestSelfDestructWithNoCountdownFieldFiresOnItsFirstVisit(t *testing.T) {
	def := &content.UnitDef{SelfDestructCountdown: "0", SelfDestructCountdownPresent: true}
	q, u := selfDestructFixture(t, def)
	q.Push(Lookup("SelfDestructFG"), Node{Owner: u.Handle})
	q.Pump(u, 0)
	if q.LenPrimary() != 0 || !u.Dying {
		t.Fatalf("a zero countdown must fire on sight: primary=%d dying=%t", q.LenPrimary(), u.Dying)
	}

	// p1 = 1 is the spawned form; it skips the countdown regardless of the field.
	q2, u2 := selfDestructFixture(t, nil)
	q2.Push(Lookup("SelfDestructFG"), Node{Owner: u2.Handle, Param1: 1})
	q2.Pump(u2, 0)
	if q2.LenPrimary() != 0 || !u2.Dying {
		t.Fatalf("p1 = 1 must fire on sight: primary=%d dying=%t", q2.LenPrimary(), u2.Dying)
	}
}

// TestSelfDestructCancelNotificationCompletesWithoutDamage locks the cancel
// path and the mechanism behind it. Gate bit 1 has no bit writer anywhere: the
// notification is delivered by the removal paths, which invoke the removed
// record's own handler with mask 2 while that bit is still armed
// [04 R-ORD-01 §0][R-ORDER-02 §2]. Arming it on every counting visit is what
// makes a re-issue or a purge terminate the countdown instead of detonating.
func TestSelfDestructCancelNotificationCompletesWithoutDamage(t *testing.T) {
	q, u := selfDestructFixture(t, nil)
	q.Push(Lookup("SelfDestructFG"), Node{Owner: u.Handle})
	q.Pump(u, 0)
	if q.Primary()[0].DynamicGate&gateCancelBit == 0 {
		t.Fatal("the counting record does not carry the cancel-current gate bit")
	}
	health := u.Health

	q.PurgeUnprotected() // a non-queued issue purges the record and notifies it

	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the purged record removed", q.LenPrimary())
	}
	if u.Dying {
		t.Fatal("a cancelled countdown applied its damage [04 R-ORD-01 §2]")
	}
	if u.Health != health {
		t.Fatalf("health = %d, want %d unchanged by a cancelled countdown", u.Health, health)
	}
}

// TestSelfDestructCountdownFieldFollowsTheParser locks the parse of
// [04 R-SPEC-01 §13]'s correction: there is no clamp. An absent key yields 5;
// an authored value is stored masked to three bits, so 8 reads as 0, 9 as 1,
// and 6 and 7 survive as authored.
func TestSelfDestructCountdownFieldFollowsTheParser(t *testing.T) {
	if got := selfDestructCountdownField(&content.UnitDef{}); got != 5 {
		t.Fatalf("absent key yielded %d, want the default 5 [02 \"Unit record\"]", got)
	}
	for _, row := range []struct {
		authored string
		want     uint32
	}{{"0", 0}, {"1", 1}, {"5", 5}, {"6", 6}, {"7", 7}, {"8", 0}, {"9", 1}, {"9tail", 1}, {"18446744073709551617", 1}, {"-18446744073709551617tail", 7}} {
		def := &content.UnitDef{SelfDestructCountdown: row.authored, SelfDestructCountdownPresent: true}
		if got := selfDestructCountdownField(def); got != row.want {
			t.Fatalf("selfdestructcountdown=%s yielded %d, want %d [04 R-SPEC-01 §13]", row.authored, got, row.want)
		}
	}
}

// TestSelfDestructRearRecordRunsOnTheSecondarySegment locks the descriptor
// split: `SelfDestruct` carries the rear-segment selection bit and therefore
// runs on its own deadline with an empty satisfied set [R-ORDER-02 §1], while
// `SelfDestructFG` sits on the front segment. Both share one handler body.
func TestSelfDestructRearRecordRunsOnTheSecondarySegment(t *testing.T) {
	rear := Lookup("SelfDestruct")
	front := Lookup("SelfDestructFG")
	if !isSecondary(rear) {
		t.Fatal("SelfDestruct must select the rear segment [04 §3.1]")
	}
	if isSecondary(front) {
		t.Fatal("SelfDestructFG must select the front segment [04 §3.1]")
	}
	if DescriptorFor(rear).Handler == nil || DescriptorFor(front).Handler == nil {
		t.Fatal("both self-destruct descriptors must carry the shared handler")
	}

	q, u := selfDestructFixture(t, nil)
	q.PushSecondary(rear, Node{Owner: u.Handle, Param1: 1}) // p1 = 1: fire on sight
	q.Pump(u, 0)
	if q.LenSecondary() != 0 {
		t.Fatalf("secondary length = %d, want the finished record freed", q.LenSecondary())
	}
	if !u.Dying {
		t.Fatal("the rear-segment record did not apply its damage")
	}
	if diags := q.Diagnostics(); len(diags) != 0 {
		t.Fatalf("secondary dispatch recorded %v, want none", diags)
	}
}

// TestSelfDestructLatchesTheRecordedAttackerAsItself locks the third field of
// the death latch [04 R-UNIT-06 §5]: the death handler always writes the death
// packet's attacker into the recorded-attacker link, and cause 3 applies its
// 30000 "to the unit itself" [04 R-SPEC-01 §1], so a self-destructed unit ends
// pointing at its own handle — not at whoever shot it before the order ran.
//
// The handler cannot reach the world's destroy entry, so it shares that entry's
// field writer. This test is what catches the two drifting apart again.
func TestSelfDestructLatchesTheRecordedAttackerAsItself(t *testing.T) {
	def := &content.UnitDef{SelfDestructCountdown: "0", SelfDestructCountdownPresent: true}
	q, u := selfDestructFixture(t, def)
	u.EngagementTarget = 77 // an earlier attacker, which the death row must overwrite
	q.Push(Lookup("SelfDestructFG"), Node{Owner: u.Handle})
	q.Pump(u, 0)
	if !u.Dying || u.DeathCause != units.DeathSelfDestruct {
		t.Fatalf("self destruct did not latch cause 3: dying=%t cause=%v", u.Dying, u.DeathCause)
	}
	if u.EngagementTarget != u.Handle {
		t.Fatalf("recorded attacker = %d, want the unit's own handle %d [04 R-UNIT-06 §5]", u.EngagementTarget, u.Handle)
	}
}

// selfDestructFixture binds the actual packet intake to the order's unit pool.
func selfDestructFixture(t *testing.T, def *content.UnitDef) (*Queue, *units.Unit) {
	t.Helper()
	q, original := standingFixture(def)
	w := newOrdersFixtureWorld(4, nil)
	createDef := &content.UnitDef{UnitName: "selfdestruct", MaxDamage: 3000, Limit: -1}
	h, err := w.Create(createDef, 0, original.X, original.Y, original.Z)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.Def = original.Def
	u.Health, u.MaxHealth = original.Health, original.MaxHealth
	service := &combat.Service{ControlByte: func(uint8) uint8 { return combat.ControlByteHuman }}
	q.binding.Damage = func(tick uint32, input combat.DamageInput) combat.DamageResult {
		return service.AcceptDamage(w, tick, input)
	}
	BindQueue(u, q)
	return q, u
}
