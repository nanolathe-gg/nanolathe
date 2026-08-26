package world

import (
	"errors"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestPlacementConversionRejectsInvalidExtents(t *testing.T) {
	for _, tc := range [][2]int32{{0, 1}, {1, 0}, {-1, 2}, {2, -1}} {
		if _, err := NewFootprintExtent(tc[0], tc[1]); !errors.Is(err, ErrInvalidFootprint) {
			t.Errorf("NewFootprintExtent(%d,%d) error = %v, want ErrInvalidFootprint", tc[0], tc[1], err)
		}
	}
	var zero FootprintExtent
	if _, err := SnapMobilePlacement(0, 0, 0, zero); !errors.Is(err, ErrInvalidFootprint) {
		t.Fatalf("zero extent mobile placement error = %v, want ErrInvalidFootprint", err)
	}
}

func TestPlacementAnchorHalfCellThresholds(t *testing.T) {
	const cell = numeric.Fixed(worldUnitsPerCell)
	for _, foot := range []int32{1, 2, 3, 4} {
		extent, err := NewFootprintExtent(foot, foot)
		if err != nil {
			t.Fatal(err)
		}
		// For anchor 0, the transition is at (foot-1)*half. The one-unit
		// samples lock strict below/at/above behavior, including odd/even
		// extents, without introducing floating-point rounding.
		threshold := numeric.Fixed(int64(foot-1) * int64(worldUnitsPerCell/2))
		for _, tc := range []struct {
			name  string
			delta int64
			want  int32
		}{
			{name: "below", delta: -1, want: -1},
			{name: "at", delta: 0, want: 0},
			{name: "above", delta: 1, want: 0},
		} {
			p := threshold + numeric.Fixed(tc.delta)
			got, err := SnapFootprintAnchor(p, p, extent)
			if err != nil {
				t.Fatalf("foot %d %s: %v", foot, tc.name, err)
			}
			if got.CellX() != tc.want || got.CellZ() != tc.want {
				t.Errorf("foot %d %s at %d: anchor=(%d,%d), want (%d,%d)", foot, tc.name, p, got.CellX(), got.CellZ(), tc.want, tc.want)
			}
		}
		// A full-cell displacement keeps the same threshold behavior at a
		// negative anchor, proving the signed arithmetic shift is retained.
		p := -3*cell + threshold
		got, err := SnapFootprintAnchor(p, p, extent)
		if err != nil {
			t.Fatalf("foot %d negative threshold: %v", foot, err)
		}
		if got.CellX() != -3 || got.CellZ() != -3 {
			t.Errorf("foot %d negative threshold anchor=(%d,%d), want (-3,-3)", foot, got.CellX(), got.CellZ())
		}
	}
}

func TestFootprintRectIsHalfOpen(t *testing.T) {
	extent, err := NewFootprintExtent(2, 3)
	if err != nil {
		t.Fatal(err)
	}
	rect, err := NewFootprintRect(NewFootprintAnchor(-2, 4), extent)
	if err != nil {
		t.Fatal(err)
	}
	if rect.MinX() != -2 || rect.MaxX() != 0 || rect.MinZ() != 4 || rect.MaxZ() != 7 {
		t.Fatalf("rect bounds = [%d,%d)x[%d,%d), want [-2,0)x[4,7)", rect.MinX(), rect.MaxX(), rect.MinZ(), rect.MaxZ())
	}
	if rect.Width() != 2 || rect.Depth() != 3 {
		t.Fatalf("rect dimensions = %dx%d, want 2x3", rect.Width(), rect.Depth())
	}
	for _, cell := range []struct {
		x, z int32
		want bool
	}{
		{-2, 4, true}, {-1, 6, true}, {-2, 6, true},
		{0, 4, false}, {-2, 7, false}, {-3, 4, false}, {0, 7, false},
	} {
		if got := rect.Contains(cell.x, cell.z); got != cell.want {
			t.Errorf("Contains(%d,%d) = %v, want %v", cell.x, cell.z, got, cell.want)
		}
	}
}

func TestMobilePlacementCenterAndAnchorRoundTrip(t *testing.T) {
	for _, foot := range []int32{1, 2, 3, 4} {
		extent, err := NewFootprintExtent(foot, foot)
		if err != nil {
			t.Fatal(err)
		}
		for _, anchor := range []FootprintAnchor{
			NewFootprintAnchor(-7, -3), NewFootprintAnchor(0, 0), NewFootprintAnchor(9, 11),
		} {
			center, err := CenterForFootprint(anchor, extent)
			if err != nil {
				t.Fatal(err)
			}
			got, err := SnapFootprintAnchor(center.X(), center.Z(), extent)
			if err != nil {
				t.Fatal(err)
			}
			if got != anchor {
				t.Errorf("foot %d anchor %v -> center (%d,%d) -> anchor %v", foot, anchor, center.X(), center.Z(), got)
			}
			wantX := CellToWorld(anchor.cellX) + numeric.Fixed(int64(foot)*worldUnitsPerCell/2)
			if center.X() != wantX || center.Z() != CellToWorld(anchor.cellZ)+numeric.Fixed(int64(foot)*worldUnitsPerCell/2) {
				t.Errorf("foot %d anchor %v center=(%d,%d), want midpoint", foot, anchor, center.X(), center.Z())
			}
		}
	}
	extent, err := NewFootprintExtent(2, 3)
	if err != nil {
		t.Fatal(err)
	}
	withHeight, err := SnapMobilePlacement(numeric.Fixed(4*worldUnitsPerCell), 77, numeric.Fixed(-2*worldUnitsPerCell), extent)
	if err != nil {
		t.Fatal(err)
	}
	if withHeight.ModelPosition().Y() != 77 {
		t.Fatalf("mobile site Y = %d, want 77", withHeight.ModelPosition().Y())
	}
}

func TestFactoryPlacementPreservesQueryBuildInfoTransform(t *testing.T) {
	extent, err := NewFootprintExtent(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	// This authored exit transform is deliberately not the geometric center
	// implied by the snapped rectangle. It must survive exactly as the model
	// position while validation receives the separately snapped rectangle.
	query := NewModelWorldPosition(
		numeric.Fixed(10*worldUnitsPerCell+12345),
		numeric.Fixed(37*worldUnitsPerCell+54321),
		numeric.Fixed(-7*worldUnitsPerCell+9876),
	)
	factory, err := SnapFactoryPlacement(query, extent)
	if err != nil {
		t.Fatal(err)
	}
	if factory.ModelPosition() != query {
		t.Fatalf("factory model position = (%d,%d,%d), want authored QueryBuildInfo (%d,%d,%d)", factory.ModelPosition().X(), factory.ModelPosition().Y(), factory.ModelPosition().Z(), query.X(), query.Y(), query.Z())
	}
	if factory.Rect().Anchor() != factory.Anchor() {
		t.Fatal("factory rectangle did not retain its validation anchor")
	}
	geometric, err := CenterForFootprint(factory.Anchor(), extent)
	if err != nil {
		t.Fatal(err)
	}
	if geometric == query {
		t.Fatal("fixture query transform unexpectedly equals geometric center")
	}
	if factory.Rect().Contains(factory.Anchor().CellX(), factory.Anchor().CellZ()) == false {
		t.Fatal("factory validation rectangle does not contain its origin")
	}
}

func TestPlacementArithmeticOverflowIsRejected(t *testing.T) {
	extent, err := NewFootprintExtent(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SnapMobilePlacement(numeric.FixedFromRaw(-1<<63), 0, 0, extent); !errors.Is(err, ErrPlacementOverflow) {
		t.Fatalf("minimum world pick error = %v, want ErrPlacementOverflow", err)
	}
	anchor := NewFootprintAnchor(1<<31-1, 0)
	if _, err := NewFootprintRect(anchor, extent); !errors.Is(err, ErrPlacementOverflow) {
		t.Fatalf("max-cell rectangle error = %v, want ErrPlacementOverflow", err)
	}
}

func TestLegacyPlacementWrappersRemainNonPanicking(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("legacy placement wrapper panicked for an input it historically accepted: %v", recovered)
		}
	}()
	PlacementAnchor(0, 0, 0, 1)
	PlacementCenter(0, 0, 0, 1)
}
