package movement

import "testing"

// The overlap protocol's one branch condition reads the occupant owner's
// player-row CONTROL byte and compares it to 3 [04 R-COLL-01 §4]. Three
// different player bytes carry three different "state" vocabularies in the
// research — the control byte {1,2,3} [05 R-SHARE-01 §1], the sweep gate's
// separate byte with its eliminated value 10 [04 R-MOV-03 §1], and the neutral
// side sentinel 10 [08 P0-04] — so this test pins which one the protocol reads
// and at which value it branches. It is cheap and it is the exact thing a
// well-meaning correction would silently move.

func TestDisplaceableOwnerStateIsControlByteThree(t *testing.T) {
	if displaceableOwnerState != 3 {
		t.Fatalf("displaceableOwnerState = %d, want the control byte's remote-peer value 3 [04 R-COLL-01 §4][05 R-SHARE-01 §1]", displaceableOwnerState)
	}
}

// TestDisplaceableBranchesOnlyOnStateThree walks the whole control-byte value
// set through the live predicate. Only 3 displaces; 1 (local human) and 2
// (computer) do not, and neither does 10 — the value the sweep gate and the
// side sentinel use, which this protocol must never read as displacing.
func TestDisplaceableBranchesOnlyOnStateThree(t *testing.T) {
	fixture := newOverlapFixture(16)
	// One occupant at slot 1 owned by player 0; ownerState is supplied per case.
	fixture.add(1, 0, Cell{X: 2, Z: 2}, 1, 1)
	for _, state := range []uint8{0, 1, 2, 3, 4, 10, 255} {
		g := NewOccupancyGrid()
		g.AttachOverlap(fixture, func(uint8) uint8 { return state })
		want := state == 3
		if got := g.displaceable(1); got != want {
			t.Fatalf("displaceable with owner control byte %d = %v, want %v [04 R-COLL-01 §4]", state, got, want)
		}
	}
}

// TestDisplaceableFalseWithoutBinding locks the unbound fallbacks: a grid with
// no overlap window or no owner-state reader keeps the pre-protocol behavior
// (the occupant keeps the cell) rather than displacing everything.
func TestDisplaceableFalseWithoutBinding(t *testing.T) {
	g := NewOccupancyGrid()
	if g.displaceable(1) {
		t.Fatal("unbound grid displaced an occupant")
	}
	fixture := newOverlapFixture(16)
	fixture.add(1, 0, Cell{X: 2, Z: 2}, 1, 1)
	g.AttachOverlap(fixture, nil)
	if g.displaceable(1) {
		t.Fatal("grid with no owner-state reader displaced an occupant")
	}
	// An identity no live unit answers for is not displaceable either.
	g.AttachOverlap(fixture, func(uint8) uint8 { return displaceableOwnerState })
	if g.displaceable(77) {
		t.Fatal("unknown occupant identity was reported displaceable")
	}
}
