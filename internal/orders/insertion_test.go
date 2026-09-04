package orders

import "testing"

// TestHeadInsertLeavesTheActiveMarkerAlone locks [04 R-ORD-01 §13]: the handler
// head insert "writes the link, the owner and the inherited auto-op flag and
// nothing else — it never reads or writes bit 12". The marker stays exactly
// where it was, INCLUDING "nowhere".
//
// The failure this guards against is silent and re-orders the queue rather than
// breaking it. PushHead used to end with ensureSingleActive, which hands the
// marker to the new head whenever no record holds one — a marker retail never
// creates. With it, a producer insertion that should have appended at the TAIL
// instead lands immediately behind the spawned record.
func TestHeadInsertLeavesTheActiveMarkerAlone(t *testing.T) {
	moveID := Lookup("Move_Ground")
	stopID := Lookup("Stop")
	if moveID == 0 || stopID == 0 {
		t.Fatal("Move_Ground / Stop descriptors unavailable")
	}

	// A spawn into an empty segment leaves the segment unmarked.
	q, u := standingFixture(nil)
	if q.PushHead(stopID, Node{Owner: u.Handle}) == nil {
		t.Fatal("PushHead returned no record")
	}
	if got := q.Primary()[0].Flags & FlagActive; got != 0 {
		t.Fatal("a head insert into an empty segment must not create an active marker [04 R-ORD-01 §13]")
	}

	// The next producer insertion therefore appends at the TAIL, behind the
	// spawned record, and takes the marker itself.
	q.Push(moveID, Node{Owner: u.Handle})
	if n := q.LenPrimary(); n != 2 {
		t.Fatalf("primary length = %d, want 2", n)
	}
	if DescriptorFor(q.Primary()[1].ID).Name != "Move_Ground" {
		t.Fatalf("the producer insertion landed at index 0; with no marker it appends at the tail [04 R-ORD-01 §13]")
	}
	if q.Primary()[1].Flags&FlagActive == 0 || q.Primary()[0].Flags&FlagActive != 0 {
		t.Fatal("the inserted record takes the marker in every branch, including the tail append [04 R-ORD-01 §13]")
	}

	// A spawn in front of a marked record does not move the marker off it, so a
	// later producer insertion still queues behind the record that spawned.
	q.PushHead(stopID, Node{Owner: u.Handle})
	if q.Primary()[0].Flags&FlagActive != 0 {
		t.Fatal("the spawned record must not take the marker [04 R-ORD-01 §13]")
	}
	if q.Primary()[2].Flags&FlagActive == 0 {
		t.Fatal("the displaced marker must stay on the record that held it [04 R-ORD-01 §13]")
	}
}

// TestOnlyTheProducerInsertionArmsTheCaption locks the caption-pending writer of
// [04 R-ORD-01 §13]: the record constructor arms no runtime bit, and the one
// site that arms the caption-pending flag is the producer-side insertion. A
// handler spawn and a patrol-chain tail append are silent.
//
// Regressing this is audible, not fatal: arming it in the constructor — where
// it used to live — makes every spawned order and every idle refill speak the
// `ok` acknowledgement voice.
func TestOnlyTheProducerInsertionArmsTheCaption(t *testing.T) {
	moveID := Lookup("Move_Ground")
	stopID := Lookup("Stop")
	if moveID == 0 || stopID == 0 {
		t.Fatal("Move_Ground / Stop descriptors unavailable")
	}
	q, u := standingFixture(nil)

	q.Push(moveID, Node{Owner: u.Handle})
	if !q.Primary()[0].CaptionPending {
		t.Fatal("the producer insertion arms the caption-pending flag [04 R-ORD-01 §13]")
	}

	spawned := q.PushHead(stopID, Node{Owner: u.Handle})
	if spawned == nil {
		t.Fatal("PushHead returned no record")
	}
	if spawned.CaptionPending {
		t.Fatal("a handler spawn is inserted with the caption-pending flag clear [04 R-ORD-01 §13]")
	}

	appended := q.appendTail(moveID, Node{Owner: u.Handle})
	if appended == nil {
		t.Fatal("appendTail returned no record")
	}
	if appended.CaptionPending {
		t.Fatal("the patrol-chain tail append is inserted with the caption-pending flag clear [04 R-ORD-01 §13]")
	}
}
