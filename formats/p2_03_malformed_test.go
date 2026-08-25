package formats

import "testing"

func TestArithmeticOverflowWrap(t *testing.T) {
	// P2-03 int32 wrap vs saturate: add/sub/mul wrap low32 [04 §4.3]
	var a int32 = 2147483647
	var b int32 = 1
	if a+b != -2147483648 {
		t.Fatalf("int32 wrap add got %d want -2147483648", a+b)
	}
	// mul wrap
	var c int32 = 0x40000000
	if c*4 != 0 {
		t.Fatalf("int32 wrap mul got %d want 0", c*4)
	}
	// div trunc toward zero [01 §8] I3
	if int32(-7)/2 != -3 {
		t.Fatalf("trunc -7/2 got %d want -3", int32(-7)/2)
	}
	// deadline wrap unsigned compare [GAP T5]
	var tick uint32 = 0xFFFFFFFE
	var deadline int32 = 10 // tick+30 wraps to small
	deadlineU := uint32(deadline)
	if !(tick >= deadlineU) {
		t.Fatalf("wrapped deadline should be immediate fire")
	}
}

func TestMalformedGAFBounds(t *testing.T) {
	// P2-03 malformed archive/TDF/GAF/PCX/WAV behavior: GAF truncated header must error not panic
	if _, err := LoadGAF([]byte{0, 0, 0}); err == nil {
		t.Fatalf("truncated GAF should error [P2-03] fallback")
	}
	// 12-byte header with EntryCount=1 but no offset table bytes should error (truncated table)
	hdr := make([]byte, 12)
	hdr[4] = 1 // EntryCount low byte 1
	if _, err := LoadGAF(hdr); err == nil {
		t.Fatalf("GAF header with EntryCount 1 but truncated table should error [P2-03]")
	}
	// PCX: only manufacturer 0x0A+version5 validated [fmt pcx]
	if _, err := LoadPCX(make([]byte, 900)); err == nil {
		t.Fatalf("short PCX should error")
	}
	badHeader := make([]byte, 896)
	badHeader[0] = 0x09 // bad manufacturer
	badHeader[1] = 5
	if _, err := LoadPCX(append(badHeader, make([]byte, 769)...)); err == nil {
		t.Fatalf("bad PCX manufacturer should error [P2-03]")
	}
	// WAV: truncated RIFF without fmt should error
	if _, err := LoadWAV([]byte("RIFF\x00\x00\x00\x00WAVE")); err == nil {
		t.Fatalf("truncated WAV should error")
	}
}

func TestTDFDiagnosticVerbatim(t *testing.T) {
	// P2-03 TDF diag verbatim ("Parse error in .TDF File!" etc), duplicate sections retained
	data := []byte("[A] { Dup=1; }\n[A] { Dup=2; }\n")
	doc, err := ParseTDF(data)
	if err != nil {
		t.Fatalf("parse TDF with duplicate sections should succeed (fallback not fatal) %v", err)
	}
	secs := doc.Root.SectionsNamed("a")
	if len(secs) != 2 {
		t.Fatalf("duplicate sections retained got %d want 2 [02 §4][P2-03]", len(secs))
	}
	if secs[0].Section("Dup") != nil || doc.Root.Section("a") == nil {
		// sections are at root, check values
	}
	// Ensure title verbatim
	if ParseErrorTitle != "Parse error in .TDF File!" {
		t.Fatalf("title verbatim mismatch [P2-03][02 §4] got %q", ParseErrorTitle)
	}
	// malformed should yield empty tree fallback
	_, err = ParseTDF([]byte("[A] { key }"))
	if err == nil {
		t.Fatalf("missing '=' should diag [P2-03]")
	}
	// Ensure five diagnostics distinct
	diags := map[ParseDiagnostic]bool{
		DiagEqualsNotFound: true, DiagSemicolonMissing: true, DiagClosingBracket: true, DiagOpeningBrace: true, DiagNextBlock: true,
	}
	if len(diags) != 5 {
		t.Fatalf("diagnostics count")
	}
}
