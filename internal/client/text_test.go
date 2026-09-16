package client

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

func TestFNTTextMeasurementAndNewline(t *testing.T) {
	f := testFont()
	if got := MeasureText(f, "AB\nBA"); got != 4 {
		t.Fatalf("newline measurement = %d, want 4", got)
	}
	if got := MeasureText(f, "A\x00B"); got != 3 {
		t.Fatalf("NUL measurement = %d, want 3", got)
	}
	if got := MeasureText(f, "?"); got != 0 {
		t.Fatalf("absent glyph measurement = %d, want 0", got)
	}
}

func TestFNTTextDrawsContinuousBitsAfterAdmission(t *testing.T) {
	f := testFont()
	frame := make([]uint8, 8*5)
	DrawText(frame, 8, 5, f, "A", 1, 2, 0, 7)
	// A's 3x2 bits are 101/011 and the low control byte shifts rows by 1.
	if frame[1+1*8] != 7 || frame[3+1*8] != 7 || frame[2+2*8] != 7 || frame[3+2*8] != 7 {
		t.Fatalf("glyph pixels were not rasterized at the baseline: %v", frame)
	}
	for i, pixel := range frame {
		if pixel != 0 && pixel != 7 {
			t.Fatalf("unexpected pixel %d = %d", i, pixel)
		}
	}
}

func TestFNTTextTruncatesBeforeClipping(t *testing.T) {
	f := testFont()
	if got := TruncateToWidth(f, "ABBA", 3); got != "A" {
		t.Fatalf("truncated text = %q, want A", got)
	}
	if got := TruncateToWidth(f, "AB\nignored", 20); got != "AB\nignored" {
		t.Fatalf("newline suffix changed when no truncation was needed: %q", got)
	}
}

// The bounded copy keeps 299 bytes, not the 300 the buffer holds: a string
// longer than 299 bytes is cut to 299 before the width loop begins [03
// R-FONT-01 §3]. A 300-byte label whose full advance would still fit therefore
// draws 299 glyphs, one fewer than an unbounded copy would.
func TestFNTTextBoundedCopyKeeps299Bytes(t *testing.T) {
	f := testFont()
	// The literal 299 is the contract, not TextBufferBytes: spelling the
	// constant here would move the expectation with the code.
	const wide = 300 // 'B' advances one column, so 300 bytes measure 300
	label := strings.Repeat("B", 300)
	got := TruncateToWidth(f, label, wide)
	if len(got) != 299 || MeasureText(f, got) != 299 {
		t.Fatalf("300-byte label truncated to %d bytes measuring %d, want 299 and 299",
			len(got), MeasureText(f, got))
	}
	// A string the bounded copy holds whole is not shortened by it.
	fits := strings.Repeat("B", 299)
	if got := TruncateToWidth(f, fits, wide); got != fits {
		t.Fatalf("299-byte label lost %d bytes to the bounded copy", len(fits)-len(got))
	}
}

func TestFNTTextUsesCharacterCodeAfterReducedTableBias(t *testing.T) {
	f := &formats.FNT{Height: 1, Baseline: -1, FirstCode: 32, Glyphs: [256]*formats.FNTGlyph{
		'A': {Width: 1, Height: 1, Bits: []byte{0x80}},
	}}
	if got := MeasureText(f, " A"); got != 1 {
		t.Fatalf("measurement = %d, want 1", got)
	}
	frame := make([]uint8, 3*3)
	DrawText(frame, 3, 3, f, "A", 1, 0, 0, 9)
	if frame[1+1*3] != 9 {
		t.Fatalf("negative baseline did not place glyph below pen: %v", frame)
	}
}

func testFont() *formats.FNT {
	return &formats.FNT{
		Height:   2,
		Baseline: 1, // baseline descender
		Glyphs: [256]*formats.FNTGlyph{
			'A': {Width: 3, Height: 2, Bits: []byte{0xac}}, // 10101100
			'B': {Width: 1, Height: 2, Bits: []byte{0xc0}},
		},
	}
}
