package world

import (
	"bytes"
	"testing"
)

func TestRetailTerrainImagesRoundTripAndPreserveFlags(t *testing.T) {
	terrain := &Terrain{CellW: 2, CellH: 2, Plot: make([]PlotCell, 4)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetMetal(uint8(10 + i))
		terrain.Plot[i].SetFlagByte(uint8(0x81 | uint8(i)<<3))
	}
	metal, err := terrain.RetailMetalImage()
	if err != nil {
		t.Fatal(err)
	}
	placer, err := terrain.RetailPlayerFeaturesImage()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(metal, []byte{10, 11, 12, 13}) || !bytes.Equal(placer, []byte{0x01, 0x23}) {
		t.Fatalf("images metal=%x placer=%x", metal, placer)
	}
	for i := range terrain.Plot {
		terrain.Plot[i].SetMetal(0)
		terrain.Plot[i].SetFlagByte(0x87)
	}
	if err := terrain.RestoreRetailMetal(metal); err != nil {
		t.Fatal(err)
	}
	if err := terrain.RestoreRetailPlayerFeatures(placer); err != nil {
		t.Fatal(err)
	}
	for i, want := range []uint8{10, 11, 12, 13} {
		if terrain.Plot[i].Metal() != want {
			t.Fatalf("cell %d metal=%d, want %d", i, terrain.Plot[i].Metal(), want)
		}
		if terrain.Plot[i].PlacerNibble() != uint8(i) || terrain.Plot[i].FlagByte()&0x87 != 0x87 {
			t.Fatalf("cell %d flags=%#x, want preserved 0x87 and placer %d", i, terrain.Plot[i].FlagByte(), i)
		}
	}
}

func TestRetailMappingImageIsExplicitlyUnavailable(t *testing.T) {
	if _, err := (&Terrain{CellW: 2, CellH: 2, Plot: make([]PlotCell, 4)}).RetailMappingImage(); err == nil {
		t.Fatal("mapping image unexpectedly synthesized")
	}
}
