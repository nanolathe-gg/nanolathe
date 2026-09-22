package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
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

// The keep-survivors purge reloads the live chain after each cleanup. A record
// appended by a cancel callback is therefore visited by that same purge rather
// than escaping ahead of the producer's replacement [04 R-MOV-03 §6].
func TestPurgeVisitsRecordAppendedByCancelCleanup(t *testing.T) {
	u := newTestUnit()
	q := &Queue{}
	notices := 0
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
			q.appendTail(Lookup("Move_Ground"), Node{Owner: u.Handle})
			return true
		}},
	}
	q.Push(Lookup("MobileBuild"), Node{Owner: u.Handle, DynamicGate: 2})

	q.PurgeUnprotected()
	if notices != 1 {
		t.Fatalf("cancel notices = %d, want one", notices)
	}
	if got := len(q.Primary()); got != 0 {
		t.Fatalf("purge retained %d callback-appended records, want none", got)
	}
}

// Established: the full purge frees both segments in order, with removal
// callbacks on every record [04 §3.3][04 R-MOV-03 §6][04 R-ORDER-02 §2].
// Construction's cancel notice can unlink its own record during that walk.
func TestCancelAllCleansEveryRecordAfterReentrantRemoval(t *testing.T) {
	for _, throughPump := range []bool{false, true} {
		name := "direct"
		if throughPump {
			name = "primary-result"
		}
		t.Run(name, func(t *testing.T) {
			q, u, notices := reentrantCancelQueue(t)
			bound, _ := cbUnit(cbProgram("StopBuilding"))
			u.ScriptState = bound.ScriptState
			q.Push(Lookup("MobileBuild"), Node{Owner: u.Handle, DynamicGate: 2, Param2: 1})
			build := q.Head()
			q.Push(Lookup("Move_Ground"), Node{Owner: u.Handle, Param1: 11})
			middle := q.Primary()[1]
			q.Push(Lookup("Move_Ground"), Node{Owner: u.Handle, Param1: 12})
			tail := q.Primary()[2]
			q.Push(Lookup("BuildWeapon"), Node{Owner: u.Handle, Param1: 13})
			rear := q.Secondary()[0]
			for _, n := range []*Node{middle, tail, rear} {
				n.Flags |= FlagStopBuildingPending
			}
			var released []*Node
			q.binding.Movement = &MovementGoalAdapter{Release: func(n *Node) bool {
				if n != build {
					released = append(released, n)
				}
				return true
			}}
			stops := 0
			callbackBridgeFor(u).SetLifecycleSink(func(e cob.LifecycleEvent) {
				if e.Name == "StopBuilding" && e.Phase == "start" {
					stops++
				}
			})
			want := []*Node{middle, tail, rear}
			if throughPump {
				// A ground move rejected while carried returns cancel-all through
				// its real descriptor handler [04 R-ORD-01 §4].
				u.Attachment.Carrier = 2
				trigger := q.PushHead(Lookup("Move_Ground"), Node{Owner: u.Handle})
				want = append([]*Node{trigger}, want...)
				q.Pump(u, 100)
			} else {
				q.CancelAll()
			}
			if *notices != 1 {
				t.Errorf("cancel notices = %d, want one", *notices)
			}
			if stops != 3 {
				t.Errorf("StopBuilding starts = %d, want one per pending record", stops)
			}
			if len(released) != len(want) {
				t.Fatalf("released %d records, want %d", len(released), len(want))
			}
			for i, n := range want {
				if released[i] != n {
					t.Errorf("release %d = %p, want %p", i, released[i], n)
				}
			}
			if len(q.Primary()) != 0 || len(q.Secondary()) != 0 {
				t.Fatal("cancel-all left queued records")
			}
		})
	}
}

func TestCancelAllDoesNotRecleanARecordRemovedByAnEarlierNotice(t *testing.T) {
	q, u, _ := reentrantCancelQueue(t)
	q.Push(Lookup("MobileBuild"), Node{Owner: u.Handle, DynamicGate: 2, Param2: 1})
	q.Push(Lookup("Move_Ground"), Node{Owner: u.Handle, Param1: 7})
	removed := q.Primary()[1]
	q.Push(Lookup("Move_Ground"), Node{Owner: u.Handle, Param1: 8})
	tail := q.Primary()[2]
	var released []*Node
	q.binding.Movement = &MovementGoalAdapter{Release: func(n *Node) bool {
		if n == removed || n == tail {
			released = append(released, n)
		}
		return true
	}}
	q.binding.Work.CancelNotice = func(_ *units.Unit, n *Node, _ uint32) bool {
		n.DynamicGate &^= 2
		q.RemovePrimaryNode(removed, false)
		q.RemovePrimaryNode(n, false)
		return true
	}
	q.CancelAll()
	if len(released) != 2 || released[0] != removed || released[1] != tail {
		t.Fatalf("release order = %v, want removed sibling then tail, once each", released)
	}
}
