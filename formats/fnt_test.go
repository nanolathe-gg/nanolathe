package formats

import "testing"

func TestFNTContinuousMSBFirstBitsAndAbsentGlyphs(t *testing.T) {
	// Height 3, width 3: nine bits packed continuously (101 010 111).
	data := make([]byte, 516+1+2)
	data[0] = 3
	data[4+int('A')*2], data[5+int('A')*2] = 4, 2 // offset 516
	data[516] = 3
	data[517], data[518] = 0xab, 0x80 // 1010101110000000
	f, err := LoadFNT(data)
	if err != nil {
		t.Fatal(err)
	}
	g := f.Glyphs['A']
	if g == nil || g.Width != 3 || g.Height != 3 {
		t.Fatalf("glyph = %#v", g)
	}
	want := []bool{true, false, true, false, true, false, true, true, true}
	for i, expected := range want {
		if got := g.On(i%3, i/3); got != expected {
			t.Fatalf("bit %d = %v, want %v", i, got, expected)
		}
	}
	if f.Glyphs['B'] != nil {
		t.Fatal("zero glyph offset should remain absent")
	}
}

func TestFNTMalformedGlyphFailsSoft(t *testing.T) {
	data := make([]byte, 516+1)
	data[0] = 8
	data[4], data[5] = 4, 2 // offset 516
	data[516] = 4           // needs four bitmap bytes, but only one is present
	if _, err := LoadFNT(data); err == nil {
		t.Fatal("truncated glyph was accepted")
	}
	var g FNTGlyph
	if g.On(0, 0) {
		t.Fatal("short manually assembled glyph should be transparent")
	}
}

func TestFNTReducedTablePreservesNamedHeaderBytes(t *testing.T) {
	// First code 32 gives a 448-byte table: this 454-byte font is valid even
	// though a full table would require 516 bytes [fmt fnt].
	data := make([]byte, 4+2*(256-32)+2)
	data[0], data[1], data[2], data[3] = 1, 0x7e, 0xfe, 32
	data[4+2*int('A'-32)] = byte(len(data) - 2)
	data[4+2*int('A'-32)+1] = byte((len(data) - 2) >> 8)
	data[len(data)-2], data[len(data)-1] = 1, 0x80
	f, err := LoadFNT(data)
	if err != nil {
		t.Fatal(err)
	}
	if f.Height != 1 || f.Ignored != 0x7e || f.Baseline != -2 || f.FirstCode != 32 {
		t.Fatalf("header = %#v", f)
	}
	if f.Glyphs[31] != nil || f.Glyphs['A'] == nil || !f.Glyphs['A'].On(0, 0) {
		t.Fatalf("first-code table indexing selected the wrong glyphs")
	}
}
