package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// px converts a map-pixel coordinate to 16.16 world units [03 §2.1].
func px(n int64) numeric.Fixed { return numeric.Fixed(n << 16) }

// audienceFixture builds a session whose visibility service covers an 8x8-cell
// map, which is a 4x4 coverage grid [03 §3.1].
func audienceFixture(mode visibility.Mode) *Session {
	return &Session{Vis: visibility.New(&world.Terrain{CellW: 8, CellH: 8}, mode)}
}

// lightByte marks a coverage cell explored for one player; lightWord sets that
// player's bit in the LOS word mask. Both write the service's own storage
// because the raster entry points need an authored shape table, and the gate
// under test reads one cell, not a footprint.
func lightByte(s *Session, player visibility.PlayerID, cx, cz int32, on bool) {
	g := s.Vis.ByteGrid(player)
	w, _ := s.Vis.GridDimensions()
	v := uint8(0)
	if on {
		v = 1
	}
	g[cz*w+cx] = v
}

func lightWord(s *Session, player visibility.PlayerID, cx, cz int32, on bool) {
	m := s.Vis.WordMask()
	w, _ := s.Vis.GridDimensions()
	bit := uint16(1) << player
	if on {
		m[cz*w+cx] |= bit
	} else {
		m[cz*w+cx] &^= bit
	}
}

// TestIsAudibleAtUsesHeightShearedPoint locks the audience gate to the
// projected point rather than the ground cell under it [03 §8.3] "audience
// gating", [03 §3.2] step 4.
//
// The regression: the gate quantized X and Z alone and ignored Y, so an
// aircraft or a hilltop weapon was tested at the cell beneath it. World point
// (32, 64, 64) map pixels projects to coverage cell (1, 1) — u = 32>>5 = 1,
// v = (64 - (64>>1))>>5 = 1 — while the unsheared quantization lands on
// (1, 2). Both converses are asserted so neither cell can be substituted for
// the other, and both coverage modes are covered because the mode word's
// bit 1 selects which grid answers.
func TestIsAudibleAtUsesHeightShearedPoint(t *testing.T) {
	elevated := [3]numeric.Fixed{px(32), px(64), px(64)}

	t.Run("explored byte grid", func(t *testing.T) {
		s := audienceFixture(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled)
		lightByte(s, 0, 1, 1, true)
		if !s.IsAudibleAt(elevated) {
			t.Fatal("projected cell (1,1) is explored: the sound must be audible")
		}
		lightByte(s, 0, 1, 1, false)
		lightByte(s, 0, 1, 2, true)
		if s.IsAudibleAt(elevated) {
			t.Fatal("only the unsheared ground cell (1,2) is explored: the sound must be silent")
		}
	})

	t.Run("LOS word mask", func(t *testing.T) {
		s := audienceFixture(visibility.ModeHistoryEnabled)
		lightWord(s, 0, 1, 1, true)
		if !s.IsAudibleAt(elevated) {
			t.Fatal("projected cell (1,1) is in line of sight: the sound must be audible")
		}
		lightWord(s, 0, 1, 1, false)
		lightWord(s, 0, 1, 2, true)
		if s.IsAudibleAt(elevated) {
			t.Fatal("only the unsheared ground cell (1,2) is lit: the sound must be silent")
		}
	})
}

