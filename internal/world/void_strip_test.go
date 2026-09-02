package world_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// voidStripW/H are the authored fixture's dimensions. Sixteen rows is the
// smallest size that lets both height walks of [03 R-TERR-01 §2] run to their
// stated depth: the north walk can reach row 7 and the south walk row H−8.
const (
	voidStripW = 16
	voidStripH = 16
)

// voidStripHeights authors one height byte per cell of a 16×16 canonical map,
// chosen so that each of the four rules of [03 R-TERR-01 §2] fires in a column
// of its own with a hand-computed row set. Nothing here is retail data.
//
// The baseline is height = 16·z, which is quiet under both walks on a 16-row
// map: PlayBottom is 16·16 − 128 = 128, so the north walk stops at row 0
// (0 − (0>>1) = 0, not negative) and the south walk stops at row 15
// (15·16 − (240>>1) = 120, at or below 128). Every override below is a
// deliberate departure from that quiet baseline.
//
//	col  2  rows 0–1 poke above the north edge, row 2 stops the walk
//	col  4  row 0 stops the walk immediately, so the spike at row 1 survives
//	col  6  the bottom row is low, so the walk voids the row ABOVE it
//	col  8  the bottom row is tall, so the walk stops and the low row 14 survives
//	col 10  the whole south end is flat zero: the walk reaches its deepest row
//	col 12  the whole north end is tall: the walk reaches its deepest row
func voidStripHeights() []uint8 {
	h := make([]uint8, voidStripW*voidStripH)
	at := func(x, z int) *uint8 { return &h[z*voidStripW+x] }
	for z := 0; z < voidStripH; z++ {
		for x := 0; x < voidStripW; x++ {
			*at(x, z) = uint8(16 * z)
		}
	}
	// North strip, rule 3. Rows 0 and 1 fail the test (0−5 and 16−100 are
	// negative) and row 2 passes it (32−16 = 16), so rows 0 and 1 are voided.
	*at(2, 0) = 10
	*at(2, 1) = 200
	// North strip, the stop. Row 0 passes (0−0 = 0), so the walk halts there
	// and the tall row 1 is NOT voided even though its own test would fail.
	*at(4, 1) = 200
	// South strip, the row above. Row 15's test is 15·16 − 50 = 190 > 128, so
	// the walk continues and voids row 14; row 14's own test is
	// 14·16 − 112 = 112 ≤ 128, which stops it. Row 15 itself is never voided.
	*at(6, 15) = 100
	// South strip, the stop. Row 15's test is 15·16 − 115 = 125 ≤ 128, so the
	// walk halts at once and the zero-height row 14 is NOT voided.
	*at(8, 15) = 230
	*at(8, 14) = 0
	// South strip, deepest reach. With zero heights the test is z·16 > 128 for
	// z = 15…9, voiding rows 14…8, and row 8's own test is 128 ≤ 128, which
	// stops the walk. Row 15 is still untouched.
	for z := 8; z < voidStripH; z++ {
		*at(10, z) = 0
	}
	// North strip, deepest reach. Height 254 halves to 127, so rows 0…6 all
	// fail (96 − 127 is still negative) and row 7 passes exactly
	// (112 − 112 = 0). Row 7 is the first row a height byte can never reach
	// past, which is why row 8 is never voided by this rule.
	for z := 0; z <= 6; z++ {
		*at(12, z) = 254
	}
	*at(12, 7) = 224
	return h
}

