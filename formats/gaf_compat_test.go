package formats

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// These authored rows lock the retail command stream, including continuation
// beyond stored row lengths [02 R-MALF-01 §6]. Every case uses both loaders.
func TestGAFCompatibleRLECommands(t *testing.T) {
	for _, tc := range []struct {
		name           string
		width, height  uint16
		stream, pixels []byte
		transparent    []bool
	}{
		{"repeat clamps", 2, 1, []byte{2, 0, 254, 7}, []byte{7, 7}, []bool{false, false}},
		{"skip clamps", 2, 1, []byte{1, 0, 255}, []byte{0, 0}, []bool{true, true}},
		{"literal discards unread suffix", 2, 1, []byte{3, 0, 252, 7, 8}, []byte{7, 8}, []bool{false, false}},
		{"zero skip continues", 2, 1, []byte{3, 0, 1, 6, 9}, []byte{9, 9}, []bool{false, false}},
		{"unused payload ignored", 2, 1, []byte{3, 0, 6, 9, 255}, []byte{9, 9}, []bool{false, false}},
		{"empty row untouched", 2, 1, []byte{0, 0}, []byte{0, 0}, []bool{true, true}},
		// Row 0 reads row 1's length as a repeat-one command and its high byte
		// as opaque palette zero, then borrows the next repeat command. Row 1
		// nevertheless starts at its stored anchor, not after the borrowed bytes.
		{"short row preserves next anchor", 4, 2, []byte{2, 0, 0, 7, 2, 0, 14, 8}, []byte{7, 0, 8, 8, 8, 8, 8, 8}, make([]bool, 8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := gafCompatibilityFixture(tc.width, tc.height, 1, tc.stream)
			meta, err := LoadGAFMetadata(data)
			if err != nil {
				t.Fatal(err)
			}
			full, err := LoadGAF(data)
			if err != nil {
				t.Fatal(err)
			}
			assertGAFMetadataMatches(t, meta, full)
			frame := full.Entries[0].Frames[0].Frame
			if !bytes.Equal(frame.Pixels, tc.pixels) {
				t.Fatalf("pixels %v, want %v", frame.Pixels, tc.pixels)
			}
			for i, want := range tc.transparent {
				if frame.Transparent[i] != want {
					t.Fatalf("pixel %d transparency=%v, want %v", i, frame.Transparent[i], want)
				}
			}
		})
	}
}

func TestGAFEmptyLeavesPreserveGeometryWithoutPixelReads(t *testing.T) {
	for _, size := range [][2]uint16{{0, 4}, {4, 0}, {0, 0}} {
		for _, compressed := range []byte{0, 1} {
			t.Run(fmt.Sprintf("%dx%d-compression%d", size[0], size[1], compressed), func(t *testing.T) {
				data := gafCompatibilityFixture(size[0], size[1], compressed, nil)
				meta, err := LoadGAFMetadata(data)
				if err != nil {
					t.Fatal(err)
				}
				full, err := LoadGAF(data)
				if err != nil {
					t.Fatal(err)
				}
				assertGAFMetadataMatches(t, meta, full)
				frame := full.Entries[0].Frames[0].Frame
				if frame.Width != size[0] || frame.Height != size[1] || len(frame.Pixels) != 0 {
					t.Fatalf("empty geometry: %+v", frame)
				}
				if _, opaque := frame.At(0, 0); opaque {
					t.Fatal("empty leaf has an opaque pixel")
				}
				doubled := frame.Doubled()
				if doubled.Width != size[0]*2 || doubled.Height != size[1]*2 || len(doubled.Pixels) != 0 {
					t.Fatalf("empty doubled geometry: %+v", doubled)
				}
			})
		}
	}
}

func TestGAFEmptyCompositeStillRetainsChildren(t *testing.T) {
	data := gafCompositeChain(2)
	frameOffset := gafFirstFrameOffset(data)
	binary.LittleEndian.PutUint16(data[frameOffset:], 0)
	full, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	frame := full.Entries[0].Frames[0].Frame
	if len(frame.Pixels) != 0 || len(frame.Subframes) == 0 || len(frame.Subframes[0].Pixels) == 0 {
		t.Fatalf("empty parent discarded child: %+v", frame)
	}
	doubled := frame.Doubled()
	if len(doubled.Subframes) != len(frame.Subframes) || len(doubled.Subframes[0].Pixels) == 0 {
		t.Fatal("empty composite variant discarded child")
	}
}

func TestGAFCompatibilityStillRejectsOutOfFileAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		width  uint16
		stream []byte
		want   string
	}{
		{"row header", 2, []byte{1}, "header is truncated"},
		{"stored extent", 2, []byte{5, 0, 6, 9}, "extent is outside file"},
		{"short row reaches EOF", 2, []byte{2, 0, 0, 7}, "command is outside file"},
		{"repeat value", 2, []byte{1, 0, 6}, "repeat byte is outside file"},
		{"used literal bytes", 2, []byte{2, 0, 4, 7}, "literal bytes are outside file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := gafCompatibilityFixture(tc.width, 1, 1, tc.stream)
			assertGAFBothReject(t, data, DefaultGAFLimits(), tc.want)
		})
	}
	for _, compressed := range []byte{0, 1} {
		data := gafCompatibilityFixture(0, 4, compressed, nil)
		offset := gafFirstFrameOffset(data)
		binary.LittleEndian.PutUint32(data[offset+16:], uint32(len(data)+1))
		assertGAFBothReject(t, data, DefaultGAFLimits(), "data pointer is outside file")
	}
}

func TestGAFRLECommandBudgetCountsEveryVisit(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"zero progress", gafCompatibilityFixture(1, 1, 1, []byte{5, 0, 1, 1, 1, 2, 9})},
		{"overlapping rows", gafCompatibilityFixture(4, 2, 1, []byte{2, 0, 0, 7, 2, 0, 14, 8})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := DefaultGAFLimits()
			limits.MaxRLECommands = 3
			assertGAFBothReject(t, tc.data, limits, "aggregate RLE commands exceed limit")
			limits.MaxRLECommands = 4
			if _, err := LoadGAFMetadataWithLimits(tc.data, limits); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadGAFWithLimits(tc.data, limits); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func assertGAFBothReject(t *testing.T, data []byte, limits GAFLimits, want string) {
	t.Helper()
	if _, err := LoadGAFMetadataWithLimits(data, limits); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("metadata error %v, want %s", err, want)
	}
	if _, err := LoadGAFWithLimits(data, limits); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("pixel error %v, want %s", err, want)
	}
}

func gafCompatibilityFixture(width, height uint16, compressed byte, stream []byte) []byte {
	const entry = 16
	const ref = entry + 40
	const frame = ref + 8
	const payload = frame + 24
	data := make([]byte, payload+len(stream))
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[12:], entry)
	binary.LittleEndian.PutUint16(data[entry:], 1)
	copy(data[entry+8:], "authored compatibility")
	binary.LittleEndian.PutUint32(data[ref:], frame)
	binary.LittleEndian.PutUint16(data[frame:], width)
	binary.LittleEndian.PutUint16(data[frame+2:], height)
	data[frame+9] = compressed
	binary.LittleEndian.PutUint32(data[frame+16:], payload)
	copy(data[payload:], stream)
	return data
}
