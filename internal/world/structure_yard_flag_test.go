package world

import "testing"

func TestStructureYardFlagPreservesOtherPlotFlags(t *testing.T) {
	var cell PlotCell
	cell.SetFlagByte(0xd5)
	cell.SetStructureYard(true)
	if !cell.StructureYard() || cell.FlagByte() != 0xd7 {
		t.Fatalf("set structure-yard flag = %#02x, want %#02x", cell.FlagByte(), uint8(0xd7))
	}
	cell.SetStructureYard(false)
	if cell.StructureYard() || cell.FlagByte() != 0xd5 {
		t.Fatalf("clear structure-yard flag = %#02x, want %#02x", cell.FlagByte(), uint8(0xd5))
	}
}
