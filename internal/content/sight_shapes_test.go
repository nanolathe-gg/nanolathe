//go:build retail

package content

import (
	"testing"
)

// TestSightShapesFromInstall locks the authored visibility-mask geometry
// against the reference install [03 §3.2]. It is the measurement behind the
// "shape k covers radius k+5" contract: without it, reading the quantized index
// as a radius looks plausible.
func TestSightShapesFromInstall(t *testing.T) {
	fs := mountRetail(t)
	sh, err := CompileSightShapes(fs)
	if err != nil {
		t.Fatal(err)
	}
	if sh.Count() != 10 {
		t.Fatalf("sight shapes = %d, want 10", sh.Count())
	}
	for k := 0; k < sh.Count(); k++ {
		s := sh.Shape(k)
		wantSide := int32(11 + 2*k)
		if s.W != wantSide || s.H != wantSide {
			t.Fatalf("shape %d is %dx%d, want %dx%d", k, s.W, s.H, wantSide, wantSide)
		}
		// The anchor is the frame centre, so shape k has a radius of k+5.
		if s.AnchorX != wantSide/2 || s.AnchorY != wantSide/2 {
			t.Fatalf("shape %d anchor (%d,%d), want (%d,%d)",
				k, s.AnchorX, s.AnchorY, wantSide/2, wantSide/2)
		}
		if int32(len(s.Opaque)) != s.W*s.H {
			t.Fatalf("shape %d mask is %d bytes for %dx%d", k, len(s.Opaque), s.W, s.H)
		}
		opaque := 0
		for _, o := range s.Opaque {
			if o {
				opaque++
			}
		}
		if opaque == 0 {
			t.Fatalf("shape %d has no opaque cells", k)
		}
	}
	// The observer's own cell is always covered.
	if s := sh.Shape(0); !s.Covers(s.AnchorX, s.AnchorY) {
		t.Fatal("shape 0 does not cover its own anchor")
	}
}

// TestCompileLOSTables records SPEC_CONFLICTS SC9: los.tdf declares nine tables
// and supplies twelve. The clamp uses the declared count.
func TestCompileLOSTables(t *testing.T) {
	fs := mountRetail(t)
	lt, err := CompileLOSTables(fs)
	if err != nil {
		t.Fatal(err)
	}
	if lt.NumTables != 9 {
		t.Fatalf("TABLEINFO numtables = %d, want 9 (SC9)", lt.NumTables)
	}
	if len(lt.Tables) != 12 {
		t.Fatalf("discovered %d TABLE sections, want 12 (SC9)", len(lt.Tables))
	}
	// Every line is a count followed by that many (dx,dz) pairs [03 §3.2].
	for _, tb := range lt.Tables {
		for i, line := range tb.Lines {
			if len(line) < 3 {
				t.Fatalf("TABLE%d line%d too short: %v", tb.TableNum, i+1, line)
			}
			if want := 1 + 2*int(line[0]); len(line) != want {
				t.Fatalf("TABLE%d line%d has %d values, want %d for count %d",
					tb.TableNum, i+1, len(line), want, line[0])
			}
		}
	}
}
