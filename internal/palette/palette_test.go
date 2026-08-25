package palette

import "testing"

func TestGUIToBaseUsesSharedPaletteAndFirstTie(t *testing.T) {
	var tables Tables
	tables.Base[0] = [4]byte{0, 0, 0, 255}
	tables.Base[1] = [4]byte{2, 0, 0, 255}
	tables.GUI[0] = [4]byte{1, 0, 0, 255}
	tables.GUI[1] = [4]byte{2, 0, 0, 255}

	remap := tables.GUIToBase()
	if remap[0] != 0 {
		t.Fatalf("tie chose palette index %d, want first index 0", remap[0])
	}
	if remap[1] != 1 {
		t.Fatalf("exact GUI color mapped to %d, want 1", remap[1])
	}
}
