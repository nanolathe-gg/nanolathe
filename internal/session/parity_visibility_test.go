package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// The Circular frame overlaps row zero even though its center projects above
// the map. Ordinary and command publication must both reach the clipped raster
// [03 R-VIS-01 §2 "Sprite-mask branch"].
func TestCircularNorthEdgePublicationAndRetirement(t *testing.T) {
	for _, command := range []bool{false, true} {
		t.Run(fmt.Sprintf("command=%v", command), func(t *testing.T) {
			s := singleCellSightFixture(t)
			s.Vis.SetShapes(&content.SightShapes{Shapes: []content.SightShape{{W: 3, H: 3, AnchorX: 1, AnchorY: 1, Opaque: []bool{true, true, true, true, true, true, true, true, true}}}})
			u := placeObserver(t, s, s.Catalog.Units[content.CanonicalKey("armcom")], 0, 320, 128, 32)
			cx, cz := observerCell(s, u, heightByteAt(u, seaLevelFor(s)))
			if cx != 10 || cz != -1 {
				t.Fatalf("observer=(%d,%d), want (10,-1)", cx, cz)
			}
			if command {
				eligible, observers := visibilityModeRefreshInputs(s, s.Vis.Mode())
				s.Vis.RefreshMode(s.Vis.Mode(), false, eligible, observers)
			} else {
				publishOne(s, u)
			}
			if !coveredCell(s, 0, 10, 0) {
				t.Fatal("Circular mask lost its in-map overlap")
			}
			unpublishOne(s, u)
			for i, v := range s.Vis.ByteGrid(0) {
				if v != 0 {
					t.Fatalf("retired mask left cell %d at %d", i, v)
				}
			}
		})
	}
}

// Death sight uses the same clipped Circular footprint and removes it only
// after its expiry tick [03 R-VIS-01 §2][03 R-COMP-02 §2]. The True branch must
// continue rejecting an off-map observer center even with in-map spoke steps.
func TestNorthEdgeDeathSightByRaster(t *testing.T) {
	for _, ray := range []bool{false, true} {
		t.Run(fmt.Sprintf("ray=%v", ray), func(t *testing.T) {
			s := newEyeballSession(t)
			s.Vis.SetShapes(&content.SightShapes{Shapes: []content.SightShape{{W: 3, H: 3, AnchorX: 1, AnchorY: 1, Opaque: []bool{true, true, true, true, true, true, true, true, true}}}})
			s.Vis.SetRayTables(&content.LOSTables{NumTables: 2, Tables: []content.LOSTable{{NumLines: 1, Lines: [][]int32{{1, 0, 1}}}, {}}})
			mode := visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled
			if ray {
				mode |= visibility.ModeTerrainRay
			}
			s.Vis.SetMode(mode)
			u := spawnEyeballVictim(t, s, 0, 10)
			u.X, u.Y, u.Z = worldUnits(320), worldUnits(128), worldUnits(32)
			s.Vis.RebuildAll(nil)
			publishOne(s, u)
			s.appendDeathEyeball(u)
			if s.EyeballCount() != 1 {
				t.Fatal("death sight was not appended")
			}
			unpublishOne(s, u)
			if got := coveredCell(s, 0, 10, 0); got == ray {
				t.Fatalf("death sight coverage=%v, want %v", got, !ray)
			}
			expiry := s.postLoop.eyeballs.records[0].expiry
			s.postLoop.eyeballs.expire(s.Vis, expiry)
			if got := coveredCell(s, 0, 10, 0); got == ray {
				t.Fatal("coverage changed at expiry equality")
			}
			s.postLoop.eyeballs.expire(s.Vis, expiry+1)
			for i, v := range s.Vis.ByteGrid(0) {
				if v != 0 {
					t.Fatalf("expired sight left cell %d at %d", i, v)
				}
			}
		})
	}
}

// All observer producers consume the same signed sight word, without replacing
// zero/negative values. Group zero retains its sanctioned empty spoke list
// [03 R-VIS-01 §2 "The observer record"][03 R-COMP-02 §1].
func TestSightDistanceSignedWordAcrossObservers(t *testing.T) {
	for _, ray := range []bool{false, true} {
		for _, tc := range []struct{ authored, stored int32 }{
			{0, 0}, {-1, -1}, {40000, -25536}, {65536, 0}, {65568, 32}, {160, 160}, {192, 192}, {-65344, 192},
		} {
			t.Run(fmt.Sprintf("ray=%v/sight=%d", ray, tc.authored), func(t *testing.T) {
				s := newEyeballSession(t)
				s.Vis.SetShapes(&content.SightShapes{Shapes: []content.SightShape{{W: 1, H: 1, Opaque: []bool{true}}, {W: 2, H: 1, Opaque: []bool{true, true}}}})
				s.Vis.SetRayTables(&content.LOSTables{NumTables: 2, Tables: []content.LOSTable{{NumLines: 1, Lines: [][]int32{{1, 1, 0}}}, {}}})
				mode := visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled
				if ray {
					mode |= visibility.ModeTerrainRay
				}
				s.Vis.SetMode(mode)
				u := spawnEyeballVictim(t, s, 0, 10)
				u.Def.SightDistance = tc.authored
				s.Vis.RebuildAll(nil)
				if got := radiusFor(u); got != tc.stored {
					t.Fatalf("observer radius=%d, want %d", got, tc.stored)
				}
				cx, cz := observerCell(s, u, heightByteAt(u, seaLevelFor(s)))
				wantEast := tc.stored >= 192
				if ray {
					wantEast = tc.stored >= 32
				}
				check := func(stage string) {
					t.Helper()
					if !coveredCell(s, 0, cx, cz) {
						t.Fatalf("%s: origin not covered", stage)
					}
					if got := coveredCell(s, 0, cx+1, cz); got != wantEast {
						t.Fatalf("%s: east coverage=%v, want %v", stage, got, wantEast)
					}
				}
				publishOne(s, u)
				check("live")
				eligible, observers := visibilityModeRefreshInputs(s, mode)
				s.Vis.RefreshMode(mode, false, eligible, observers)
				check("mode command")
				s.appendDeathEyeball(u)
				unpublishOne(s, u)
				if s.EyeballCount() != 1 {
					t.Fatal("death sight was not appended")
				}
				if got := int32(s.postLoop.eyeballs.records[0].sightDistance); got != tc.stored {
					t.Fatalf("death radius=%d, want %d", got, tc.stored)
				}
				check("death")
				s.postLoop.eyeballs.expire(s.Vis, s.postLoop.eyeballs.records[0].expiry+1)
				for i, v := range s.Vis.ByteGrid(0) {
					if v != 0 {
						t.Fatalf("expiry left cell %d at %d", i, v)
					}
				}
			})
		}
	}
}
