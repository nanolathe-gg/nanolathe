package visibility

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Circular clips its authored mask even when the center is outside the grid;
// the whole-center rejection belongs only to terrain-ray [03 R-VIS-01 §2].
func TestRefreshOffmapCenterByRaster(t *testing.T) {
	for _, ray := range []bool{false, true} {
		for _, command := range []bool{false, true} {
			for _, point := range []struct {
				name                 string
				x, z, probeX, probeZ int32
				overlaps             bool
			}{
				{"north", 4, -1, 4, 0, true}, {"west", -1, 4, 0, 4, true},
				{"south", 4, 8, 4, 7, true}, {"east", 8, 4, 7, 4, true},
				{"fully-outside", 4, -3, 4, 0, false},
			} {
				t.Run(fmt.Sprintf("%s/ray=%v/command=%v", point.name, ray, command), func(t *testing.T) {
					mode := ModeHistoryEnabled | ModeCurrentEnabled
					if ray {
						mode |= ModeTerrainRay
					}
					s := New(flatTerrain(16, 10), mode)
					s.SetShapes(&content.SightShapes{Shapes: []content.SightShape{{W: 3, H: 3, AnchorX: 1, AnchorY: 1, Opaque: []bool{true, true, true, true, true, true, true, true, true}}}})
					s.SetRayTables(&content.LOSTables{NumTables: 2, Tables: []content.LOSTable{{NumLines: 1, Lines: [][]int32{{1, 1, 0}}}, {}}})
					ob := Observer{Owner: 0, CX: point.x, CZ: point.z, HeightByte: 20, Radius: 32}
					if command {
						var eligible [10]bool
						eligible[0] = true
						s.RefreshMode(mode, false, eligible, []ModeRefreshObserver{{ID: 1, Observer: ob}})
					} else {
						s.Refresh(1, ob)
					}
					want := uint8(0)
					if !ray && point.overlaps {
						want = 1
					}
					idx := int(point.probeZ*s.W + point.probeX)
					if got := s.byteGrids[0][idx]; got != want {
						t.Fatalf("edge coverage=%d, want %d", got, want)
					}
					// An identical ordinary visit must not double the clipped contribution.
					s.Refresh(1, ob)
					if got := s.byteGrids[0][idx]; got != want {
						t.Fatalf("throttled coverage=%d, want %d", got, want)
					}
					// Moving fully off-map removes the preceding clipped footprint. Retiring
					// that empty mask must not subtract any border cell a second time.
					ob.CX, ob.CZ = -10, -10
					s.Refresh(1, ob)
					s.RetireObserver(1)
					for i, v := range s.byteGrids[0] {
						if v != 0 {
							t.Fatalf("retirement left cell %d at %d", i, v)
						}
					}
					if want != 0 && s.wordMask[idx]&1 == 0 {
						t.Fatal("retirement removed history")
					}
					// Direct retirement of an overlapping mask must balance too.
					ob.CX, ob.CZ = point.x, point.z
					s.Refresh(2, ob)
					s.RetireObserver(2)
					for i, v := range s.byteGrids[0] {
						if v != 0 {
							t.Fatalf("direct retirement left cell %d at %d", i, v)
						}
					}
				})
			}
		}
	}
}
