package formats

import (
	"bytes"
	"testing"
)

func TestPALRetainsReservedBytesAndImportLayout(t *testing.T) {
	for _, stride := range []int{3, 4} {
		source := make([]byte, stride*256)
		for i := range source {
			source[i] = byte(i)
		}
		pal, err := LoadPAL(source)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pal.Raw, source) {
			t.Fatal("lossless palette lost authored bytes")
		}
		source[0] = 99
		if pal.Raw[0] == 99 || pal.Colors[0].R != 0 || pal.Colors[0].A != 255 {
			t.Fatal("palette source ownership or opaque resolved color changed")
		}
	}
}
