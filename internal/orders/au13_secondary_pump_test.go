package orders

// AU-13 — the two segment pumps are independent per-unit calls.
//
// [04 §3.3] step 3's stop ends the FRONT-segment walk only; the per-unit tick
// then pumps the rear segment regardless of the front head's gate
// ([04 R-ORD-01 §10], "the caller pumps the two segments back to back"). A gate
// test used to stand between the two calls in Pump, so a unit whose front head
// was gated and unsatisfied never dispatched its rear-segment records — and
// never spent the secondary code-3 arm's RNG(15) draw, which moves the
// simulation stream's position. §3.3 step 3's clause "rear-segment records sit
// behind any front blocker" has been struck with it.

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/units"
)

// blockedFrontHead pushes a primary record whose gate is armed with a bit
// nothing satisfies and whose deadline never arrives, so step 3 stops the
// front walk on its first record.
func blockedFrontHead(t *testing.T, q *Queue, id ID) {
	t.Helper()
	q.Push(id, Node{})
	head := q.primary[0]
	head.DynamicGate = 0x400 // nonzero gate [04 §3.3] step 2
	head.Satisfied = 0       // nothing in it satisfied [04 §3.3] step 3
	head.Deadline = -1       // no deadline to arrive [04 §3.3] step 1
}

func TestSecondaryPumpRunsBehindBlockedFrontHead(t *testing.T) {
	moveID, buildID := Lookup("Move_Ground"), Lookup("BuildWeapon")
	if moveID == 0 || buildID == 0 {
		t.Fatalf("descriptor lookup failed")
	}

	t.Run("rear record dispatches", func(t *testing.T) {
		sim := injectTestSim(t)
		q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()

		frontRan := false
		restoreFront := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
			frontRan = true
			return 2
		})
		defer restoreFront()
		rearCalls := 0
		restoreRear := setHandler(buildID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
			rearCalls++
			return 5 // unlink, free, continue [04 §3.3] secondary column
		})
		defer restoreRear()

		blockedFrontHead(t, q, moveID)
		q.PushSecondary(buildID, Node{})
		q.secondary[0].DynamicGate = 0 // ready by construction [04 R-ORD-01 §1]
		q.secondary[0].Deadline = -1

		q.Pump(u, probeTick)

		if frontRan {
			t.Fatal("front handler ran: the blocked head must stop the front walk [04 §3.3] step 3")
		}
		if len(q.primary) != 1 {
			t.Fatalf("primary len %d, want the blocked record still linked", len(q.primary))
		}
		if rearCalls != 1 {
			t.Fatalf("rear handler calls %d, want 1: the secondary pump runs unconditionally after the primary [04 R-ORD-01 §10]", rearCalls)
		}
		if len(q.secondary) != 0 {
			t.Fatalf("secondary len %d, want 0 after code 5", len(q.secondary))
		}
	})

	// The stream consequence, which is what makes the coupling a determinism
	// defect rather than a missed dispatch: the secondary code-3 arm draws
	// RNG(15) exactly as the primary's does [04 §3.3] "The two deadline draws".
	t.Run("secondary code 3 still spends its draw", func(t *testing.T) {
		sim := injectTestSim(t)
		q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()

		restoreFront := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return 2 })
		defer restoreFront()
		restoreRear := setHandler(buildID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return 3 })
		defer restoreRear()

		blockedFrontHead(t, q, moveID)
		q.PushSecondary(buildID, Node{})
		q.secondary[0].DynamicGate = 0
		q.secondary[0].Deadline = -1

		want := expectedDraw(sim, 15)
		before := sim.Draws()
		q.Pump(u, probeTick)

		if d := sim.Draws() - before; d != 1 {
			t.Fatalf("draw delta %d, want 1: the rear code-3 arm draws RNG(15) even behind a blocked front head", d)
		}
		if got := q.secondary[0].Deadline; got != int32(probeTick+30+want) {
			t.Fatalf("rear deadline %d, want %d (tick + 30 + RNG(15)) [04 §3.3]", got, int32(probeTick+30+want))
		}
		if q.secondary[0].DynamicGate != 1 {
			t.Fatalf("rear gate %#x, want the lowest bit set [04 §3.3]", q.secondary[0].DynamicGate)
		}
	})
}
