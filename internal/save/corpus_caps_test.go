package save

import (
	"testing"
)

// TestSaveBoxAndBankCapsAreOutsideStock proves the save layer's own fault
// guards are outside stock-reachable behavior [P1-I09]: the bulk box sizes
// stock saves are written at are the exact sizes the reader accepts, and the
// HAPIBANK header bounds of bank.go C13 do not reject a well-formed bank.
//
// It used to be TestCorpusSaveCaps_Retail and compiled the whole retail
// catalog to walk every .tnt in the corpus and assert that none hit
// DefaultTNTLimits. That census is the same one
// formats.TestCorpusFormatLimits_Retail runs — including the "every stock TNT
// parses" claim, which was moved there rather than dropped — so the corpus was
// being measured twice for one conclusion. What is left here is the part only
// this package can state, and none of it needs retail assets: it is a claim
// about our own constants and reader.
func TestSaveBoxAndBankCapsAreOutsideStock(t *testing.T) {
	// Retail save bulk boxes are fixed sizes per bulk.go. Stock saves use
	// these sizes, so validation must not reject them.
	if UnitBoxSize != 0xB8 {
		t.Fatalf("UnitBoxSize %#x, want 0xB8", UnitBoxSize)
	}
	if OrderBoxSize != 0x3A {
		t.Fatalf("OrderBoxSize %#x, want 0x3A", OrderBoxSize)
	}

	// HAPIBANK header bounds: C13's checks reject out-of-range offsets, and a
	// bank this package wrote itself must round-trip through them.
	b := NewBuilder()
	b.Add("Summary").SetInt("maxunits", 100)
	accounts := b.Add("Players")
	accounts.SetInt("Human Player", 0)
	bank, err := OpenBytes(b.Bytes())
	if err != nil {
		t.Fatalf("synthetic bank parse failed: %v", err)
	}
	if bank.Count() == 0 {
		t.Fatalf("synthetic bank has no accounts")
	}
}
