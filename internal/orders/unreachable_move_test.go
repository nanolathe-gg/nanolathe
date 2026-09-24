package orders

import (
	"reflect"
	"testing"
)

// Modern unreachable moves (DESIGN_UNITS_ORDERS_COB "Modern unreachable
// moves"): orders admits exactly the crowded-arrival records, without the
// crowded radius or stillness, and Strict admits none. The answer is pure:
// no record write and no RNG in either mode.
func TestUnreachableMoveArrivalEligibilityAndStrictBypass(t *testing.T) {
	for _, modern := range []bool{false, true} {
		q, u, n := crowdedOrderFixture(modern)
		u.Move.Speed = 1 // unlike crowded arrival, a moving unit is eligible
		n.GoalX = u.X + 1<<30
		before, random := *n, *q.Binding().SimRNG
		if got := UnreachableMoveArrival(u, n); got != modern {
			t.Fatalf("modern=%v eligible=%v", modern, got)
		}
		if *q.Binding().SimRNG != random || !reflect.DeepEqual(before, *n) {
			t.Fatalf("modern=%v: the eligibility answer wrote the record or drew RNG", modern)
		}
	}
	for _, kind := range []string{"queued behind", "attack", "target", "automatic work", "danger", "return", "paralyze", "carried", "incomplete", "air", "dying"} {
		t.Run(kind, func(t *testing.T) {
			q, u, n := crowdedOrderFixture(true)
			switch kind {
			case "queued behind":
				q.Push(Lookup("Move_Ground"), Node{})
			case "attack":
				n.ID = Lookup("Attack_Chase")
			case "target":
				n.Target = 2
			case "automatic work":
				n.automaticWork = true
			case "danger":
				q.danger.response = n
			case "return":
				q.danger.returnMove = n
			case "paralyze":
				q.PushHead(Lookup("Paralyze"), Node{})
			case "carried":
				u.Attachment.Carrier = 2
			case "incomplete":
				u.Remaining = 1
			case "air":
				u.Def.CanFly = true
			case "dying":
				u.Dying = true
			}
			if UnreachableMoveArrival(u, n) {
				t.Fatalf("%s: admitted a record that keeps its own failure handling", kind)
			}
		})
	}
}
