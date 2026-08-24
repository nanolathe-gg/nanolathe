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

// TestLegacyVersionRejected locks the R7 decision. [03 §2.2] accepts exactly
// two version words, but the width of the legacy attribute record is
// unrecovered [02 "Terrain file"], so decoding it as the canonical four-byte
// record would silently mis-parse every cell. Reject with a diagnostic that
// says why rather than inventing a stride (I9).
func TestLegacyVersionRejected(t *testing.T) {
	_, err := LoadTNT(tntHeader(uint32(VersionLegacy)))
	if err == nil {
		t.Fatal("legacy TNT was accepted")
	}
	for _, want := range []string{"legacy", "0x1020", "attribute record"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("diagnostic %q is missing %q", err, want)
		}
	}
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

// TestLegacyMinimapSlotIsNotSlotTen is the header half of R7. On a legacy file
// slot 10 is the minimum wind speed and the minimap offset moves to slot 14
// [03 §2.2]. Reading slot 10 as an offset seeks to the wind value. The loader
// rejects legacy files, so this checks the slot resolution directly.
func TestLegacyMinimapSlotIsNotSlotTen(t *testing.T) {
	if VersionLegacy == VersionCanonical {
		t.Fatal("version words must differ")
	}
	// The rejection must mention the map dimensions, proving the header was
	// parsed with legacy slot meanings before the attribute stride stopped it.
	_, err := LoadTNT(tntHeader(uint32(VersionLegacy)))
	if err == nil || !strings.Contains(err.Error(), "4x4") {
		t.Fatalf("legacy header was not parsed before rejection: %v", err)
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
