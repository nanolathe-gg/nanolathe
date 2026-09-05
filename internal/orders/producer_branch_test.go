package orders

import "testing"

// TestStaticHeadInsertCensus locks which descriptors select the producer
// insertion's head-insert branch [04 R-ORD-01 §13][04 §3.1]. The routing reads
// the record's static-mask copy, so a typo in one table row silently moves an
// order to the other end of the queue; this is the cheap guard against that.
//
// `SelfRepair` and `WaitForAttack` are deliberately absent. [04 R-ORD-01 §13]'s
// prose names them among the bit-5 carriers, but §3.1's byte-exact descriptor
// table gives them `0x1000204` and `0x204`, neither carrying bit 5 — the
// open-question marker at staticHeadInsert holds that conflict open.
func TestStaticHeadInsertCensus(t *testing.T) {
	wantHead := map[string]bool{
		"Activate": true, "Deactivate": true,
		"Cloak_On": true, "Cloak_Off": true,
		"Standing_MoveOrder": true, "Standing_FireOrder": true,
		"Paralyze": true, "GetBuilt": true, "BeCarried": true, "Guard_NoMove": true,
	}
	wantRear := map[string]bool{"BuildWeapon": true, "SelfDestruct": true}
	for i := range table {
		desc := table[i]
		if desc.Name == "" {
			continue
		}
		if got := desc.StaticGate&staticHeadInsert != 0; got != wantHead[desc.Name] {
			t.Errorf("%s: static bit 5 = %v, want %v [04 §3.1]", desc.Name, got, wantHead[desc.Name])
		}
		if got := desc.StaticGate&staticRearSegment != 0; got != wantRear[desc.Name] {
			t.Errorf("%s: static bit 18 = %v, want %v [04 §3.1]", desc.Name, got, wantRear[desc.Name])
		}
	}
}

// TestProducerInsertionRoutesByStaticBitsFiveAndEighteen locks the two branches
// of [04 R-ORD-01 §13]. With both bits clear the record lands immediately after
// the active marker and takes it; with either bit set it is head-inserted into
// the segment bit 18 selects and the marker is neither read nor written.
//
// The behavior this guards: a paralyzer hit or a stance toggle must reach the
// FRONT of the queue — that is what makes [04 R-ORD-01 §2]'s "later paralyzer
// hits add to p1 of the waiting head record" reachable — and a player-issued
// `BuildWeapon` must land on the rear segment rather than blocking the silo's
// front queue.
func TestProducerInsertionRoutesByStaticBitsFiveAndEighteen(t *testing.T) {
	moveID := Lookup("Move_Ground")
	guardID := Lookup("Guard_NoMove") // bit 5, front segment
	weaponID := Lookup("BuildWeapon") // bit 18, rear segment
	if moveID == 0 || guardID == 0 || weaponID == 0 {
		t.Fatal("Move_Ground / Guard_NoMove / BuildWeapon descriptors unavailable")
	}
	q, u := standingFixture(nil)

	// Two plain orders: the second lands after the marker the first took.
	q.Push(moveID, Node{Owner: u.Handle, Param1: 1})
	q.Push(moveID, Node{Owner: u.Handle, Param1: 2})
	if n := q.LenPrimary(); n != 2 || q.Primary()[1].Param1 != 2 || q.Primary()[1].Flags&FlagActive == 0 {
		t.Fatalf("after-marker branch: primary %v, want the second record behind the first and carrying the marker", q.Primary())
	}

	// A bit-5 record head-inserts, and the marker stays where it was.
	q.Push(guardID, Node{Owner: u.Handle, Param1: 3})
	prim := q.Primary()
	if len(prim) != 3 || prim[0].ID != guardID {
		t.Fatalf("primary %v, want the bit-5 record at the head [04 R-ORD-01 §13]", prim)
	}
	if prim[0].Flags&FlagActive != 0 {
		t.Fatal("the head-insert branch writes no active marker [04 R-ORD-01 §13]")
	}
	if prim[2].Flags&FlagActive == 0 {
		t.Fatal("the marker must stay on the record that held it [04 R-ORD-01 §13]")
	}
	if q.LenSecondary() != 0 {
		t.Fatal("a bit-5, bit-18-clear record belongs on the FRONT segment [04 §3.1]")
	}

	// A bit-18 record head-inserts into the REAR segment and leaves the front
	// segment's length and marker untouched.
	q.Push(weaponID, Node{Owner: u.Handle, Param1: 4})
	if q.LenPrimary() != 3 {
		t.Fatalf("primary length %d after a rear-segment issue, want 3 [04 §3.1]", q.LenPrimary())
	}
	if q.LenSecondary() != 1 || q.Secondary()[0].ID != weaponID {
		t.Fatalf("secondary %v, want the BuildWeapon record [04 §3.1]", q.Secondary())
	}
	if q.Primary()[2].Flags&FlagActive == 0 {
		t.Fatal("a rear-segment issue must not move the front segment's marker [04 R-ORD-01 §13]")
	}
}

// TestRearSegmentIssueKeepsLeadingAutoOps locks the guard on the leading-auto
// drop: retail runs that step only when the new record's static-mask copy lacks
// bit 18 [04 R-ORD-01 §13], so queueing a weapon on a silo does not drop the
// standing record the pump parked at its head.
func TestRearSegmentIssueKeepsLeadingAutoOps(t *testing.T) {
	moveID, weaponID := Lookup("Move_Ground"), Lookup("BuildWeapon")
	if moveID == 0 || weaponID == 0 {
		t.Fatal("Move_Ground / BuildWeapon descriptors unavailable")
	}
	q, u := standingFixture(nil)
	q.Push(moveID, Node{Owner: u.Handle})
	q.Primary()[0].Flags |= FlagAutoOp // as the pump's own idle refill leaves it

	q.Push(weaponID, Node{Owner: u.Handle})
	if q.LenPrimary() != 1 {
		t.Fatalf("primary %v, want the leading auto record kept across a rear-segment issue [04 R-ORD-01 §13]", q.Primary())
	}

	// A front-segment issue does drop it, which is the other half of the rule.
	q.Push(moveID, Node{Owner: u.Handle, Param1: 9})
	if q.LenPrimary() != 1 || q.Primary()[0].Param1 != 9 {
		t.Fatalf("primary %v, want the leading auto record dropped by a front-segment issue [04 §3.3]", q.Primary())
	}
}

// TestCaptionIsArmedOnlyForANonQueuedIssue locks the caption-pending writer's
// guard [04 R-ORD-01 §13]: the producer insertion arms the bit in the same
// statement that marks the record as producer-inserted, but only when the
// issue is NON-QUEUED (Replace). A Shift-queued order is inserted silent, which
// is why retail speaks one acknowledgement for a plain order and none for a
// queued one.
func TestCaptionIsArmedOnlyForANonQueuedIssue(t *testing.T) {
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("Move_Ground descriptor unavailable")
	}
	q, u := standingFixture(nil)

	q.Push(moveID, Node{Owner: u.Handle})
	if !q.Primary()[0].CaptionPending {
		t.Fatal("a non-queued issue arms the caption-pending flag [04 R-ORD-01 §13]")
	}
	q.Push(moveID, Node{Owner: u.Handle, QueuedIssue: true})
	queued := q.Primary()[1]
	if queued.CaptionPending {
		t.Fatal("a queued (Shift) issue is inserted silent [04 R-ORD-01 §13]")
	}
	// The modifier is the helper's argument, not record state [04 §3.2].
	if queued.QueuedIssue {
		t.Fatal("the stored record must not carry the queue modifier [04 §3.2]")
	}
}
