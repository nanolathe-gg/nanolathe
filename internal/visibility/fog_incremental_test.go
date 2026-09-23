package visibility

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// fullFogReference derives s's fog from scratch on a separate service holding
// copies of s's grids, local player and mode, for the given window.
func fullFogReference(t *testing.T, s *Service, terrain *world.Terrain, window [4]int32) *FogCache {
	t.Helper()
	ref := New(terrain, s.Mode()&^ModeFogCacheValid)
	copy(ref.wordMask, s.wordMask)
	for p := range s.byteGrids {
		copy(ref.byteGrids[p], s.byteGrids[p])
	}
	ref.local = s.local
	ref.mode &^= ModeFogCacheValid
	ref.fog.inValid = false
	if window == [4]int32{} {
		ref.RebuildFog(0, 0)
	} else {
		ref.RebuildFogWindow(window[0], window[1], window[2], window[3])
	}
	return ref.Fog()
}

// Re-deriving only the cells whose inputs changed must publish the same bytes
// as the full rebuild, tick after tick, while sight footprints move, history
// accumulates, the viewer and the current-coverage mode change, and the window
// alternates between the whole map and a viewport [03 §3.3]. The revision
// moves exactly when the bytes do.
func TestIncrementalFogMatchesFullRebuild(t *testing.T) {
	const cells = 24 // visibility tiles; the terrain grid is twice that
	terrain := &world.Terrain{CellW: cells * 2, CellH: cells * 2}
	s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	seed := uint32(99)
	next := func(n int32) int32 {
		seed = seed*1664525 + 1013904223
		return int32((seed >> 8) % uint32(n))
	}
	type mover struct{ x, z, r int32 }
	movers := []mover{{2, 2, 3}, {20, 5, 4}, {10, 18, 2}, {0, 23, 3}, {23, 0, 2}}
	incremental := 0
	var prevWindow [4]int32
	for tick := 0; tick < 300; tick++ {
		// Current coverage: clear every byte grid, then stamp each mover's
		// square for its owner, the way a sight raster lands.
		for p := range s.byteGrids {
			clear(s.byteGrids[p])
		}
		for m := range movers {
			mv := &movers[m]
			mv.x = min(max(mv.x+next(3)-1, -2), cells+1)
			mv.z = min(max(mv.z+next(3)-1, -2), cells+1)
			owner := PlayerID(m & 1)
			for z := mv.z - mv.r; z <= mv.z+mv.r; z++ {
				for x := mv.x - mv.r; x <= mv.x+mv.r; x++ {
					if x < 0 || z < 0 || x >= cells || z >= cells {
						continue
					}
					i := z*cells + x
					s.byteGrids[owner][i]++
					s.wordMask[i] |= cellBit(owner)
				}
			}
		}
		switch {
		case tick%97 == 50:
			s.SetLocal(PlayerID(1 - s.local))
		case tick%61 == 30:
			s.SetMode(s.Mode() ^ ModeCurrentEnabled)
		}
		var window [4]int32
		if tick%40 >= 30 {
			window = [4]int32{160, 96, 320, 256}
		}
		s.mode &^= ModeFogCacheValid
		takesIncremental := s.fog.inValid && window == prevWindow
		prevWindow = window
		version := s.FogVersion()
		before0 := append([]uint8(nil), s.fog.ch0...)
		before1 := append([]uint8(nil), s.fog.ch1...)
		if window == [4]int32{} {
			s.RebuildFog(0, 0)
		} else {
			s.RebuildFogWindow(window[0], window[1], window[2], window[3])
		}
		if takesIncremental {
			incremental++
		}
		want := fullFogReference(t, s, terrain, window)
		got := s.Fog()
		if got.w != want.w || got.h != want.h || got.originX != want.originX || got.originZ != want.originZ {
			t.Fatalf("tick %d: window %dx%d@%d,%d, full rebuild %dx%d@%d,%d", tick, got.w, got.h, got.originX, got.originZ, want.w, want.h, want.originX, want.originZ)
		}
		if !bytes.Equal(got.ch0, want.ch0) || !bytes.Equal(got.ch1, want.ch1) {
			t.Fatalf("tick %d: incremental fog differs from the full rebuild", tick)
		}
		same := bytes.Equal(before0, got.ch0) && bytes.Equal(before1, got.ch1)
		if takesIncremental && same && s.FogVersion() != version {
			t.Fatalf("tick %d: unchanged fog bytes advanced the revision", tick)
		}
		if !same && s.FogVersion() == version {
			t.Fatalf("tick %d: changed fog bytes kept the revision", tick)
		}
	}
	if incremental == 0 {
		t.Fatal("no rebuild took the incremental path; the comparison proves nothing")
	}
}

// A write that does not come from a rebuild makes the next rebuild a full one.
func TestFogCacheWriteForcesFullRebuild(t *testing.T) {
	s := New(&world.Terrain{CellW: 16, CellH: 16}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.RebuildFog(0, 0)
	s.Fog().SetChannel(3, 3, 9, 9)
	s.mode &^= ModeFogCacheValid
	s.RebuildFog(0, 0)
	if c0, c1 := s.Fog().Channel(3, 3); c0 == 9 || c1 == 9 {
		t.Fatal("a rebuild after a foreign cache write kept the foreign byte")
	}
}
