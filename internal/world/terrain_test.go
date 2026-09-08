package world

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/vfs"
)

// synth builds a Terrain directly from attribute cells, bypassing the VFS.
func synth(t *testing.T, cellW, cellH int32, attrs []formats.TNTAttribute, defs []*content.FeatureDef) *Terrain {
	t.Helper()
	ter := &Terrain{
		CellW:       cellW,
		CellH:       cellH,
		Version:     VersionCanonical,
		Plot:        ExpandPlot(attrs, int(cellW), int(cellH)),
		FeatureDefs: defs,
	}
	ter.BuildLOSHeightWordsForTest()
	ter.stampFeatureAnchors()
	return ter
}

func flat(cellW, cellH int32, height uint8) []formats.TNTAttribute {
	attrs := make([]formats.TNTAttribute, cellW*cellH)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: height, Feature: PlotFeatureNone}
	}
	return attrs
}

// TestFloorPairIsDerived is the core of R6: CoarseHeightAt averages the derived
// floor pair (hmax at plot cell offset 5, hmin at offset 6) [02 "Terrain file"],
// [03 §2.3]. Load used to leave both bytes zero, so the query returned 0 for
// every cell of every map.
func TestFloorPairIsDerived(t *testing.T) {
	attrs := flat(4, 4, 10)
	attrs[1*4+1].Height = 30 // one peak, at cell (1,1)
	ter := synth(t, 4, 4, attrs, nil)

	// Cell (0,0) spans corners (0,0),(1,0),(0,1),(1,1) so it sees the peak.
	if lo, hi := ter.PlotAt(0, 0).MinHeight(), ter.PlotAt(0, 0).MaxHeight(); lo != 10 || hi != 30 {
		t.Fatalf("cell (0,0) floor pair = %d/%d, want 00/30", lo, hi)
	}
	if got := ter.CoarseHeightAt(0, 0); got != 20*65536 {
		t.Fatalf("CoarseHeightAt(0,0) = %d, want %d", got, 20*65536)
	}
	// A cell far from the peak keeps a flat pair.
	if got := ter.CoarseHeightAt(3, 3); got != 10*65536 {
		t.Fatalf("CoarseHeightAt(3,3) = %d, want %d", got, 10*65536)
	}
	// The whole grid must not be zero, which is what the bug looked like.
	if ter.CoarseHeightAt(2, 2) == 0 {
		t.Fatal("coarse height is still zero — the floor pair was not derived")
	}
}

// TestFringeAnchorsResolve is the other half of R6. A multi-cell feature stores
// its index in the anchor and fills covered cells with the fringe sentinel
// [fmt tnt]; consumers reach the index only through the anchor offsets
// [04 §6.2]. Unstamped, every fringe cell resolved to nothing.
func TestFringeAnchorsResolve(t *testing.T) {
	attrs := flat(4, 4, 5)
	// A 3x3 feature (record 0) anchored at cell (1,1) — the shape [fmt tnt]
	// documents for ArchMetal1 on Coast To Coast.
	attrs[1*4+1].Feature = 0
	for dz := 0; dz < 3; dz++ {
		for dx := 0; dx < 3; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			attrs[(1+dz)*4+(1+dx)].Feature = PlotFeatureFringe
		}
	}
	defs := []*content.FeatureDef{{FootprintX: 3, FootprintZ: 3}}
	ter := synth(t, 4, 4, attrs, defs)

	for dz := 0; dz < 3; dz++ {
		for dx := 0; dx < 3; dx++ {
			cx, cz := 1+dx, 1+dz
			got, ok := ResolveFeature(ter.Plot, 4, 4, cx, cz)
			if !ok || got != 0 {
				t.Fatalf("cell (%d,%d) resolved to %d/%v, want feature 0", cx, cz, got, ok)
			}
		}
	}
	// An empty cell still resolves to nothing.
	if _, ok := ResolveFeature(ter.Plot, 4, 4, 0, 0); ok {
		t.Fatal("empty cell resolved to a feature")
	}
}

