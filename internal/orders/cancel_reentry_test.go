package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// reentrantCancelQueue builds a queue whose removal cleanup removes the record
// itself, which is what the three construction rows do for real: the cleanup
// sends [R-ORDER-02 §2]'s cancel notification while the record's dynamic gate
// still holds bit 1 (value 2), the notification is the production machine's
// cancel-current, and cancel-current's epilogue removes the record. The fixture
// keeps the shape and drops construction's machinery — the row is the
// handler-less `MobileBuild` and the binding's CancelNotice stands in for
// DeliverCancelNotice, releasing the gate before the removal so the re-entry
// terminates.
func reentrantCancelQueue(t *testing.T) (*Queue, *units.Unit, *int) {
	t.Helper()
	u := newTestUnit()
	notices := 0
	q := &Queue{}
	q.binding = &QueueBinding{
		Lookup: func(h pool.Handle) *units.Unit {
			if h == u.Handle {
				return u
			}
			return nil
		},
		Work: &WorkAdapter{CancelNotice: func(_ *units.Unit, n *Node, _ uint32) bool {
			notices++
			n.DynamicGate &^= 2
			q.RemovePrimaryNode(n, false)
			return true
		}},
	}
	return q, u, &notices
}

// TestCancelTailMostSurvivesACancelNoticeThatRemovesTheRecord reproduces the
// crash a skirmish hit on the build page's cancel button:
// `panic: runtime error: slice bounds out of range [1:0]` inside
// CancelTailMost, reached through construction's CancelProductCount.
//
// The mechanism is the double removal of one record that
// TestRemoveHeadSurvivesACancelNoticeThatRemovesTheHead documents for the head
// removal: the counted subtraction ran the cleanup, the cleanup's cancel
// notification unlinked the record itself, and the positional splice that
// followed then indexed a segment the notification had already emptied. With a
// record queued behind the cancelled one, the same double removal silently
// dropped that record instead of crashing.
func TestCancelTailMostSurvivesACancelNoticeThatRemovesTheRecord(t *testing.T) {
	buildID := Lookup("MobileBuild")
	moveID := Lookup("Move_Ground")
	if buildID == 0 || moveID == 0 {
		t.Skip("descriptor lookup failed")
	}

	// The degenerate case the crash actually hit: one record, and the segment
	// is empty by the time the outer splice runs.
	q, u, notices := reentrantCancelQueue(t)
	q.Push(buildID, Node{Owner: u.Handle, DynamicGate: 0xa, Phase: 3, Param2: 1})
	if !q.CancelTailMost(func(n Node) bool { return n.ID == buildID }) {
		t.Fatal("CancelTailMost reported no match for the record it was given")
	}
	if *notices != 1 {
		t.Fatalf("cancel notices %d, want exactly one [R-ORDER-02 §2]", *notices)
	}
	if len(q.Primary()) != 0 {
		t.Fatalf("queue holds %d records after cancelling its only one", len(q.Primary()))
	}

	// And the silent case: a record ahead of the cancelled one must survive.
	q2, u2, _ := reentrantCancelQueue(t)
	q2.Push(moveID, Node{Owner: u2.Handle, Param1: 7})
	q2.Push(buildID, Node{Owner: u2.Handle, DynamicGate: 0xa, Phase: 3, Param2: 1})
	if len(q2.Primary()) != 2 {
		t.Fatalf("fixture queue %d records, want 2", len(q2.Primary()))
	}
	ahead := q2.Primary()[0]
	if !q2.CancelTailMost(func(n Node) bool { return n.ID == buildID }) {
		t.Fatal("CancelTailMost reported no match")
	}
	if len(q2.Primary()) != 1 || q2.Primary()[0] != ahead {
		t.Fatalf("queue after the double removal has %d records, want only the record ahead of the cancelled one", len(q2.Primary()))
	}

	// A record whose count exceeds one is decremented, not removed, so no
	// cleanup and no notification run at all [05 "Queue subtraction"].
	q3, u3, n3 := reentrantCancelQueue(t)
	q3.Push(buildID, Node{Owner: u3.Handle, DynamicGate: 0xa, Phase: 3, Param2: 3})
	if !q3.CancelTailMost(func(n Node) bool { return n.ID == buildID }) {
		t.Fatal("CancelTailMost reported no match on the counted record")
	}
	if *n3 != 0 {
		t.Fatalf("a count decrement sent %d cancel notices, want none", *n3)
	}
	if len(q3.Primary()) != 1 || q3.Primary()[0].Param2 != 2 {
		t.Fatalf("counted subtraction left %d records with count %d, want one record at 2", len(q3.Primary()), q3.Primary()[0].Param2)
	}
}

// The front-most duplicate removal and the Replace purge splice the same
// segment after the same cleanup, so they take the same double removal.
func TestCancelFrontMostAndPurgeSurviveAReentrantCleanup(t *testing.T) {
	buildID := Lookup("MobileBuild")
	moveID := Lookup("Move_Ground")
	if buildID == 0 || moveID == 0 {
		t.Skip("descriptor lookup failed")
	}

	q, u, notices := reentrantCancelQueue(t)
	q.Push(buildID, Node{Owner: u.Handle, DynamicGate: 0xa, Phase: 3, Param2: 1})
	q.Push(moveID, Node{Owner: u.Handle, Param1: 9})
	behind := q.Primary()[1]
	if !q.CancelFrontMost(func(n Node) bool { return n.ID == buildID }) {
		t.Fatal("CancelFrontMost reported no match")
	}
	if *notices != 1 {
		t.Fatalf("cancel notices %d, want exactly one", *notices)
	}
	if len(q.Primary()) != 1 || q.Primary()[0] != behind {
		t.Fatalf("front-most removal left %d records, want only the record behind the cancelled head", len(q.Primary()))
	}

	// The purge frees every record lacking the purge-survivor bit; the survivor
	// must still be there afterwards, and the head test must not have been
	// answered by a slot a survivor was compacted into.
	q2, u2, n2 := reentrantCancelQueue(t)
	q2.Push(buildID, Node{Owner: u2.Handle, DynamicGate: 0xa, Phase: 3, Param2: 1})
	q2.Push(moveID, Node{Owner: u2.Handle, Param1: 4})
	q2.Primary()[1].Flags |= FlagPurgeSurvivor
	survivor := q2.Primary()[1]
	q2.PurgeUnprotected()
	if *n2 != 1 {
		t.Fatalf("purge sent %d cancel notices, want exactly one", *n2)
	}
	if len(q2.Primary()) != 1 || q2.Primary()[0] != survivor {
		t.Fatalf("purge left %d records, want only the purge survivor", len(q2.Primary()))
	}
}
