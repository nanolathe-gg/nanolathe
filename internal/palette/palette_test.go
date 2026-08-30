package palette

import "testing"

func TestGUIToBaseUsesSharedPaletteAndFirstTie(t *testing.T) {
	var tables Tables
	tables.Base[0] = [4]byte{0, 0, 0, 255}
	tables.Base[1] = [4]byte{2, 0, 0, 255}
	tables.GUI[0] = [4]byte{1, 0, 0, 255}
	tables.GUI[1] = [4]byte{2, 0, 0, 255}
	tables.BuildLogicalMap()

	remap := tables.GUIToBase()
	if remap[0] != 0 {
		t.Fatalf("tie chose palette index %d, want first index 0", remap[0])
	}
	if remap[1] != 1 {
		t.Fatalf("exact GUI color mapped to %d, want 1", remap[1])
	}
}

// TestLogicalMapResolvesReservedGUIEntries locks the retail relationship the
// logical→physical map exists for. PALETTE.PAL entries 10..15 are the zeroed
// Windows-reserved slots, so a logical entry read straight out of PALETTE.PAL
// is black; the map instead sends it to the display entry nearest the authored
// GUIPAL.PAL colour under the sum of absolute per-channel differences, the
// lowest destination index winning a tie [03 §4.3][07 "Retail palette
// contract"].
//
// Entry 10 is the one the selected-unit footprint quad resolves
// [03 R-WATER-01 §1] and the one the HUD health primitive's dcb[10] threshold
// reads [07 §6]; retail's GUIPAL.PAL holds (85,255,85) there.
func TestLogicalMapResolvesReservedGUIEntries(t *testing.T) {
	var tables Tables
	// A display palette shaped like retail's: a zeroed reserved block at
	// 10..15, real colours elsewhere.
	for i := range tables.Base {
		tables.Base[i] = [4]byte{0, 0, 0, 255}
	}
	tables.Base[200] = [4]byte{85, 254, 85, 255} // 0+1+0 = 1 from the GUI green
	tables.Base[201] = [4]byte{88, 255, 85, 255} // 3
	tables.Base[202] = [4]byte{82, 255, 85, 255} // 3, later index
	tables.GUI[10] = [4]byte{85, 255, 85, 255}   // retail's GUIPAL.PAL green
	tables.GUI[11] = [4]byte{85, 255, 85, 255}   // same colour, must agree
	tables.BuildLogicalMap()

	// Brute-force the stated metric independently of the builder.
	want, best := 0, 1<<20
	for dst := 0; dst < 256; dst++ {
		d := absPaletteDistance(85, tables.Base[dst][0]) +
			absPaletteDistance(255, tables.Base[dst][1]) +
			absPaletteDistance(85, tables.Base[dst][2])
		if d < best {
			best, want = d, dst
		}
	}
	if want != 200 {
		t.Fatalf("sum of absolute channel differences picked %d, want 200", want)
	}
	if int(tables.Logical[10]) != want {
		t.Fatalf("logical entry 10 mapped to %d, want nearest display entry %d [03 §4.3]", tables.Logical[10], want)
	}
	if tables.Logical[11] != tables.Logical[10] {
		t.Fatalf("identical GUI colours mapped to %d and %d", tables.Logical[10], tables.Logical[11])
	}
	// Remove the winner so 201 and 202 tie at distance 3: the lower index wins.
	tables.Base[200] = [4]byte{0, 0, 0, 255}
	tables.BuildLogicalMap()
	if tables.Logical[10] != 201 {
		t.Fatalf("tie kept %d, want the lowest destination index 201 [03 §4.3]", tables.Logical[10])
	}
	// Reading the logical entry as if it were physical is the defect the map
	// exists to avoid.
	if r, g, b, _ := tables.RGBA(10); r|g|b != 0 {
		t.Fatalf("PALETTE.PAL entry 10 = %d,%d,%d, want the zeroed reserved slot", r, g, b)
	}
	if _, g, _, _ := tables.RGBA(tables.Logical[10]); g != 255 {
		t.Fatal("mapped entry did not resolve to the green display entry")
	}
}

// TestImageBytesBypassTheLogicalMap locks the boundary that keeps the map from
// recolouring the whole game: GAF/PCX/TNT bytes and the LHT/SHD source index
// are already PALETTE.PAL indices, and no lookup happens again at present time
// [03 §4.3][03 §4.3.1][07 "Retail palette contract"].
func TestImageBytesBypassTheLogicalMap(t *testing.T) {
	var tables Tables
	tables.Base[3] = [4]byte{10, 20, 30, 255}
	tables.Base[7] = [4]byte{70, 70, 70, 255}
	tables.Light[2*256+7] = 3
	tables.Shade[5][7] = 3
	tables.Logical[7] = 3 // a semantic entry that must not touch image bytes

	if r, g, b, a := tables.RGBA(7); [4]byte{r, g, b, a} != [4]byte{70, 70, 70, 255} {
		t.Fatalf("image byte 7 resolved to %#v, want PALETTE.PAL entry 7", [4]byte{r, g, b, a})
	}
	if got := tables.LightLookup(2, 7); got != 3 {
		t.Fatalf("LHT lookup = %d, want the row entry for physical index 7", got)
	}
	if got := tables.ShadeLookup(5, 7); got != 3 {
		t.Fatalf("SHD lookup = %d, want the row entry for physical index 7", got)
	}
	// The semantic route still uses the map.
	if got := tables.GUIColor(7); got != 3 {
		t.Fatalf("GUIColor(7) = %d, want the mapped physical index 3", got)
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