// TestUnboundFeatureNameIsNotAnIndex locks R9's out-of-range rule: a map naming
// a feature the catalog does not have behaves as out-of-range, not as a load
// failure and not as a valid index [04 §6.2].
func TestUnboundFeatureNameIsNotAnIndex(t *testing.T) {
	ter := synth(t, 2, 2, flat(2, 2, 1), []*content.FeatureDef{nil})
	if _, ok := ter.FeatureDefAt(0); ok {
		t.Fatal("an unbound record resolved to a definition")
	}
	if _, ok := ter.FeatureDefAt(PlotFeatureFringe); ok {
		t.Fatal("a sentinel resolved to a definition")
	}
	if _, ok := ter.FeatureDefAt(99); ok {
		t.Fatal("an out-of-range index resolved to a definition")
	}
}

// TestSurfaceMetalSeeding locks R6's metal half. Before ApplySchema the field is
// unseeded and sampling must refuse rather than quietly return the footprint
// count; after it, the sum is extractsMetal * sum(cellMetal+1)
// [05 "Terrain metal extraction"].
func TestSurfaceMetalSeeding(t *testing.T) {
	ter := synth(t, 4, 4, flat(4, 4, 1), nil)
	if _, err := ter.SampleMetal(0, 0, 3, 3, 1); err == nil {
		t.Fatal("sampling an unseeded metal field was allowed")
	}

	mh := &content.MapHeader{Schemas: []content.MapSchema{{SurfaceMetal: 3}}}
	if err := ter.ApplySchema(mh, 0); err != nil {
		t.Fatal(err)
	}
	got, err := ter.SampleMetal(0, 0, 3, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Nine cells, each contributing metal+1 = 4.
	if got != 36 {
		t.Fatalf("sampled metal = %v, want 36 (9 cells x (3+1))", got)
	}
	// A zero-metal cell still contributes one [05 "Terrain metal extraction"].
	if err := ter.ApplySchema(&content.MapHeader{Schemas: []content.MapSchema{{SurfaceMetal: 0}}}, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ = ter.SampleMetal(0, 0, 3, 3, 1); got != 9 {
		t.Fatalf("zero-metal sample = %v, want 9", got)
	}
}

// TestLOSHeightAggregates locks the polarity of the LOS height word, which the
// builder settles [R-P0-18-B]: the table is seeded low=0x00 /
// high=0xFF and updated with `low = max`, `high = min`, so the LOW byte
// (admission) is the neighbourhood MAXIMUM and the HIGH byte (horizon advance)
// its MINIMUM.
//
// Reading them the other way round — the intuitive (min, max) — is the
// maximally occlusive pairing: admission gets the lowest candidate and the
// horizon the highest retained slope, which scatters false shadows across
// ground retail leaves fully visible.
func TestLOSHeightAggregates(t *testing.T) {
	attrs := flat(8, 8, 10)
	attrs[3*8+3].Height = 90 // a ridge away from any tile origin
	ter := synth(t, 8, 8, attrs, nil)

	// Flat ground away from the ridge: the two bytes converge. They land a
	// little under the authored height because the scattered value is scaled by
	// (tileZ*32+31)/(zs+31) <= 1 and the tail blends the pair by thirds.
	lo, hi := ter.LOSHeightWord(3, 3)
	if lo != hi || lo < 9 || lo > 10 {
		t.Fatalf("flat tile word = (%d,%d), want a converged pair near 10", lo, hi)
	}

	// Somewhere the ridge reaches, the maximum lands in LOW and the minimum in
	// HIGH — never the reverse.
	ridged := false
	for vz := int32(0); vz < 4; vz++ {
		for vx := int32(0); vx < 4; vx++ {
			lo, hi := ter.LOSHeightWord(vx, vz)
			if hi > lo {
				t.Fatalf("tile (%d,%d) word = (%d,%d): high must never exceed low", vx, vz, lo, hi)
			}
			if lo > 10 {
				ridged = true
			}
		}
	}
	if !ridged {
		t.Fatal("the ridge must raise the low byte of some tile")
	}

	// It is a separate query from the bilinear one and must not be substituted.
	if numeric := ter.HeightAt(0, 0); numeric == 90*65536 {
		t.Fatal("HeightAt and LOSHeightWord must not agree on a ridge corner")
	}
}

// TestHeightAtBilinear locks C7: integer bilinear over the four neighbouring
// cell heights with the signed right-shift bias, never a float lerp [03 §2.3].
func TestHeightAtBilinear(t *testing.T) {
	attrs := flat(2, 2, 0)
	attrs[1].Height = 16 // cell (1,0)
	ter := synth(t, 2, 2, attrs, nil)

	// Halfway along x inside cell (0,0): 8 map pixels into a 16-pixel cell.
	half := numeric.Fixed(8 * 65536)
	if got := ter.HeightAt(half, 0); got != 8*65536 {
		t.Fatalf("HeightAt(half cell) = %d, want %d", got, 8*65536)
	}
}

// TestVoidEdgeRules locks the conversion gate the four engine-derived rules of
// [03 R-TERR-01 §2] share: only an empty or fringe cell is converted, so a live
// feature standing in a strip survives it. Which rows each walk reaches, and
// which cell it writes, is locked cell by cell against an authored map in
// void_strip_test.go.
//
// Row expectations corrected by WU-19-48. On this 12x12 map at height 10 the
// north walk voids row 0 and stops at row 1 (16 - 5 is not negative), and the
// south walk runs from row 11 up to row 5 (PlayBottom is 64 and 5*16 - 5 = 75
// is the last value above it), voiding the row ABOVE each tested row — rows 4
// through 10, the bottom row not among them.
func TestVoidEdgeRules(t *testing.T) {
	// 12x12 flat map at height 10; put a live feature in the rightmost
	// column and one on the north edge so their survival is asserted.
	attrs := flat(12, 12, 10)
	// Feature index 1 at (10, 5): right column W-2.
	attrs[5*12+10] = formats.TNTAttribute{Height: 10, Feature: 1}
	// Feature index 2 at (3, 0): north edge, would otherwise void.
	attrs[0*12+3] = formats.TNTAttribute{Height: 10, Feature: 2}
	ter := synth(t, 12, 12, attrs, nil)
	ter.applyVoidFixup(nil)

	// Right columns: empty cells voided, the placed feature survives.
	if got := ter.PlotAt(11, 5).Feature(); got != PlotFeatureVoid {
		t.Fatalf("right column empty cell = %#x, want %#x", got, PlotFeatureVoid)
	}
	if got := ter.PlotAt(10, 5).Feature(); got != 1 {
		t.Fatalf("right column feature = %d, want 0 (must survive)", got)
	}
	// North strip: height 10 at row 0 has z*16=0 < 10>>1=5, so voided;
	// the feature at (3,0) survives.
	if got := ter.PlotAt(5, 0).Feature(); got != PlotFeatureVoid {
		t.Fatalf("north strip cell = %#x, want %#x", got, PlotFeatureVoid)
	}
	if got := ter.PlotAt(3, 0).Feature(); got != 2 {
		t.Fatalf("north edge feature = %d, want 2 (must survive)", got)
	}
	// Rows 1..3 are reached by neither walk on this map.
	if got := ter.PlotAt(5, 3).Feature(); got != PlotFeatureNone {
		t.Fatalf("mid row cell = %#x, want %#x", got, PlotFeatureNone)
	}
	// South strip: the deepest row it voids here is 10, the row above the
	// bottom row it tests first.
	if got := ter.PlotAt(5, 10).Feature(); got != PlotFeatureVoid {
		t.Fatalf("south strip cell = %#x, want %#x", got, PlotFeatureVoid)
	}
	// ... and the bottom row itself is never voided by the south rule.
	if got := ter.PlotAt(5, 11).Feature(); got != PlotFeatureNone {
		t.Fatalf("bottom row cell = %#x, want %#x (rule 4 voids the row above)", got, PlotFeatureNone)
	}
	// A tall bottom row (240, half 120) stops the walk at once: 11*16 - 120 =
	// 56 is at or below PlayBottom 64, so that column loses no row at all —
	// including row 10, which the flat map above did lose.
	attrs2 := flat(12, 12, 10)
	attrs2[11*12+5] = formats.TNTAttribute{Height: 240, Feature: PlotFeatureNone}
	ter2 := synth(t, 12, 12, attrs2, nil)
	ter2.applyVoidFixup(nil)
	for _, cz := range []int32{10, 11} {
		if got := ter2.PlotAt(5, cz).Feature(); got != PlotFeatureNone {
			t.Fatalf("tall south column cell (5,%d) = %#x, want %#x", cz, got, PlotFeatureNone)
		}
	}
}

// TestVoidLavaFlood locks the lavaworld bulk flood: empty/fringe cells with
// hmin <= SeaLevel become void; placed features survive [02 "Terrain file"].
func TestVoidLavaFlood(t *testing.T) {
	attrs := flat(8, 8, 0)
	attrs[4*8+4] = formats.TNTAttribute{Height: 0, Feature: 3} // basin feature
	ter := synth(t, 8, 8, attrs, nil)
	ter.SeaLevel = 10
	ter.applyVoidFixup(&content.MapHeader{LavaWorld: 1})
	if got := ter.PlotAt(2, 2).Feature(); got != PlotFeatureVoid {
		t.Fatalf("basin cell = %#x, want %#x", got, PlotFeatureVoid)
	}
	if got := ter.PlotAt(4, 4).Feature(); got != 3 {
		t.Fatalf("basin feature = %d, want 3 (must survive)", got)
	}
}

// legacyTNTBytes builds a structurally valid legacy (0x1020) TNT file:
// 16x16 cells at height 10, two live features, per-cell metal seeds in the
// 8-byte attribute records (byte 6), and header wind/gravity in slots
// 10/11/13 [02 "Terrain file"].
func legacyTNTBytes(t *testing.T) []byte {
	t.Helper()
	le := binary.LittleEndian
	tileMap := 64 * 2    // (16/2)*(16/2) uint16 tile indices
	attrs := 16 * 16 * 8 // 8-byte legacy records
	gfx := 1 * 1024
	feats := 2 * 132
	offAttr := 0x40 + tileMap
	offGfx := offAttr + attrs
	offFeat := offGfx + gfx
	offMini := offFeat + feats
	b := make([]byte, offMini+8)
	le.PutUint32(b[0x00:], 0x1020) // version
	le.PutUint32(b[0x04:], 16)     // width
	le.PutUint32(b[0x08:], 16)     // height
	le.PutUint32(b[0x0c:], 0x40)   // tile map
	le.PutUint32(b[0x10:], uint32(offAttr))
	le.PutUint32(b[0x14:], uint32(offGfx))
	le.PutUint32(b[0x18:], 1) // tile count
	le.PutUint32(b[0x1c:], 2) // feature records
	le.PutUint32(b[0x20:], uint32(offFeat))
	le.PutUint32(b[0x24:], 0)               // sea level
	le.PutUint32(b[0x28:], 500)             // slot 10: min wind
	le.PutUint32(b[0x2c:], 800)             // slot 11: max wind
	le.PutUint32(b[0x34:], 112)             // slot 13: gravity
	le.PutUint32(b[0x38:], uint32(offMini)) // slot 14: minimap
	le.PutUint32(b[0x3c:], 0)               // slot 15: minimap flag
	for i := 0; i < 16*16; i++ {
		rec := b[offAttr+i*8 : offAttr+i*8+8]
		rec[0] = 10 // height
		rec[6] = byte(1 + i%9)
		// Feature: index 1 at (14,8), index 2 at (3,0); band elsewhere.
		if i == 8*16+14 {
			rec[2] = 0
		} else if i == 0*16+3 {
			rec[2] = 1
		} else {
			rec[2] = 0xFF
		}
	}
	for n := 0; n < 2; n++ {
		feat := b[offFeat+n*132 : offFeat+n*132+132]
		le.PutUint32(feat, uint32(n))
		copy(feat[4:], []byte("tree"+string(rune('a'+n))))
	}
	return b
}

// TestLegacyTerrainLoads locks the world side of the legacy (0x1020) path:
// header wind/gravity always win, per-cell metal comes from attribute byte 6,
// and ApplySchema must not overwrite it [02 "Terrain file"]. Void fixup still
// derives the engine edges.
func TestLegacyTerrainLoads(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "maps", "legacy.tnt"), legacyTNTBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 0); err != nil {
		t.Fatalf("mount: %v", err)
	}
	ter, err := Load(fs, nil, "legacy")
	if err != nil {
		t.Fatalf("legacy Load: %v", err)
	}
	if ter.Version != VersionLegacy {
		t.Fatalf("version = %v, want legacy", ter.Version)
	}
	if got := ter.LOSHeightBuildCount(); got != 1 {
		t.Fatalf("LOS height build count = %d, want 1 immediately after load", got)
	}
	if ter.WindMin != 500 || ter.WindMax != 800 {
		t.Fatalf("wind = %d/%d, want 500/800 (header values, no OTA)", ter.WindMin, ter.WindMax)
	}
	if want := numeric.Fixed(112 * 65536 / 900); ter.Gravity != want {
		t.Fatalf("gravity = %d, want %d (header gravity via *65536/900)", ter.Gravity, want)
	}
	// Per-cell metal from attribute byte 6; interior cell (5,5) stays clean
	// (rows 1..7 clear of both strips — the south walk on this flat 16-row map
	// reaches row 8 — and columns 0..13 clear of the right edge).
	if got := ter.PlotAt(5, 5).Metal(); got != byte(1+(5*16+5)%9) {
		t.Fatalf("legacy per-cell metal at (5,5) = %d", got)
	}
	if got := ter.PlotAt(5, 5).Feature(); got != PlotFeatureNone {
		t.Fatalf("interior cell = %#x, want empty", got)
	}
	// Engine edges still derive: right column, north strip, south strip.
	if got := ter.PlotAt(14, 5).Feature(); got != PlotFeatureVoid {
		t.Fatalf("right column = %#x, want void", got)
	}
	if got := ter.PlotAt(5, 0).Feature(); got != PlotFeatureVoid {
		t.Fatalf("north strip = %#x, want void", got)
	}
	if got := ter.PlotAt(5, 12).Feature(); got != PlotFeatureVoid {
		t.Fatalf("south strip = %#x, want void", got)
	}
	// Placed features survive the edges: (14,8) in the right column, (3,0) in
	// the north strip.
	if got := ter.PlotAt(14, 8).Feature(); got != 0 {
		t.Fatalf("right-column feature = %d, want 0", got)
	}
	if got := ter.PlotAt(3, 0).Feature(); got != 1 {
		t.Fatalf("north-strip feature = %d, want 1", got)
	}
	// ApplySchema must not overwrite the legacy per-cell seeds; the uniform
	// SurfaceMetal write is canonical-only [02 "Terrain file"].
	before := ter.PlotAt(5, 5).Metal()
	if err := ter.ApplySchema(&content.MapHeader{Schemas: []content.MapSchema{{SurfaceMetal: 42}}}, 0); err != nil {
		t.Fatalf("ApplySchema: %v", err)
	}
	if got := ter.PlotAt(5, 5).Metal(); got != before {
		t.Fatalf("ApplySchema overwrote legacy per-cell metal %d -> %d", before, got)
	}
}

