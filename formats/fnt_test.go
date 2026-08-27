package formats

import (
	"encoding/binary"
	"testing"
)

func TestFNTContinuousMSBFirstBitsAndAbsentGlyphs(t *testing.T) {
	// Height 3, width 3: nine bits packed continuously (101 010 111).
	data := make([]byte, 516+1+2)
	binary.LittleEndian.PutUint16(data[0:], 3)
	binary.LittleEndian.PutUint16(data[4+uint16('A')*2:], 516)
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
	binary.LittleEndian.PutUint16(data[0:], 8)
	binary.LittleEndian.PutUint16(data[4:], 516)
	data[516] = 4 // needs four bitmap bytes, but only one is present
	if _, err := LoadFNT(data); err == nil {
		t.Fatal("truncated glyph was accepted")
	}
	var g FNTGlyph
	if g.On(0, 0) {
		t.Fatal("short manually assembled glyph should be transparent")
	}
}
