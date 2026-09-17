package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// releaseYardCase drives one building through stamp and release with a chosen
// port-18 state, and optionally flips that state before the release without a
// restamp.
type releaseYardCase struct {
	name        string
	yardMap     string
	stampOpen   bool
	flipBefore  bool // change the live yard state before the release
	wantClaimed []bool
}

// TestReleasePlacementClearsEveryStampedCell locks the clear pass against
// [04 R-COLL-01 §4 "clear, in order"]: "building class — ground word equal to
// self -> 0, yard bit 0 -> clear the cell's flag-byte bit 1". The clear reads
// no yard state; only the stamp and the restamp select cells by yard byte. A
// clear gated on a yard state therefore strands every cell the other state had
// claimed, in both halves of Nanolathe's split ground plane, with no later
// writer to release it: a permanent refusal for anything placed there again.
//
// The yard map is `OoC` over a 3x1 footprint — `O` is selected only while the
// yard is open, `C` only while it is closed, `o` in both states — so each
// state claims a different pair of cells and every cell carries the bit-0
// structure-yard mark. The release must clear whichever pair was claimed,
// including when the live state no longer matches the stamp.
func TestReleasePlacementClearsEveryStampedCell(t *testing.T) {
	cases := []releaseYardCase{
		{
			name:        "open yard",
			yardMap:     "OoC",
			stampOpen:   true,
			wantClaimed: []bool{true, true, false},
		},
		{
			name:        "closed yard",
			yardMap:     "OoC",
			stampOpen:   false,
			wantClaimed: []bool{false, true, true},
		},
		{
			name:        "stamped open, live state closed at release",
			yardMap:     "OoC",
			stampOpen:   true,
			flipBefore:  true,
			wantClaimed: []bool{true, true, false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			terrain := exitTerrain(16, 16)
			def := newFactoryDef("yardreleasefac", 3, 1, 3000)
			def.YardMap = tc.yardMap
			cat := exitCatalog(def)
			svc, w := exitService(t, terrain, cat)

			h, err := w.Create(def, 0, world.CellToWorld(6), 0, world.CellToWorld(6))
			if err != nil {
				t.Fatalf("create building: %v", err)
			}
			u := w.Unit(h)
			if u == nil {
				t.Fatal("created building has no unit record")
			}
			// A yard write that lands before the first product re-stamp:
			// [04 §4.7] admits a Create-time port-18 write and keeps its bit
			// for the initial stamp. No stock script does this — port 18 is
			// written only from `OpenYard`/`CloseYard` in 23 of the 278 stock
			// unit scripts, and no `Create` reaches either — so this stands in
			// for mod content and for the admitted Create-time write.
			u.YardOpen = tc.stampOpen

			rect := completedOccupancyRect(t, 6, 6, 3, 1)
			if err := svc.reservePlacement(h, def, rect); err != nil {
				t.Fatalf("reserve placement: %v", err)
			}

			for i, want := range tc.wantClaimed {
				x := rect.MinX() + int32(i)
				cell := terrain.PlotAt(x, rect.MinZ())
				got := cell.OccupantA()
				if want && got != int16(h) {
					t.Fatalf("cell %d occupant=%d, want the stamping identity %d", i, got, h)
				}
				if !want && got == int16(h) {
					t.Fatalf("cell %d was claimed by %d, but this yard state does not select it", i, h)
				}
				if !cell.StructureYard() {
					t.Fatalf("cell %d lacks the bit-0 structure-yard mark", i)
				}
			}

			if tc.flipBefore {
				// The live state moves with no restamp, so the cells the stamp
				// claimed are the ones the current state would not select. The
				// clear must still release them.
				u.YardOpen = !tc.stampOpen
			}

			if !svc.ReleasePlacement(h) {
				t.Fatal("placement was not released")
			}
			for i := range tc.wantClaimed {
				x := rect.MinX() + int32(i)
				cell := terrain.PlotAt(x, rect.MinZ())
				if got := cell.OccupantA(); got != 0 {
					t.Fatalf("cell %d still held by %d after release [04 R-COLL-01 §4]", i, got)
				}
				if cell.StructureYard() {
					t.Fatalf("cell %d kept its structure-yard mark after release", i)
				}
			}
			if _, ok := svc.placements[h]; ok {
				t.Fatal("release left the placement record behind")
			}
		})
	}
}