// TestCanonicalGlobalsTreatOmissionAsTheParserDefault locks [03 §2.2] C3/C4 as
// its 2026-08-29 correction against [02 R-MAP-01] states it: an OMITTED OTA key
// is not "unparsed". Once a `[GlobalHeader]` has been parsed, the OTA parser
// stores the key's own default — integer 0, float 0.0 — and that default passes
// the `>= 0` test like any authored value. The 100/2000 wind pair, the 0x1FDB
// gravity and the 0.5 tidal stand only for a NEGATIVE authored value or for no
// parsed `[GlobalHeader]` at all.
//
// The relationship, not a census: an omission and a negative must NOT resolve
// alike, and the absent case must agree with an explicit authored zero.
func TestCanonicalGlobalsTreatOmissionAsTheParserDefault(t *testing.T) {
	parsed := func(mutate func(*content.MapHeader)) *content.MapHeader {
		mh := &content.MapHeader{RawOTA: &formats.OTA{Global: &formats.Section{}}}
		if mutate != nil {
			mutate(mh)
		}
		return mh
	}

	// Omitted keys: content.MapHeader compiles each to the parser's default 0.
	omitted := parsed(nil)
	wMin, wMax, grav, authored := canonicalWindAndGravity(omitted)
	if wMin != 0 || wMax != 0 {
		t.Fatalf("omitted wind = %d/%d, want 0/0 (parser default passes >= 0) [03 §2.2 C3]", wMin, wMax)
	}
	if grav != 0 || authored != 0 {
		t.Fatalf("omitted gravity = %d (authored %d), want 0/0 [03 §2.2 C4]", grav, authored)
	}
	if got := canonicalTidal(omitted); got != 0 {
		t.Fatalf("omitted tidalstrength = %g, want 0 [03 §2.2 C4]", got)
	}

	// A negative authored value is the case the fallbacks are for, and it must
	// differ from the omission above.
	negative := parsed(func(mh *content.MapHeader) {
		mh.MinWindSpeed, mh.MaxWindSpeed, mh.Gravity, mh.TidalStrength = -1, -1, -1, -1
	})
	wMin, wMax, grav, authored = canonicalWindAndGravity(negative)
	if wMin != 100 || wMax != 2000 {
		t.Fatalf("negative wind = %d/%d, want 100/2000 [03 §2.2 C3]", wMin, wMax)
	}
	if grav != numeric.Fixed(0x1FDB) || authored != 112 {
		t.Fatalf("negative gravity = %d (authored %d), want 0x1FDB/112 [03 §2.2 C4]", grav, authored)
	}
	if got := canonicalTidal(negative); got != 0.5 {
		t.Fatalf("negative tidalstrength = %g, want 0.5 [03 §2.2 C4]", got)
	}

	// No `[GlobalHeader]` parsed at all takes the same fallbacks as a negative.
	wMin, wMax, grav, authored = canonicalWindAndGravity(nil)
	if wMin != 100 || wMax != 2000 || grav != numeric.Fixed(0x1FDB) || authored != 112 {
		t.Fatalf("unparsed header = %d/%d g=%d a=%d, want 100/2000/0x1FDB/112 [03 §2.2]", wMin, wMax, grav, authored)
	}
	if got := canonicalTidal(nil); got != 0.5 {
		t.Fatalf("unparsed tidalstrength = %g, want 0.5 [03 §2.2 C4]", got)
	}

	// A stock authored value still converts by *65536/900 [fmt ota].
	stock := parsed(func(mh *content.MapHeader) { mh.Gravity = 112 })
	if _, _, g, a := canonicalWindAndGravity(stock); g != numeric.Fixed(112*65536/900) || a != 112 {
		t.Fatalf("authored 112 gave %d (authored %d)", g, a)
	}
}

// Tidal strength is a single-precision scalar, not a fixed-point position
// [03 R-TERR-01 §6][05 R-PROD-01 §4].
func TestTidalScalarPreservesFractionAndFloatStore(t *testing.T) {
	for _, tt := range []struct {
		source float64
		bits   uint32
	}{
		{0.000001, 0x358637bd},
		{0.1, 0x3dcccccd},
		{-1e-50, 0x80000000}, // source rounds to negative zero before the strict test
		{-1, 0x3f000000},
	} {
		h := &content.MapHeader{RawOTA: &formats.OTA{Global: &formats.Section{}}, TidalStrength: tt.source}
		if got := math.Float32bits(canonicalTidal(h)); got != tt.bits {
			t.Fatalf("tidal %g=%08x, want %08x", tt.source, got, tt.bits)
		}
	}
}
