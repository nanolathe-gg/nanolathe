package gui

import "testing"

// The per-gadget font walk: the n-th kind-7 record counting from zero, with
// the font number read as a signed byte, and the common font (no record)
// otherwise [07 R-WGT-01 §6][03 R-FONT-01 §5]. The fixture is authored:
// three font records (alpha, beta, and gamma whose file does not exist) with
// gadgets in between them, so record order — not gadget adjacency — is what
// the walk counts.
func TestFontRecordCountsKindSevenFromZero(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "fonts.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var records []int
	for i, g := range w.Gadgets {
		if g.Kind == KindFont {
			records = append(records, i)
		}
	}
	if len(records) != 3 {
		t.Fatalf("fixture authors %d font records, want 3", len(records))
	}
	for n, want := range records {
		if got := w.FontRecord(uint8(n)); got != want {
			t.Fatalf("FontRecord(%d) = %d want gadget %d (the %d-th kind-7 record counting from zero) [07 R-WGT-01 §6]", n, got, want, n)
		}
	}
	if got := w.FontRecord(0); w.Gadgets[got].FileName != "alpha" {
		t.Fatalf("font number 0 selects %q, want the first record alpha [07 R-WGT-01 §6]", w.Gadgets[got].FileName)
	}
	// Past the end, and the two stock high values the signed read makes
	// negative: no record, the common font [03 R-FONT-01 §5].
	for _, n := range []uint8{3, 9, 132, 205} {
		if got := w.FontRecord(n); got != NoFontRecord {
			t.Fatalf("FontRecord(%d) = %d want NoFontRecord [07 R-WGT-01 §6]", n, got)
		}
	}
}

func TestFontLoadsRecordFileOnceAndIgnoresMissing(t *testing.T) {
	fs := testFS(t, "testdata")
	w, err := Load(fs, "fonts.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	alpha := w.Font(fs, 0)
	beta := w.Font(fs, 1)
	if alpha == nil || beta == nil {
		t.Fatalf("Font(0)=%v Font(1)=%v want both loaded from the record file slots [07 R-WGT-01 §12]", alpha != nil, beta != nil)
	}
	if alpha == beta || alpha.Height == beta.Height {
		t.Fatalf("records 0 and 1 resolved to the same font; the walk must count records, not reuse the first")
	}
	if again := w.Font(fs, 0); again != alpha {
		t.Fatalf("Font(0) reloaded the record; the window must cache the loaded FNT")
	}
	// Record 2 names a file the VFS does not hold: a null load, so the
	// selector keeps the previously active font [03 R-FONT-01 §5].
	if got := w.Font(fs, 2); got != nil {
		t.Fatalf("Font(2) = %v want nil for a missing record file", got)
	}
	// No record and no VFS both yield nil without error.
	for _, n := range []uint8{9, 132, 205} {
		if got := w.Font(fs, n); got != nil {
			t.Fatalf("Font(%d) = %v want nil (no such record selects the common font) [07 R-WGT-01 §6]", n, got)
		}
	}
	if got := w.Font(nil, 0); got != nil {
		t.Fatalf("Font with a nil VFS = %v want nil", got)
	}
	var none *Window
	if none.FontRecord(0) != NoFontRecord || none.Font(fs, 0) != nil {
		t.Fatal("a nil window selects no record")
	}
}

func TestFontRecordWithoutRecordsIsCommonFont(t *testing.T) {
	// A window that authors no kind-7 record — the stock LOUNGE/TALK2 case,
	// whose gadgets still carry font numbers — selects the common font for
	// every number, including 0 [03 R-FONT-01 §5].
	fs := testFS(t, "testdata")
	w, err := Load(fs, "defaults.gui")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, n := range []uint8{0, 1, 132, 205} {
		if got := w.FontRecord(n); got != NoFontRecord {
			t.Fatalf("FontRecord(%d) = %d on a window with no font records, want NoFontRecord", n, got)
		}
	}
}
