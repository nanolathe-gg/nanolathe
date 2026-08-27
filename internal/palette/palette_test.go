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

func TestTablesUseLogicalMapOnlyAtIndexedLookup(t *testing.T) {
	var tables Tables
	tables.Base[3] = [4]byte{10, 20, 30, 255}
	tables.Base[4] = [4]byte{40, 50, 60, 255}
	tables.Logical[7] = 3
	tables.Light[2*256+3] = 4
	tables.Shade[5][3] = 4
	if r, g, b, a := tables.RGBA(7); [4]byte{r, g, b, a} != [4]byte{10, 20, 30, 255} {
		t.Fatalf("logical RGBA = %#v", [4]byte{r, g, b, a})
	}
	if got := tables.LightLookup(2, 7); got != 4 {
		t.Fatalf("LHT lookup = %d, want 4", got)
	}
	if got := tables.ShadeLookup(5, 7); got != 4 {
		t.Fatalf("SHD lookup = %d, want 4", got)
	}
	// GUI semantic remapping never changes the indexed image lookup.
	tables.GUI[7] = [4]byte{40, 50, 60, 255}
	if got, _, _, _ := tables.RGBA(7); got != 10 {
		t.Fatalf("GUI palette changed image index to %d", got)
	}
}

func TestGrayTableUsesSumWindowAndStableTie(t *testing.T) {
	var tables Tables
	for i := range tables.Base {
		v := byte(i)
		tables.Base[i] = [4]byte{v, v, v, 255}
	}
	// The source itself is always an exact candidate when the palette entry is
	// already gray.
	tables.Base[100] = [4]byte{99, 99, 99, 255}
	tables.Base[101] = [4]byte{101, 101, 101, 255}
	buildGrayTable(&tables)
	if got := tables.Gray[102]; got != 102 {
		t.Fatalf("gray exact entry = %d, want 102", got)
	}
	if got := tables.Gray[101]; got != 101 {
		t.Fatalf("gray exact entry 101 = %d, want 101", got)
	}
}
