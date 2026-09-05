package orders

import "testing"

// The active marker (bit 12 of the record's static-mask copy) has exactly ONE
// writer: the producer insertion's after-marker branch, which sets it on the
// record it creates and clears it on the record that held it
// [04 R-ORD-01 §13]. These two cases lock the paths that used to write it
// anyway, through the head fallback of the deleted ensureSingleActive.
//
// Both matter for ordering, not for tidiness: [04 §3.1]'s insertion rule reads
// "with no marked record it appends at the tail", so a marker invented on
// primary[0] puts the NEXT Shift-queued order at index 1 — at the front of the
// remaining queue.

// The tail rotate (PRIMARY result code 6) moves a link and nothing else. Here
// the rotated record is the one carrying the marker, which is the case the old
// arm got backwards: it cleared the marker it was moving and gave it to the
// new head.
func TestTailRotateKeepsTheMarkerOnTheRotatedRecord(t *testing.T) {
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("descriptor lookup failed")
	}
	sim := injectTestSim(t)
	q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
	p := newProbe()
	p.install(t, moveID, func(n *Node) Code {
		if n.Param1 == 1 {
			return 6 // [04 §3.3] move the record to the tail of its segment
		}
		return 2
	})
	q.Push(moveID, Node{Param1: 1})
	q.Push(moveID, Node{Param1: 2})
	clearGates(q)
	// Put the marker on the record that is about to rotate.
	q.primary[0].Flags |= FlagActive
	q.primary[1].Flags &^= FlagActive
	rotated := q.primary[0]

	q.Pump(u, probeTick)

	if len(q.primary) != 2 || q.primary[1] != rotated {
		t.Fatalf("code6 did not rotate the head to the tail: len %d", len(q.primary))
	}
	if rotated.Flags&FlagActive == 0 {
		t.Fatal("code6 cleared the active marker on the record it rotated [04 §3.3][04 R-ORD-01 §13]")
	}
	if q.primary[0].Flags&FlagActive != 0 {
		t.Fatal("code6 handed the active marker to the new head; the rotate has no marker write [04 R-ORD-01 §13]")
	}
}

// The Replace purge removes the records lacking static bit 2 and does nothing
// else [04 R-MOV-03 §6]. When the record it frees was the marked one the
// segment is left unmarked, and the Replace's own insertion then appends at
// the tail — behind the survivors, not in front of them. The leading-auto drop
// is the same shape ([04 R-ORD-01 §13]: the marker is written by the branch
// that runs AFTER the purge and the drop, on the record being inserted).
//
// The two survivors below are the pair [04 R-ORD-01 §15] names for a factory
// product — `GetBuilt` then `BeCarried`, "with neither record carrying the
// active marker" — which is where this mattered in play: the next non-queued
// order the unit takes belongs behind both.
func TestPurgeAndAutoDropWriteNoActiveMarker(t *testing.T) {
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("descriptor lookup failed")
	}
	q := &Queue{}
	q.primary = []*Node{
		{ID: moveID, Param1: 1, Flags: FlagPurgeSurvivor},
		{ID: moveID, Param1: 2, Flags: FlagPurgeSurvivor},
		{ID: moveID, Param1: 3, Flags: FlagActive}, // the marked record, not a survivor
	}
	q.PurgeUnprotected()
	if len(q.primary) != 2 {
		t.Fatalf("purge kept %d records, want the 2 survivors", len(q.primary))
	}
	for _, n := range q.primary {
		if n.Flags&FlagActive != 0 {
			t.Fatalf("the purge marked surviving record %d; the marker has one writer [04 R-ORD-01 §13]", n.Param1)
		}
	}
	// The insertion that follows a Replace therefore appends at the TAIL.
	q.Push(moveID, Node{Param1: 4})
	if len(q.primary) != 3 || q.primary[2].Param1 != 4 {
		t.Fatalf("insertion after the purge landed at index %d, want the tail [04 §3.1]", len(q.primary)-1)
	}

	// The leading-auto drop, same rule: dropping the marked leading auto record
	// leaves the segment unmarked.
	q2 := &Queue{}
	q2.primary = []*Node{
		{ID: moveID, Param1: 1, Flags: FlagAutoOp | FlagActive},
		{ID: moveID, Param1: 2},
	}
	q2.DropLeadingAutoOps()
	if len(q2.primary) != 1 || q2.primary[0].Param1 != 2 {
		t.Fatalf("drop left %d records", len(q2.primary))
	}
	if q2.primary[0].Flags&FlagActive != 0 {
		t.Fatal("the leading-auto drop marked the new head; the drop has no marker write [04 R-ORD-01 §13]")
	}
}

// The shared tail append of [04 R-ORD-02 §4] — the patrol-chain setup's
// return-to-start waypoint and `VTOL_Follow`'s seek-guard hand-off — inherits
// no flag and writes no marker, on an empty segment as much as on a full one.
// It used to hand the marker to the record it created when the primary segment
// had been empty, which made it a second writer of bit 12 and put the next
// Shift-queued order in front of the waypoint it had just planted.
func TestTailAppendWritesNoActiveMarker(t *testing.T) {
	patrolID := Lookup("Patrol")
	moveID := Lookup("Move_Ground")
	if patrolID == 0 || moveID == 0 {
		t.Fatal("descriptor lookup failed")
	}
	q := &Queue{}

	// Empty segment: the waypoint lands unmarked, so the segment stays unmarked.
	first := q.appendTail(patrolID, Node{Param1: 1})
	if first == nil || len(q.primary) != 1 {
		t.Fatalf("append left %d records", len(q.primary))
	}
	if first.Flags&FlagActive != 0 {
		t.Fatal("the tail append marked the record it created on an empty segment; the marker has one writer [04 R-ORD-01 §13][04 R-ORD-02 §4]")
	}

	// And the producer insertion that follows therefore appends at the TAIL,
	// behind the waypoint, rather than displacing it [04 §3.1].
	q.Push(moveID, Node{Param1: 2})
	if len(q.primary) != 2 || q.primary[1].Param1 != 2 {
		t.Fatalf("insertion after the tail append landed at index %d, want the tail [04 §3.1]", len(q.primary)-1)
	}

	// A marked segment keeps its marker where it was.
	marked := q.primary[1]
	if marked.Flags&FlagActive == 0 {
		t.Fatal("the producer insertion did not take the marker [04 R-ORD-01 §13]")
	}
	q.appendTail(patrolID, Node{Param1: 3})
	if q.primary[2].Flags&FlagActive != 0 || marked.Flags&FlagActive == 0 {
		t.Fatal("the tail append moved the active marker [04 R-ORD-02 §4]")
	}
}
