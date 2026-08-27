package formats

import (
	"encoding/binary"
	"strings"
	"testing"
)

// tntHeader builds a 0x40-byte TNT header with the given version word. The
// section pointers are left zero; these fixtures only exercise version gating,
// which happens before any section is read.
func tntHeader(version uint32) []byte {
	b := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(b[0x00:], version)
	binary.LittleEndian.PutUint32(b[0x04:], 4) // width, even
	binary.LittleEndian.PutUint32(b[0x08:], 4) // height, even
	return b
}

// TestUnknownVersionRejected: any other version word is a diagnostic and a
// resource-failure path [03 §2.2].
func TestUnknownVersionRejected(t *testing.T) {
	_, err := LoadTNT(tntHeader(0x3000))
	if err == nil {
		t.Fatal("unknown version was accepted")
	}
	if !strings.Contains(err.Error(), "0x3000") {
		t.Fatalf("diagnostic %q does not name the version word", err)
	}
}

// legacyFixture builds a minimal but structurally valid legacy (0x1020) TNT
// file: 2x2 cells, one tile, one feature record, no minimap. The header slots
// carry the legacy meanings (slot 10 min wind, 11 max wind, 13 gravity, 14
// minimap offset) and the attribute array uses the 8-byte record.
func legacyFixture(t *testing.T) []byte {
	t.Helper()
	attr := make([]byte, 4*8)
	// Cell 0: height 5, feature 0xFF (sentinel band -> empty), metal 7.
	attr[0], attr[2], attr[6] = 5, 0xFF, 7
	// Cell 1: height 6, feature 3 (live index), metal 8.
	attr[8], attr[10], attr[14] = 6, 0, 8
	// Cells 2, 3: empty, height 0, metal 0.
	var b []byte
	b = append(b, tntHeader(uint32(VersionLegacy))...)
	binary.LittleEndian.PutUint32(b[0x04:], 2)                  // width
	binary.LittleEndian.PutUint32(b[0x08:], 2)                  // height
	binary.LittleEndian.PutUint32(b[0x0c:], 0x40)               // tile map
	binary.LittleEndian.PutUint32(b[0x10:], 0x40+2)             // attribute array
	binary.LittleEndian.PutUint32(b[0x14:], 0x40+2+32)          // tile graphics
	binary.LittleEndian.PutUint32(b[0x18:], 1)                  // tile count
	binary.LittleEndian.PutUint32(b[0x1c:], 1)                  // feature records
	binary.LittleEndian.PutUint32(b[0x20:], 0x40+2+32+1024)     // feature table
	binary.LittleEndian.PutUint32(b[0x24:], 0)                  // sea level
	binary.LittleEndian.PutUint32(b[0x28:], 500)                // slot 10: min wind
	binary.LittleEndian.PutUint32(b[0x2c:], 800)                // slot 11: max wind
	binary.LittleEndian.PutUint32(b[0x34:], 112)                // slot 13: gravity
	binary.LittleEndian.PutUint32(b[0x38:], 0x40+2+32+1024+132) // slot 14: minimap offset
	binary.LittleEndian.PutUint32(b[0x3c:], 0)                  // slot 15: minimap flag
	b = append(b, 0, 0)                                         // one tile index
	b = append(b, attr...)                                      // 4 x 8-byte attribute records
	b = append(b, make([]byte, 1024)...)                        // one tile
	feat := make([]byte, 132)
	binary.LittleEndian.PutUint32(feat, 0)
	copy(feat[4:], "tree")
	b = append(b, feat...)
	b = append(b, make([]byte, 8)...) // minimap header (width/height, zeroed)
	return b
}

// TestLegacyTNTLoads locks the legacy (0x1020) path: the 8-byte attribute
// record is parsed (byte 0 height, byte 2 one-byte feature reference with the
// 0xFC+ sentinel band, byte 6 per-cell metal), the header wind/gravity slots
// are read, and the minimap offset comes from slot 14 [02 "Terrain file"].
func TestLegacyTNTLoads(t *testing.T) {
	tnt, err := LoadTNT(legacyFixture(t))
	if err != nil {
		t.Fatalf("legacy TNT rejected: %v", err)
	}
	if tnt.Version != uint32(VersionLegacy) {
		t.Fatalf("version = %#x", tnt.Version)
	}
	if tnt.LegacyMinWind != 500 || tnt.LegacyMaxWind != 800 || tnt.LegacyGravity != 112 {
		t.Fatalf("legacy header wind/gravity = %d/%d/%d, want 500/800/112",
			tnt.LegacyMinWind, tnt.LegacyMaxWind, tnt.LegacyGravity)
	}
	if tnt.MiniMapOffset != 0x40+2+32+1024+132 {
		t.Fatalf("legacy minimap offset from slot 14 = %#x", tnt.MiniMapOffset)
	}
	if len(tnt.Attributes) != 4 {
		t.Fatalf("attributes = %d cells", len(tnt.Attributes))
	}
	a0, a1 := tnt.Attributes[0], tnt.Attributes[1]
	if a0.Height != 5 || a0.Feature != 0xFFFF || a0.Metal != 7 {
		t.Fatalf("cell 0 = h%d f%#x m%d, want h5 f0xffff m7", a0.Height, a0.Feature, a0.Metal)
	}
	if a1.Height != 6 || a1.Feature != 0 || a1.Metal != 8 {
		t.Fatalf("cell 1 = h%d f%d m%d, want h6 f0 m8", a1.Height, a1.Feature, a1.Metal)
	}
	if tnt.FeatureTable[0].Name != "tree" {
		t.Fatalf("feature table name = %q", tnt.FeatureTable[0].Name)
	}
}

// TestLegacyMinimapSlotIsNotSlotTen is the header half of R7. On a legacy file
// slot 10 is the minimum wind speed and the minimap offset moves to slot 14
// [03 §2.2]. Reading slot 10 as an offset would seek to the wind value.
func TestLegacyMinimapSlotIsNotSlotTen(t *testing.T) {
	if VersionLegacy == VersionCanonical {
		t.Fatal("version words must differ")
	}
	tnt, err := LoadTNT(legacyFixture(t))
	if err != nil {
		t.Fatalf("legacy TNT rejected: %v", err)
	}
	if tnt.MiniMapOffset == 500 {
		t.Fatalf("minimap offset read slot 10 (wind) instead of slot 14: %#x", tnt.MiniMapOffset)
	}
}

// TestFeatureSentinelBand locks the accepted sentinel range. Values at or above
// 0xFFFB are sentinels, not indices: consumers test below 0xFFFB before
// dereferencing, so 0xFFFB and 0xFFFC act as further void thresholds
// [02 "Terrain file"], [GAP T14]. The retail corpus writes 0xFFFC for void on
// disk [fmt tnt].
func TestFeatureSentinelBand(t *testing.T) {
	for _, sentinel := range []uint16{0xFFFF, 0xFFFE, 0xFFFD, 0xFFFC, 0xFFFB} {
		if sentinel < featureSentinelBase {
			t.Fatalf("%#x must be inside the sentinel band", sentinel)
		}
	}
	if featureSentinelBase-1 >= featureSentinelBase {
		t.Fatal("0xFFFA must be a real index")
	}
}