// canonicalVoidStripTNT encodes the fixture as a structurally valid canonical
// (0x2000) TNT: sixteen 32-bit header slots, a (W/2)×(H/2) tile index map, one
// tile of graphics, 4-byte attribute records (height, LE uint16 feature
// reference, unused byte) and two feature records [fmt tnt][03 R-TERR-01 §1].
// One live feature sits at (14, 5), inside the right void columns, so its
// survival can be asserted.
func canonicalVoidStripTNT(t *testing.T) []byte {
	t.Helper()
	le := binary.LittleEndian
	const (
		tileMapOffset  = 0x40
		featureRecords = 2
	)
	tileMapBytes := (voidStripW / 2) * (voidStripH / 2) * 2
	attrOffset := tileMapOffset + tileMapBytes
	gfxOffset := attrOffset + voidStripW*voidStripH*4
	featOffset := gfxOffset + 1024
	miniOffset := featOffset + featureRecords*132
	b := make([]byte, miniOffset+8)

	le.PutUint32(b[0x00:], 0x2000) // slot 0: canonical version
	le.PutUint32(b[0x04:], voidStripW)
	le.PutUint32(b[0x08:], voidStripH)
	le.PutUint32(b[0x0c:], tileMapOffset)
	le.PutUint32(b[0x10:], uint32(attrOffset))
	le.PutUint32(b[0x14:], uint32(gfxOffset))
	le.PutUint32(b[0x18:], 1)              // slot 6: tile count
	le.PutUint32(b[0x1c:], featureRecords) // slot 7: feature record count
	le.PutUint32(b[0x20:], uint32(featOffset))
	le.PutUint32(b[0x24:], 0)                  // slot 9: sea level
	le.PutUint32(b[0x28:], uint32(miniOffset)) // slot 10: minimap offset
	le.PutUint32(b[0x2c:], 0)                  // slot 11: no embedded minimap

	heights := voidStripHeights()
	for i := 0; i < voidStripW*voidStripH; i++ {
		rec := b[attrOffset+i*4 : attrOffset+i*4+4]
		rec[0] = heights[i]
		feature := uint16(world.PlotFeatureNone)
		if i == 5*voidStripW+14 {
			feature = 1
		}
		le.PutUint16(rec[1:3], feature)
	}
	for n := 0; n < featureRecords; n++ {
		rec := b[featOffset+n*132 : featOffset+(n+1)*132]
		le.PutUint32(rec, uint32(n))
		copy(rec[4:], []byte("fixture"+string(rune('a'+n))))
	}
	return b
}

