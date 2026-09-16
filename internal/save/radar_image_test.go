package save

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// authoredRadarRaster is a small deterministic indexed picture with a row
// pitch wider than its width, which is the shape a radar surface hands the
// encoder.
func authoredRadarRaster(w, h, pitch int) []byte {
	raster := make([]byte, h*pitch)
	for y := 0; y < h; y++ {
		for x := 0; x < pitch; x++ {
			raster[y*pitch+x] = byte(x*7 + y*13 + 1)
		}
	}
	return raster
}

// TestRadarImageBoxRoundTrip locks the box layout: an 8-byte header of a u32
// width and a u32 height, then height rows of width palette bytes with the
// source pitch removed [08 R-SAVE-02 §3].
func TestRadarImageBoxRoundTrip(t *testing.T) {
	const w, h, pitch = 11, 7, 16
	raster := authoredRadarRaster(w, h, pitch)
	box := EncodeRadarImage(w, h, raster, pitch)
	if len(box) != RadarImageHeaderSize+w*h {
		t.Fatalf("box length %d, want %d", len(box), RadarImageHeaderSize+w*h)
	}
	if got := binary.LittleEndian.Uint32(box[0:]); got != w {
		t.Fatalf("header width %d, want %d", got, w)
	}
	if got := binary.LittleEndian.Uint32(box[4:]); got != h {
		t.Fatalf("header height %d, want %d", got, h)
	}
	gotW, gotH, pixels, ok := DecodeRadarImage(box)
	if !ok || gotW != w || gotH != h {
		t.Fatalf("decode = (%d,%d,%v)", gotW, gotH, ok)
	}
	for y := 0; y < h; y++ {
		if !bytes.Equal(pixels[y*w:(y+1)*w], raster[y*pitch:y*pitch+w]) {
			t.Fatalf("row %d not the source row without its pitch", y)
		}
	}
}

// TestRadarImageShortBoxYieldsNoImage locks the reader rule: a box whose
// declared extent is not fully present is a short read and shows no image
// rather than failing the panel [08 R-SAVE-02 §3].
func TestRadarImageShortBoxYieldsNoImage(t *testing.T) {
	box := EncodeRadarImage(6, 4, authoredRadarRaster(6, 4, 6), 6)
	for _, truncated := range [][]byte{nil, box[:4], box[:RadarImageHeaderSize], box[:len(box)-1]} {
		if _, _, _, ok := DecodeRadarImage(truncated); ok {
			t.Fatalf("short box of %d bytes decoded", len(truncated))
		}
	}
	var zeroExtent [RadarImageHeaderSize]byte
	if _, _, _, ok := DecodeRadarImage(zeroExtent[:]); ok {
		t.Fatal("a zero extent decoded")
	}
	// A declared extent far larger than the payload must not be believed.
	huge := append([]byte(nil), box...)
	binary.LittleEndian.PutUint32(huge[0:], 1<<20)
	if _, _, _, ok := DecodeRadarImage(huge); ok {
		t.Fatal("an over-declared width decoded")
	}
}

// TestEncodeRadarImageRefusesAnIncompleteRaster keeps a caller with no picture
// from writing a box the reader would drop.
func TestEncodeRadarImageRefusesAnIncompleteRaster(t *testing.T) {
	if box := EncodeRadarImage(8, 8, make([]byte, 8*7), 8); box != nil {
		t.Fatalf("encoded %d bytes from a raster missing its last row", len(box))
	}
	if box := EncodeRadarImage(0, 8, make([]byte, 64), 8); box != nil {
		t.Fatal("encoded a zero-width picture")
	}
	if box := EncodeRadarImage(8, 8, make([]byte, 64), 4); box != nil {
		t.Fatal("encoded with a pitch narrower than the width")
	}
}

// TestReadSummaryFileWithBoxesCarriesTheRadarImage proves the two filtered
// readers differ exactly in the box payload: the slot list drops it and the
// selected file's panel read keeps it [08 R-SAVE-02 §1] [08 R-SAVE-02 §3].
func TestReadSummaryFileWithBoxesCarriesTheRadarImage(t *testing.T) {
	const w, h = 63, 126
	box := EncodeRadarImage(w, h, authoredRadarRaster(w, h, w), w)
	b := NewBuilder()
	WriteSummary(b, Summary{Description: "listed", IsBattle: true, Gametype: 2, Players: 2, GameTime: 900, RadarImage: box})
	path := filepath.Join(t.TempDir(), "radar.SAV")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	listed, ok, err := ReadSummaryFile(path)
	if err != nil || !ok {
		t.Fatalf("ReadSummaryFile = (%v,%v)", ok, err)
	}
	if len(listed.RadarImage) != 0 {
		t.Fatalf("the listing read retained %d radar bytes", len(listed.RadarImage))
	}
	selected, ok, err := ReadSummaryFileWithBoxes(path)
	if err != nil || !ok {
		t.Fatalf("ReadSummaryFileWithBoxes = (%v,%v)", ok, err)
	}
	if selected.Description != listed.Description || selected.GameTime != listed.GameTime {
		t.Fatalf("panel read disagrees with the listing read: %+v vs %+v", selected, listed)
	}
	if !bytes.Equal(selected.RadarImage, box) {
		t.Fatalf("panel read returned %d radar bytes, want the written %d", len(selected.RadarImage), len(box))
	}
	gotW, gotH, _, ok := DecodeRadarImage(selected.RadarImage)
	if !ok || gotW != w || gotH != h {
		t.Fatalf("round-tripped extent = (%d,%d,%v)", gotW, gotH, ok)
	}
}

// TestReadSummaryFileWithBoxesOnASaveWithoutOne is the older-save case: no box
// is present, the read still succeeds and the panel has nothing to show.
func TestReadSummaryFileWithBoxesOnASaveWithoutOne(t *testing.T) {
	b := NewBuilder()
	WriteSummary(b, Summary{Description: "no preview", IsBattle: true, Gametype: 2, Players: 2})
	path := filepath.Join(t.TempDir(), "plain.SAV")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, ok, err := ReadSummaryFileWithBoxes(path)
	if err != nil || !ok || summary.Description != "no preview" {
		t.Fatalf("ReadSummaryFileWithBoxes = (%+v,%v,%v)", summary, ok, err)
	}
	if len(summary.RadarImage) != 0 {
		t.Fatalf("a save with no box produced %d radar bytes", len(summary.RadarImage))
	}
	if _, _, _, ok := DecodeRadarImage(summary.RadarImage); ok {
		t.Fatal("an absent box decoded")
	}
}
