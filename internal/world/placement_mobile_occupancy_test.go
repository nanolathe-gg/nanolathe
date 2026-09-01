package world

import "testing"

// stubOccupancy is an authored mover-occupancy plane: a single held cell.
type stubOccupancy struct {
	x, z int32
	id   uint16
}

func (s stubOccupancy) CellOccupant(cellX, cellZ int32) uint16 {
	if cellX == s.x && cellZ == s.z {
		return s.id
	}
	return 0
}

func mobileOccupancyTerrain() *Terrain {
	t := &Terrain{CellW: 8, CellH: 8, Plot: make([]PlotCell, 8*8)}
	for i := range t.Plot {
		t.Plot[i].SetFeature(PlotFeatureNone)
	}
	return t
}

func mobileOccupancyRect(t *testing.T, cx, cz, w, d int32) FootprintRect {
	t.Helper()
	extent, err := NewFootprintExtent(w, d)
	if err != nil {
		t.Fatalf("extent: %v", err)
	}
	rect, err := NewFootprintRect(NewFootprintAnchor(cx, cz), extent)
	if err != nil {
		t.Fatalf("rect: %v", err)
	}
	return rect
}

// TestMobileOccupancyRejectsAForeignOccupant locks the occupant test of
// [04 R-COLL-01 §2] across BOTH halves of Nanolathe's split ground word:
// retail keeps one occupancy word per cell, written by ground movers and by
// building-class units alike, and "bits 1-2 reject any nonzero occupant other
// than the passed self identity"
// [05 "control-byte bit roles in the footprint validator"].
//
// This is the contract a mobile builder used to escape: only construction
// writes the plot half, so a builder standing on its own site was invisible to
// the validator and stamped a nanoframe over itself.
func TestMobileOccupancyRejectsAForeignOccupant(t *testing.T) {
	terrain := mobileOccupancyTerrain()
	rect := mobileOccupancyRect(t, 1, 1, 3, 3)
	rules := PlacementRules{MaxSlope: 255, MaxWaterSlope: 255, MaxWaterDepth: 10000, MinWaterDepth: -10000}

	free := PlacementQuery{Rect: rect, Rules: rules, Mobile: true}
	if _, err := terrain.CheckPlacement(free); err != nil {
		t.Fatalf("free rectangle must validate, got %v", err)
	}

	// The mover plane is bound to the terrain for the whole battle, not
	// elected per query, so every check below sees it.
	terrain.Movers = stubOccupancy{x: 2, z: 2, id: 7}
	held := free
	if _, err := terrain.CheckPlacement(held); err == nil {
		t.Fatal("a covered cell held by a mover must reject the placement [04 R-COLL-01 §2]")
	}

	// Every placement caller passes a null self identity [04 R-COLL-01 §6], so
	// the exemption below is exercised only by the builder-clearance filter
	// that asks whether the builder itself can stand on its own cells.
	self := held
	self.Self = 7
	if _, err := terrain.CheckPlacement(self); err != nil {
		t.Fatalf("the passed self identity must be exempt, got %v", err)
	}

	// A yard byte without the occupancy bits does not test occupancy at all,
	// in either half of the word [05 "control-byte bit roles in the footprint
	// validator"].
	yard := held
	yard.Mobile = false
	yard.Yard = make([]YardCell, 9)
	for i := range yard.Yard {
		yard.Yard[i] = 0x08 // slope sampling only, no occupancy bits
	}
	if _, err := terrain.CheckPlacement(yard); err != nil {
		t.Fatalf("a yard byte that does not test occupancy must ignore the mover, got %v", err)
	}
}

// TestMoverPlaneIsNotAPerQueryChoice locks the ownership that the split-word
// bug came from: the mover half belongs to the terrain, so a caller cannot
// reach CheckPlacement while consulting only the plot half.
//
// While each caller supplied its own plane, six production placement queries
// existed and four passed nothing: the build-placement preview published to
// the player, AI siting, and the commander-death check. Those three silently
// skipped the mover test, so the preview drew a site legal with a unit parked
// on it and the AI would site a building there [04 R-COLL-01 §2].
func TestMoverPlaneIsNotAPerQueryChoice(t *testing.T) {
	rect := mobileOccupancyRect(t, 1, 1, 3, 3)
	rules := PlacementRules{MaxSlope: 255, MaxWaterSlope: 255, MaxWaterDepth: 10000, MinWaterDepth: -10000}
	query := PlacementQuery{Rect: rect, Rules: rules, Mobile: true}

	// A caller that knows nothing about movement still gets the mover test,
	// because the terrain it was handed carries the plane.
	terrain := mobileOccupancyTerrain()
	terrain.Movers = stubOccupancy{x: 2, z: 2, id: 7}
	if _, err := terrain.CheckPlacement(query); err == nil {
		t.Fatal("a terrain-bound mover must reject the placement for every caller")
	}

	// And the same query on a terrain with no mover plane at all is legal:
	// nil means no plane exists yet, not that this caller opted out.
	bare := mobileOccupancyTerrain()
	if _, err := bare.CheckPlacement(query); err != nil {
		t.Fatalf("terrain with no mover plane must validate, got %v", err)
	}
}
