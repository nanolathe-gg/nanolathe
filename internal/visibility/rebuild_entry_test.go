package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func entryRayService(mode Mode) *Service {
	s := New(flatTerrain(64, 10), mode)
	s.SetRayTables(&content.LOSTables{NumTables: 1, Tables: []content.LOSTable{{TableNum: 0}}})
	s.SetShapes(oneCellShape())
	return s
}

// TestRebuildEntryRefillsBothStoresAndRestampsInRecordOrder locks
// [08 R-ENTRY-01 §7] steps 1-3 for the Unmapped + current-coverage mode every
// stock skirmish runs: the word grid is refilled from mode bit 0 whether or not
// the caller asked for a history reset, each eligible slot's byte grid is
// refilled from mode bit 1, an ineligible slot keeps its stale bytes, and every
// supplied unit is stamped again.
func TestRebuildEntryRefillsBothStoresAndRestampsInRecordOrder(t *testing.T) {
	s := entryRayService(ModeHistoryEnabled | ModeCurrentEnabled | ModeTerrainRay)
	s.SetLocal(0)
	s.Refresh(1, Observer{Owner: 0, CX: 4, CZ: 5, HeightByte: 20, Radius: 20})
	remembered := int(9*s.W + 9)
	s.wordMask[remembered] |= cellBit(0) // ground explored before the rebuild
	s.byteGrids[2][remembered] = 7       // an ineligible slot's stale bytes

	var eligible [10]bool
	eligible[0] = true
	s.RebuildEntry(eligible, []ModeRefreshObserver{{
		ID: 1, Observer: Observer{Owner: 0, CX: 4, CZ: 5, HeightByte: 20, Radius: 20},
	}})

	if got := s.wordMask[remembered]; got != 0 {
		t.Fatalf("step 1 left pre-rebuild map memory at the remembered cell: %#x", got)
	}
	if got := s.byteGrids[2][remembered]; got != 7 {
		t.Fatalf("step 2 refilled an ineligible slot: byte %d, want the stale 7 [08 R-ENTRY-01 §7]", got)
	}
	stamped := int(5*s.W + 4)
	if s.wordMask[stamped]&cellBit(0) == 0 {
		t.Fatal("step 3 did not re-stamp the supplied observer into the word grid")
	}
	if got := s.byteGrids[0][stamped]; got != 1 {
		t.Fatalf("step 3 byte refcount = %d, want exactly one contribution", got)
	}
	// The stamp must leave a stored record behind, or the next throttled
	// refresh republishes on top of it and the refcount never returns to zero
	// [03 R-VIS-01 §2].
	s.Refresh(1, Observer{Owner: 0, CX: 4, CZ: 5, HeightByte: 20, Radius: 20})
	if got := s.byteGrids[0][stamped]; got != 1 {
		t.Fatalf("an unchanged observer double-stamped after the rebuild: %d", got)
	}
}

// TestRebuildEntryWithBothBitsClearFillsAllVisible is the watcher/observer
// state: clearing mode bits 0 and 1 is Mapped + Permanent, and the forced bulk
// rebuild fills the word grid with every usable player bit and each eligible
// slot's byte grid with 1, so nothing is fogged [03 R-VIS-01 §1]
// [03 R-VIS-01 §4] pass 1. Step 3 visits no unit while bit 1 is clear.
func TestRebuildEntryWithBothBitsClearFillsAllVisible(t *testing.T) {
	s := entryRayService(ModeHistoryEnabled | ModeCurrentEnabled | ModeTerrainRay)
	s.SetLocal(0)
	s.Refresh(1, Observer{Owner: 0, CX: 4, CZ: 5, HeightByte: 20, Radius: 20})

	var eligible [10]bool
	eligible[0] = true
	s.SetMode(s.Mode() &^ (ModeHistoryEnabled | ModeCurrentEnabled))
	s.RebuildEntry(eligible, []ModeRefreshObserver{{
		ID: 1, Observer: Observer{Owner: 0, CX: 4, CZ: 5, HeightByte: 20, Radius: 20},
	}})

	for i, got := range s.wordMask {
		if got != 0x03FF {
			t.Fatalf("word cell %d = %#x, want every usable player bit", i, got)
		}
	}
	for i, got := range s.byteGrids[0] {
		if got != 1 {
			t.Fatalf("eligible byte cell %d = %d, want the all-visible fill", i, got)
		}
	}
	// Step 3 is gated on mode bit 1, so the saved observer record survives the
	// rebuild untouched [08 R-ENTRY-01 §7] step 3.
	if _, ok := s.footprints[1]; !ok {
		t.Fatal("rebuild discarded the saved observer record while current coverage was disabled")
	}
}
