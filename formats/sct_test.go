package formats

import (
	"encoding/binary"
	"testing"
)

func TestLoadSCTRejectsInvalidBlockLayout(t *testing.T) {
	for _, test := range []struct {
		name                    string
		tileGraphics, tileIndex uint32
	}{
		{name: "header overlap", tileGraphics: 0, tileIndex: 1052},
		{name: "known block overlap", tileGraphics: 28, tileIndex: 28},
	} {
		t.Run(test.name, func(t *testing.T) {
			const previewOffset = 1054
			data := make([]byte, previewOffset+128*128)
			put := func(offset int, value uint32) { binary.LittleEndian.PutUint32(data[offset:], value) }
			put(0, 3)
			put(4, previewOffset)
			put(8, 1)
			put(12, test.tileGraphics)
			put(16, 1)
			put(20, 1)
			put(24, test.tileIndex)
			if _, err := LoadSCT(data); err == nil {
				t.Fatal("LoadSCT accepted an invalid block layout")
			}
		})
	}
}

// authoredSCT uses a nonsquare grid to expose row-stride mistakes. The block
// order varies independently of the height grid, whose position follows the
// tile indices rather than the preview or graphics pointer [fmt tnt].
func authoredSCT(version uint32, previewFirst bool) []byte {
	stride := 4
	if version == 2 {
		stride = 8
	}
	const header, cells = 28, 2
	heightStart := header + cells*2
	heightEnd := heightStart + cells*4*stride
	graphics, preview := heightEnd+7, heightEnd+7+1024
	if previewFirst {
		preview, graphics = heightEnd+7, heightEnd+7+128*128
	}
	data := make([]byte, heightEnd+7+1024+128*128)
	for i, v := range []uint32{version, uint32(preview), 1, uint32(graphics), 2, 1, header} {
		binary.LittleEndian.PutUint32(data[i*4:], v)
	}
	for i := 0; i < cells*4; i++ {
		for b := 0; b < stride; b++ {
			data[heightStart+i*stride+b] = 0xa5
		}
		data[heightStart+i*stride] = byte(i + 1)
	}
	return data
}

func TestSCTHeightGridAndRetainedRecords(t *testing.T) {
	for _, version := range []uint32{2, 3} {
		for _, previewFirst := range []bool{false, true} {
			data := authoredSCT(version, previewFirst)
			section, err := LoadSCT(data)
			if err != nil {
				t.Fatal(err)
			}
			stride := 4
			if version == 2 {
				stride = 8
			}
			if len(section.Heights) != int(4*section.Width*section.Height) || len(section.AttributeData) != len(section.Heights)*stride {
				t.Fatalf("version %d: fine-grid/record lengths = %d/%d", version, len(section.Heights), len(section.AttributeData))
			}
			for i, height := range section.Heights {
				if height != byte(i+1) || section.AttributeData[i*stride+1] != 0xa5 {
					t.Fatalf("version %d: height record %d = %d / %x", version, i, height, section.AttributeData[i*stride:(i+1)*stride])
				}
			}
			data[32] = 99
			if section.Heights[0] != 1 || section.AttributeData[0] != 1 || section.Raw[32] != 1 {
				t.Fatal("SCT retained a caller-owned input slice")
			}
		}
	}
}

func TestSCTRejectsIncompleteOrOverlappingHeightRecords(t *testing.T) {
	for _, version := range []uint32{2, 3} {
		data := authoredSCT(version, false)
		// Move the valid tile grid to the last four bytes. Every other
		// declared block still fits, but its required height records do not.
		binary.LittleEndian.PutUint32(data[24:], uint32(len(data)-4))
		if _, err := LoadSCT(data); err == nil {
			t.Fatalf("version %d: accepted missing height records", version)
		}
		data = authoredSCT(version, false)
		binary.LittleEndian.PutUint32(data[12:], 33)
		if _, err := LoadSCT(data); err == nil {
			t.Fatalf("version %d: accepted graphics overlapping height records", version)
		}
	}
}
