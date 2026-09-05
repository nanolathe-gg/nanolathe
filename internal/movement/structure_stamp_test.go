package movement

import "testing"

// TestNoteStructureStampReclassifiesTheRectangle locks the stamp half of the
// class-layer maintenance [04 R-COLL-01 §4]: "for the building class the
// derived-height recompute over the grown rectangle and a reclassification of
// the rectangle in every active class layer follow".
//
// Two directions matter, because one entry serves both the claim and the
// release: the cells a building's yard newly occupies must become blocked, and
// the cells it releases (a factory opening its door, [04 R-COLL-01 §4] "the
// yard-open port write") must become passable again. Nothing else rewrites
// them — the request revision pass walks live units through the occupant-age
// window and a building never enters it, since it has no mover and takes the
// classifier's unconditional null arm [04 R-PATH-01 §14].
func TestNoteStructureStampReclassifiesTheRectangle(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	layer := sys.ensureLayerRegistry().For("", wiringProfile)

	anchor := Cell{X: 6, Z: 6}
	const footX, footZ int16 = 2, 2
	// Handle 9 has no collision record, so HasMover reports no mover and the
	// occupant blocks at every watermark [04 R-PATH-01 §14] — the same arm a
	// building takes in production.
	const id = 9

	for z := anchor.Z; z < anchor.Z+int32(footZ); z++ {
		for x := anchor.X; x < anchor.X+int32(footX); x++ {
			if got := layer.Value(x, z); got != LayerClear {
				t.Fatalf("flat terrain cell (%d,%d) want clear got %d", x, z, got)
			}
		}
	}

	// The occupancy write alone leaves the layer stale: this is the defect the
	// stamp-side call closes.
	grid.Stamp(anchor, footX, footZ, id)
	if got := layer.Value(anchor.X, anchor.Z); got != LayerClear {
		t.Fatalf("occupancy write must not reclassify by itself, got %d", got)
	}

	sys.NoteStructureStamp(anchor, footX, footZ)
	for z := anchor.Z; z < anchor.Z+int32(footZ); z++ {
		for x := anchor.X; x < anchor.X+int32(footX); x++ {
			if got := layer.Value(x, z); got != LayerBlocked {
				t.Fatalf("stamped cell (%d,%d) want blocked got %d", x, z, got)
			}
		}
	}
	// The restamped rectangle is the anchor rectangle grown by the requester
	// footprint on the low side and the occupant size on the high side
	// [04 R-PATH-01 §2], so a 1x1 requester anchored one cell short of the
	// building reads it through its own ring and demotes to steep.
	if got := layer.Value(anchor.X-1, anchor.Z-1); got != LayerSteep {
		t.Fatalf("ring anchor want steep(1) got %d", got)
	}

	// The release direction: the yard transition clears the cells and the same
	// entry hands the layer back its passable verdict.
	grid.Clear(anchor, footX, footZ, id)
	sys.NoteStructureStamp(anchor, footX, footZ)
	for z := anchor.Z; z < anchor.Z+int32(footZ); z++ {
		for x := anchor.X; x < anchor.X+int32(footX); x++ {
			if got := layer.Value(x, z); got != LayerClear {
				t.Fatalf("released cell (%d,%d) want clear got %d", x, z, got)
			}
		}
	}
}
