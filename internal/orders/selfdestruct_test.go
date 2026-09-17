package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
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

// selfDestructVisit is one counting visit of a countdown run: the tick the
// handler ran on, the remaining count it saw, and the status-cue kinds that
// visit published.
type selfDestructVisit struct {
	tick  uint32
	count uint32
	cues  []uint8
}

// runSelfDestructCountdown drives one whole countdown for an authored
// `selfdestructcountdown`, waking the record exactly on each deadline it arms,
// and returns the counting visits plus the final wait the count-0 step drew.
// The queue carries no damage callback, so the terminal visit is observable
// without the unit dying.
func runSelfDestructCountdown(t *testing.T, authored string) ([]selfDestructVisit, uint32) {
	t.Helper()
	rng.SeedGlobal(1, 0) // same stream position for every run, so draws compare
	def := &content.UnitDef{SelfDestructCountdown: authored, SelfDestructCountdownPresent: true}
	u := &units.Unit{Handle: 1, Alive: true, Def: def, Health: 3000, MaxHealth: 3000}
	var cues []uint8
	q := &Queue{binding: &QueueBinding{
		SimRNG: rng.Global.Sim,
		Presentation: &PresentationAdapter{
			Ready:  func() bool { return true },
			Status: func(_ *units.Unit, kind uint8, _ string) bool { cues = append(cues, kind); return true },
		},
	}}
	BindQueue(u, q)
	q.Push(Lookup("SelfDestructFG"), Node{Owner: u.Handle})

	var visits []selfDestructVisit
	var finalWait uint32
	tick := uint32(0)
	for step := 0; q.LenPrimary() > 0 && step < 24; step++ {
		n := q.Primary()[0]
		count := selfDestructCountdownField(def)
		if n.Param2&selfDestructMarkerMask != 0 {
			count = n.Param2 & selfDestructCountField
		}
		counting := n.Param1 == 0
		before := len(cues)
		q.Pump(u, tick)
		if !counting {
			break // the damage visit; it completes and frees the record
		}
		visits = append(visits, selfDestructVisit{tick: tick, count: count, cues: append([]uint8(nil), cues[before:]...)})
		if q.LenPrimary() == 0 {
			break
		}
		next := uint32(q.Primary()[0].Deadline)
		if count == 0 {
			finalWait = next - tick
		}
		tick = next
	}
	return visits, finalWait
}

// TestSelfDestructCountdownAnnouncesOnlyTheSixTabulatedCounts locks the
// handling of the one part of this row retail leaves undefined.
//
// Retail's announce is a six-entry table built in the handler's own stack
// frame, holding the status kinds 22, 21, 20, 19, 18 and 17 for the remaining
// counts 0 through 5, and the read is not bounds-checked above the table
// [04 R-ORD-01 §14]. Counts 6 and 7 are reachable — the authored
// `selfdestructcountdown` is masked to three bits with no clamp — and retail
// then passes a word from outside the table to the cue emitter as a kind, which
// is undefined behaviour rather than an announcement of any defined kind
// [04 R-SPEC-01 §13]. This port announces nothing for those two counts, which
// is also what retail's local-owner-gated emitter already does for every unit
// the local player does not own.
//
// The contract this locks is narrow and easy to regress in either direction:
// counts 0..5 keep their exact kinds and order, counts 6 and 7 publish no cue
// at all, and nothing else about the countdown — the number of visits, the
// 30-tick spacing, the remaining-count sequence, or the single draw the count-0
// step makes — varies with the authored value.
func TestSelfDestructCountdownAnnouncesOnlyTheSixTabulatedCounts(t *testing.T) {
	tabulated := []uint8{17, 18, 19, 20, 21, 22} // counts 5,4,3,2,1,0

	var waits []uint32
	for _, row := range []struct {
		authored string
		start    uint32
		want     [][]uint8 // cues per counting visit, in visit order
	}{
		{authored: "5", start: 5, want: [][]uint8{{17}, {18}, {19}, {20}, {21}, {22}}},
		{authored: "6", start: 6, want: [][]uint8{nil, {17}, {18}, {19}, {20}, {21}, {22}}},
		{authored: "7", start: 7, want: [][]uint8{nil, nil, {17}, {18}, {19}, {20}, {21}, {22}}},
	} {
		visits, finalWait := runSelfDestructCountdown(t, row.authored)
		waits = append(waits, finalWait)

		if len(visits) != int(row.start)+1 {
			t.Fatalf("countdown %s ran %d counting visits, want %d: one per remaining count %d..0 [04 R-SPEC-01 §13]", row.authored, len(visits), row.start+1, row.start)
		}
		for i, visit := range visits {
			wantCount := row.start - uint32(i)
			if visit.count != wantCount {
				t.Fatalf("countdown %s visit %d saw remaining count %d, want %d", row.authored, i, visit.count, wantCount)
			}
			if wantCount > 0 && visit.tick != uint32(i)*selfDestructStep {
				t.Fatalf("countdown %s visit %d ran on tick %d, want %d: every step but the last waits one 30-tick step [04 R-ORD-01 §2]", row.authored, i, visit.tick, uint32(i)*selfDestructStep)
			}
			if len(visit.cues) != len(row.want[i]) {
				t.Fatalf("countdown %s remaining count %d published cues %v, want %v [04 R-ORD-01 §14][04 R-SPEC-01 §13]", row.authored, wantCount, visit.cues, row.want[i])
			}
			for k, kind := range visit.cues {
				if kind != row.want[i][k] {
					t.Fatalf("countdown %s remaining count %d published cue kind %d, want %d [04 R-ORD-01 §14]", row.authored, wantCount, kind, row.want[i][k])
				}
			}
			// The tabulated kinds are exactly 22 − count, and only there.
			if wantCount < 6 && (len(visit.cues) != 1 || visit.cues[0] != tabulated[5-wantCount]) {
				t.Fatalf("countdown %s remaining count %d must announce kind %d [04 R-ORD-01 §14]", row.authored, wantCount, 22-wantCount)
			}
			if wantCount >= 6 && len(visit.cues) != 0 {
				t.Fatalf("countdown %s remaining count %d announced %v; no cue kind is defined past the six-entry table [04 R-SPEC-01 §13]", row.authored, wantCount, visit.cues)
			}
		}
	}

	// One draw, at the count-0 step, in every run. Identical seeds give an
	// identical wait only if counts 6 and 7 consumed no extra randomness and
	// took no extra step, so this is the RNG half of "the countdown itself is
	// unchanged" [04 R-SPEC-01 §13].
	for i, wait := range waits {
		if wait == 0 || wait >= 15 {
			t.Fatalf("run %d drew a final wait of %d, want the RNG(15) arm of the count-0 step [04 R-SPEC-01 §13]", i, wait)
		}
		if wait != waits[0] {
			t.Fatalf("run %d drew a final wait of %d, want %d: a 6 or 7 countdown must not perturb the simulation stream", i, wait, waits[0])
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
