package hud

import "testing"

// TestBuildMarkerSweepEndpoints locks the two ends of the sweep [07 §9]. At age
// zero the four inner lines lie on the footprint's own edges; at the end of the
// sweep each has crossed to the opposite edge, so the marker reads as the same
// rectangle it started from.
func TestBuildMarkerSweepEndpoints(t *testing.T) {
	const l, tp, r, b = 100, 50, 140, 82

	start := BuildMarkerSegments(l, tp, r, b, 0, true)
	inner := start[4:]
	want := [][4]int32{{l, tp, l, b}, {r, tp, r, b}, {l, tp, r, tp}, {l, b, r, b}}
	for i, seg := range inner {
		got := [4]int32{seg.X0, seg.Y0, seg.X1, seg.Y1}
		if got != want[i] {
			t.Errorf("age 0 inner %d = %v want %v [07 §9]", i, got, want[i])
		}
	}

	end := BuildMarkerSegments(l, tp, r, b, BuildMarkerSweepTicks, true)
	if end[4].X0 != r || end[5].X0 != l {
		t.Errorf("verticals ended at %d,%d want %d,%d [07 §9]", end[4].X0, end[5].X0, r, l)
	}
	if end[6].Y0 != b || end[7].Y0 != tp {
		t.Errorf("horizontals ended at %d,%d want %d,%d [07 §9]", end[6].Y0, end[7].Y0, b, tp)
	}
}

// TestBuildMarkerAgeClamps checks that the marker holds after the sweep rather
// than running off the footprint, and that a negative age cannot invert it.
func TestBuildMarkerAgeClamps(t *testing.T) {
	held := BuildMarkerSegments(0, 0, 40, 40, 900, false)
	final := BuildMarkerSegments(0, 0, 40, 40, BuildMarkerSweepTicks, false)
	for i := range held {
		if held[i] != final[i] {
			t.Fatalf("segment %d kept moving past the sweep: %v vs %v [07 §9]", i, held[i], final[i])
		}
	}
	early := BuildMarkerSegments(0, 0, 40, 40, -5, false)
	zero := BuildMarkerSegments(0, 0, 40, 40, 0, false)
	for i := range early {
		if early[i] != zero[i] {
			t.Fatalf("negative age moved segment %d [07 §9]", i)
		}
	}
}

// TestBuildMarkerSelectionColors locks the two color pairs: retail brightens the
// marker of a unit that is currently selected.
func TestBuildMarkerSelectionColors(t *testing.T) {
	sel := BuildMarkerSegments(0, 0, 16, 16, 3, true)
	uns := BuildMarkerSegments(0, 0, 16, 16, 3, false)
	if sel[0].Color != MarkerOuterSelected || sel[4].Color != MarkerInnerSelected {
		t.Errorf("selected colors %d/%d want %d/%d [07 §9]", sel[0].Color, sel[4].Color, MarkerOuterSelected, MarkerInnerSelected)
	}
	if uns[0].Color != MarkerOuterUnselected || uns[4].Color != MarkerInnerUnselected {
		t.Errorf("unselected colors %d/%d want %d/%d [07 §9]", uns[0].Color, uns[4].Color, MarkerOuterUnselected, MarkerInnerUnselected)
	}
}