// TestIsAudibleAtTestsLocalPlayerOnly locks the no-ally-OR rule of
// [03 §3.1]: only the local viewing player's bit or explored count admits.
func TestIsAudibleAtTestsLocalPlayerOnly(t *testing.T) {
	elevated := [3]numeric.Fixed{px(32), px(64), px(64)}

	t.Run("explored byte grid", func(t *testing.T) {
		s := audienceFixture(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled)
		s.LocalOwner = 1
		s.Vis.SetLocal(1)
		lightByte(s, 0, 1, 1, true)
		if s.IsAudibleAt(elevated) {
			t.Fatal("another player's explored cell must not admit the local audience")
		}
		lightByte(s, 1, 1, 1, true)
		if !s.IsAudibleAt(elevated) {
			t.Fatal("the local player's own explored cell must admit")
		}
	})

	t.Run("LOS word mask", func(t *testing.T) {
		s := audienceFixture(visibility.ModeHistoryEnabled)
		s.LocalOwner = 1
		s.Vis.SetLocal(1)
		lightWord(s, 0, 1, 1, true)
		if s.IsAudibleAt(elevated) {
			t.Fatal("another player's LOS bit must not admit the local audience")
		}
		lightWord(s, 1, 1, 1, true)
		if !s.IsAudibleAt(elevated) {
			t.Fatal("the local player's own LOS bit must admit")
		}
	})
}

// TestIsAudibleAtSignedEdgesAndBounds locks the two halves of the projection
// that a naive quantization gets wrong [03 §3.2] step 4: the narrowing of each
// 16.16 coordinate to a SIGNED 16-bit map-pixel component, which wraps rather
// than saturating, and the unsigned bounds test that turns any negative
// projected component into a rejection.
func TestIsAudibleAtSignedEdgesAndBounds(t *testing.T) {
	s := audienceFixture(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled)
	for cz := int32(0); cz < 4; cz++ {
		for cx := int32(0); cx < 4; cx++ {
			lightByte(s, 0, cx, cz, true)
		}
	}

	cases := []struct {
		name string
		pos  [3]numeric.Fixed
		want bool
	}{
		{"inside", [3]numeric.Fixed{px(32), px(64), px(64)}, true},
		{"west of the map", [3]numeric.Fixed{px(-1), 0, px(64)}, false},
		{"north of the map", [3]numeric.Fixed{px(32), 0, px(-1)}, false},
		// The shear alone can carry an in-bounds Z off the north edge: with
		// z = 0 and y = 64 the projected v is (0 - 32) >> 5 = -1.
		{"sheared off the north edge", [3]numeric.Fixed{px(32), px(64), 0}, false},
		{"east of the map", [3]numeric.Fixed{px(128), 0, px(64)}, false},
		{"south of the map", [3]numeric.Fixed{px(32), 0, px(128)}, false},
		// 65,568 map pixels narrows to +32 as a signed 16-bit quantity, so the
		// point wraps back onto cell column 1 instead of saturating off-map.
		{"pixel component wraps at 16 bits", [3]numeric.Fixed{px(65568), px(64), px(64)}, true},
	}
	for _, c := range cases {
		if got := s.IsAudibleAt(c.pos); got != c.want {
			t.Errorf("%s: audible=%v want %v", c.name, got, c.want)
		}
	}
}

var audienceSink bool

// TestIsAudibleAtAllocatesNothing locks the audience query to a scalar answer.
// The gate once copied the word mask and all ten player byte grids to read one
// cell, which put a map-sized allocation on every queued weapon and feature
// cue. The assertion is the allocation count, not a timing threshold.
func TestIsAudibleAtAllocatesNothing(t *testing.T) {
	s := &Session{Vis: visibility.New(&world.Terrain{CellW: 512, CellH: 512}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)}
	lightByte(s, 0, 1, 1, true)
	pos := [3]numeric.Fixed{px(32), px(64), px(64)}
	if n := testing.AllocsPerRun(100, func() { audienceSink = s.IsAudibleAt(pos) }); n != 0 {
		t.Fatalf("audience query allocates %.0f times per call, want 0", n)
	}
}

func BenchmarkIsAudibleAt(b *testing.B) {
	s := &Session{Vis: visibility.New(&world.Terrain{CellW: 512, CellH: 512}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)}
	lightByte(s, 0, 1, 1, true)
	pos := [3]numeric.Fixed{px(32), px(64), px(64)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		audienceSink = s.IsAudibleAt(pos)
	}
}
