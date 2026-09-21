package mission

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// TestInitialMissionAppendsInQueuedMode locks [04 §3.6]'s "each queuing verb
// ... appends one record in queued mode" against the one consumer the modifier
// has: the producer insertion's caption-pending bit, which a non-queued
// (Replace) issue arms and a queued issue does not [04 R-ORD-01 §13]. An armed
// mission record makes the viewing player's units speak the `ok` cue the first
// time a handler runs the shared caption clear, at the start of every mission
// whose script queues orders.
//
// The same test pins the insertion POSITION, because queued mode must change
// nothing else: the script's records stand in script order at the head of an
// empty queue, exactly where a non-queued push put them.
func TestInitialMissionAppendsInQueuedMode(t *testing.T) {
	w := newMissionFixtureWorld(5, nil)
	h, _ := w.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u := atPlacement(w.Unit(h), 0)
	m := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{
		UnitName:       "ARMCOM",
		InitialMission: "m 10 20,m 30 40",
	}}}
	RunInitialMissionsWithCatalog(m, w, testInitialCatalog)

	nodes := primaryNodes(u)
	if len(nodes) != 3 { // two moves plus the postlude MakeSelectable
		t.Fatalf("mission script queued %d records, want 3", len(nodes))
	}
	moveGround := orders.Lookup("Move_Ground")
	for i, n := range nodes[:2] {
		if n.ID != moveGround {
			t.Fatalf("record %d is %v, want the resolved Move_Ground in script order", i, n.ID)
		}
		if n.CaptionPending {
			t.Fatalf("mission record %d is caption-armed; a queued issue does not arm it [04 §3.6][04 R-ORD-01 §13]", i)
		}
	}

	// The arm itself must survive: a player's plain (non-queued) click on the
	// same queue still arms the bit, which is what makes the `ok` cue reachable
	// at all.
	orders.QueueForUnit(u).Push(moveGround, orders.Node{Owner: u.Handle})
	nodes = primaryNodes(u)
	clicked := nodes[len(nodes)-1]
	for _, n := range nodes {
		if n.ID == moveGround && n.CaptionPending {
			clicked = n
		}
	}
	if !clicked.CaptionPending {
		t.Fatal("a non-queued issue no longer arms the caption-pending bit [04 R-ORD-01 §13]")
	}
}