// loadVoidStripFixture writes the fixture into a temporary directory and takes
// it through the ordinary map loader, so the sweep is exercised where it runs
// rather than through a hand-assembled Terrain.
func loadVoidStripFixture(t *testing.T) *world.Terrain {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "maps", "voidstrip.tnt"), canonicalVoidStripTNT(t), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 0); err != nil {
		t.Fatalf("mount: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	terrain, err := world.Load(fs, nil, "voidstrip")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	return terrain
}

// TestVoidStripPassMatchesTheFourRules locks the loader's edge sweep of
// [03 R-TERR-01 §2] cell by cell: the expected set below is written out by
// hand from the four rules, not recomputed from the loader's own arithmetic.
//
// Two of its entries are the corrections that finding carries against doc 02's
// earlier summary, and both were wrong here before WU-19-48. Column 4 has a
// tall row 1 that the old per-row form voided although the walk had already
// stopped at row 0, and column 6 voids row 14 while leaving row 15 alone —
// the old form voided the row it tested, which put a void in the bottom row
// that retail never writes.
func TestVoidStripPassMatchesTheFourRules(t *testing.T) {
	terrain := loadVoidStripFixture(t)

	// Rule 1: the play insets are written by this pass [03 R-TERR-01 §2].
	if terrain.PlayRight != voidStripW*16-32 || terrain.PlayBottom != voidStripH*16-128 {
		t.Fatalf("play insets = %d/%d, want %d/%d",
			terrain.PlayRight, terrain.PlayBottom, voidStripW*16-32, voidStripH*16-128)
	}

	want := map[[2]int32]bool{}
	// Rule 2: both right columns, every row. The live feature at (14, 5) is
	// not convertible and keeps its index.
	for z := int32(0); z < voidStripH; z++ {
		for x := int32(voidStripW - 2); x < voidStripW; x++ {
			if x == 14 && z == 5 {
				continue
			}
			want[[2]int32{x, z}] = true
		}
	}
	// Rule 3: column 2 rows 0–1, column 12 rows 0–6. Column 4 contributes
	// nothing — its walk stops at row 0.
	for _, z := range []int32{0, 1} {
		want[[2]int32{2, z}] = true
	}
	for z := int32(0); z <= 6; z++ {
		want[[2]int32{12, z}] = true
	}
	// Rule 4: column 6 row 14 and column 10 rows 8–14, never row 15. Column 8
	// contributes nothing — its walk stops at the bottom row.
	want[[2]int32{6, 14}] = true
	for z := int32(8); z <= 14; z++ {
		want[[2]int32{10, z}] = true
	}

	for z := int32(0); z < voidStripH; z++ {
		for x := int32(voidStripW - 1); x >= 0; x-- {
			got := terrain.PlotAt(x, z).Feature()
			switch {
			case want[[2]int32{x, z}]:
				if got != world.PlotFeatureVoid {
					t.Errorf("cell (%d,%d) = %#x, want void %#x", x, z, got, world.PlotFeatureVoid)
				}
			case x == 14 && z == 5:
				if got != 1 {
					t.Errorf("live feature at (14,5) = %#x, want index 1 (rule 2 must not convert it)", got)
				}
			default:
				if got != world.PlotFeatureNone {
					t.Errorf("cell (%d,%d) = %#x, want empty %#x", x, z, got, world.PlotFeatureNone)
				}
			}
		}
	}

	// Void writes the feature word and nothing else: the height byte, the
	// derived floor pair and the flag byte's placer nibble all survive the
	// sweep [03 R-TERR-01 §1 "Reader census"]. Cell (10,10) is voided by rule
	// 4; its 2×2 height neighbourhood is 0/160/0/176.
	cell := terrain.PlotAt(10, 10)
	if cell.Height() != 0 || cell.MinHeight() != 0 || cell.MaxHeight() != 176 || cell.FlagByte() != 0x50 {
		t.Fatalf("voided cell (10,10) = height %d, pair %d/%d, flags %#x; want 0, 0/176, 0x50",
			cell.Height(), cell.MinHeight(), cell.MaxHeight(), cell.FlagByte())
	}
}

// TestVoidStripCellsBlockAFootprint is the seam between the sweep and the
// movement classifier: nothing tests the void sentinel by name, and a void cell
// reaches the per-cell chain as a blocking feature, so any footprint whose
// rectangle covers one classifies 0 [03 R-TERR-01 §2 "Who treats void
// specially"][04 §6.1][04 R-SLOPE-01 §3].
//
// The profile is a 2×2 class with slope limits wide enough that the fixture's
// 16-per-row gradient is passable on its own; the only thing that can block
// these anchors is a void cell.
func TestVoidStripCellsBlockAFootprint(t *testing.T) {
	terrain := loadVoidStripFixture(t)
	profile := movement.Profile{
		FootPrintX: 2, FootPrintZ: 2,
		MaxWaterDepth: 0, MinWaterDepth: -10000,
		MaxSlope: 64, BadSlope: 32,
		MaxWaterSlope: 255, BadWaterSlope: 127,
	}
	// Each anchor's 2×2 rectangle reaches one of the four rules' cells.
	for _, anchor := range [][2]int32{
		{1, 0},  // rule 3, column 2 rows 0-1
		{13, 3}, // rule 2, column 14
		{5, 13}, // rule 4, column 6 row 14
		{9, 7},  // rule 4, column 10 row 8
		{11, 4}, // rule 3, column 12 rows 0-6
	} {
		if profile.ClassifyFootprint(terrain, anchor[0], anchor[1]) != movement.ClassBlocked {
			t.Errorf("footprint at (%d,%d) is not blocked, but its rectangle covers a void cell", anchor[0], anchor[1])
		}
	}
	// A rectangle clear of every strip stays passable, so the assertions above
	// are about the void cells and not about the fixture being impassable.
	if !profile.IsPassableFootprint(terrain, 0, 2) {
		t.Fatalf("footprint at (0,2) is blocked; it covers no void cell")
	}
}
