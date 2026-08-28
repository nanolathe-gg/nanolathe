package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/palette"
)

func TestIndexedPixelsUseSharedPaletteDirectly(t *testing.T) {
	var tables palette.Tables
	tables.Base[0] = [4]byte{10, 10, 10, 0}
	tables.Base[1] = [4]byte{20, 20, 20, 0}
	tables.Base[2] = [4]byte{30, 30, 30, 0}
	tables.Base[7] = [4]byte{70, 70, 70, 0}
	tables.GUI[7] = tables.Base[1] // differs deliberately: GUIColor fields use this map.
	for i := 3; i < 256; i++ {
		if i != 7 {
			tables.Base[i] = [4]byte{255, 255, 255, 0}
		}
		tables.GUI[i] = tables.Base[i]
	}
	tables.GUI[7] = tables.Base[1]
	for i := range tables.Logical {
		tables.Logical[i] = byte(i)
	}

	c, err := New(Options{Width: 2, Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	c.SetPalette(&tables)
	c.indexed[0] = 7                  // GAF/PCX/TNT/FNT byte; must remain PALETTE index 7.
	c.indexed[1] = tables.GUIColor(7) // GUI semantic field is resolved before writing.
	c.convertIndexedToRGBA()

	if got := c.rgba[0]; got != 70 {
		t.Fatalf("indexed image red=%d, want direct PALETTE index 7 result 70", got)
	}
	if got := c.rgba[4]; got != 20 {
		t.Fatalf("GUI semantic pixel red=%d, want mapped PALETTE index 1 result 20", got)
	}
}
