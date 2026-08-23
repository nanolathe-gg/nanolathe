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
