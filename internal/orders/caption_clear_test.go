package orders

import "testing"

// TestCaptionClearIsOneShotAcrossReArms locks [04 R-ORD-01 §1]'s caption clear:
// "A record whose static-mask copy carries the runtime caption-pending bit has
// it cleared by a one-shot helper that also emits status kind 5 (`ok`)".
//
// The one-shot half was missing, and it is audible. `Move_Ground` phase 0 is
// "caption clear; point goal; gate `0xE0`" and phase 1 re-arms with code 9
// whenever the follower reports no route instead of arrival — the pump then
// resets the phase and re-dispatches phase 0 after 30..59 ticks, for the life
// of the record. [04 R-PATH-01 §14] item 4 establishes that this cycle is the
// legitimate steady state of a move whose goal cell is held, and that it is
// "silent and unbounded ... no motion, no engine cue". With an unconditional
// emit, every re-arm raised the `ok` acknowledgement voice instead, so a
// settled group of movers repeated it once or twice a second forever.
func TestCaptionClearIsOneShotAcrossReArms(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("Move_Ground descriptor unavailable")
	}
	q, u, spy := arrivedFixture(nil)
	q.Push(id, Node{Owner: u.Handle, GoalX: u.X, GoalZ: u.Z})

	// Phase 0: the record is fresh, so the caption clear speaks once.
	q.Pump(u, 40)
	if got := spy.count(statusOK); got != 1 {
		t.Fatalf("kind 5 raised %d times on the first visit, want exactly 1 [04 R-ORD-01 §1]", got)
	}
	n := q.Primary()[0]
	if n.CaptionPending {
		t.Fatal("the caption-pending flag survived the caption clear; the helper is not one-shot [04 §3.2]")
	}

	// Four re-arm cycles: the no-route bit drives phase 1 to code 9, the pump
	// resets the phase and arms a 30..59-tick deadline, and the next visit
	// after the deadline re-enters phase 0 [04 §3.3][04 R-PATH-01 §14].
	tick := uint32(41)
	for cycle := 0; cycle < 4; cycle++ {
		n = q.Primary()[0]
		n.Satisfied |= gateNoRoute
		q.pumpPrimary(u, tick)
		tick++
		n = q.Primary()[0]
		if n.Phase != 0 {
			t.Fatalf("cycle %d: phase = %d after the re-arm, want the pump's phase reset [04 §3.3] code 9", cycle, n.Phase)
		}
		// Jump past the randomized deadline so the next walk dispatches phase 0.
		if n.Deadline > 0 {
			tick = uint32(n.Deadline) + 1
		}
		q.pumpPrimary(u, tick)
		tick++
	}
	if got := spy.count(statusOK); got != 1 {
		t.Fatalf("kind 5 raised %d times across four re-arms, want exactly 1 — the steady state is silent [04 R-PATH-01 §14][04 R-ORD-01 §1]", got)
	}
}

// TestFreshRecordAcknowledgesAgain is the other half: the one-shot flag belongs
// to the RECORD, so a newly issued order acknowledges even though the previous
// one already had [04 §3.2].
func TestFreshRecordAcknowledgesAgain(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("Move_Ground descriptor unavailable")
	}
	q, u, spy := arrivedFixture(nil)
	q.Push(id, Node{Owner: u.Handle, GoalX: u.X, GoalZ: u.Z})
	q.Pump(u, 40)
	q.PurgeUnprotected()
	q.Push(id, Node{Owner: u.Handle, GoalX: u.X, GoalZ: u.Z})
	q.Pump(u, 80)
	if got := spy.count(statusOK); got != 2 {
		t.Fatalf("kind 5 raised %d times for two issued orders, want 2 [04 R-ORD-01 §1]", got)
	}
}
