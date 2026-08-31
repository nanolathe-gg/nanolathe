package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// CancelFrontMost removes the first match walking from the head, where
// CancelTailMost removes the last [07 R-P0-11 §6]. The direction is the whole
// point: retail's queued-order duplicate test returns on the first match from
// the front, so the oldest queued order at a point is the one that goes.
func TestCancelFrontMostRemovesTheOldestMatch(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Skip("Move_Ground descriptor unavailable")
	}
	build := func() *Queue {
		q := &Queue{}
		for _, g := range []int32{10, 20, 30} {
			q.Push(id, Node{GoalX: numeric.Fixed(g) << 16, Param1: uint32(g)})
		}
		return q
	}
	q := build()
	if !q.CancelFrontMost(func(n Node) bool { return n.GoalX != numeric.Fixed(20)<<16 }) {
		t.Fatal("CancelFrontMost removed nothing")
	}
	if got := q.Primary()[0].Param1; got != 20 {
		t.Fatalf("front-most removal left %d at the head, want the 10 node gone", got)
	}
	if len(q.Primary()) != 2 {
		t.Fatalf("front-most removal left %d nodes, want 2", len(q.Primary()))
	}

	// The tail-most helper on the same queue and predicate takes the other end,
	// which is what makes these two distinct operations rather than aliases.
	q2 := build()
	if !q2.CancelTailMost(func(n Node) bool { return n.GoalX != numeric.Fixed(20)<<16 }) {
		t.Fatal("CancelTailMost removed nothing")
	}
	if got := q2.Primary()[len(q2.Primary())-1].Param1; got != 20 {
		t.Fatalf("tail-most removal left %d at the tail, want the 30 node gone", got)
	}
}

// The duplicate removal frees the whole node whatever its count field says.
// The count decrement belongs to the factory producer's negative-count path
// [R-P0-11 §1], not here, so a coalesced node goes in one click.
func TestCancelFrontMostIgnoresTheCountField(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Skip("Move_Ground descriptor unavailable")
	}
	q := &Queue{}
	q.Push(id, Node{GoalX: numeric.Fixed(10) << 16, Param2: 5})
	if !q.CancelFrontMost(func(n Node) bool { return n.GoalX == numeric.Fixed(10)<<16 }) {
		t.Fatal("CancelFrontMost removed nothing")
	}
	if len(q.Primary()) != 0 {
		t.Fatalf("a count-5 node survived as %d nodes; the duplicate removal does not decrement", len(q.Primary()))
	}
}

func TestCancelFrontMostNoMatch(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Skip("Move_Ground descriptor unavailable")
	}
	q := &Queue{}
	q.Push(id, Node{GoalX: numeric.Fixed(10) << 16})
	if q.CancelFrontMost(func(n Node) bool { return false }) {
		t.Fatal("CancelFrontMost reported a removal with no match")
	}
	if len(q.Primary()) != 1 {
		t.Fatalf("a non-matching scan changed the queue to %d nodes", len(q.Primary()))
	}
}
