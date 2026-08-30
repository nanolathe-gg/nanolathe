package movement

import (
	"github.com/nanolathe/nanolathe/internal/world"
	"testing"
)

func TestMoveLaneIntegerDistance(t *testing.T) {
	for _, tc := range []struct{ n, want uint64 }{{0, 0}, {1, 1}, {24, 4}, {25, 5}, {26, 5}, {100, 10}, {^uint64(0), 4294967295}} {
		if got := isqrt(tc.n); got != tc.want {
			t.Fatalf("isqrt(%d)=%d want %d", tc.n, got, tc.want)
		}
	}
}

func TestMoveLaneSquaredDistanceSaturates(t *testing.T) {
	threshold := uint64(2*65536) * uint64(2*65536)
	if got := squaredDistanceFixed(0, 0, 2*65536, 0); got != threshold {
		t.Fatalf("near-steering square=%d want %d", got, threshold)
	}
	if got := squaredDistanceFixed(-2*65536, 0, 0, 0); got != threshold {
		t.Fatalf("negative near-steering square=%d want %d", got, threshold)
	}
	if got := squaredDistanceFixed(maxInt64, minInt64, 0, 0); got != maxUint64 {
		t.Fatalf("extreme square wrapped: %d", got)
	}
}

func TestMoveLaneFootprintAggregate(t *testing.T) {
	terrain := &world.Terrain{CellW: 3, CellH: 3, SeaLevel: 0, Plot: make([]world.PlotCell, 9)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i].SetHeight(10)
		terrain.Plot[i].SetMinHeight(10)
		terrain.Plot[i].SetMaxHeight(10)
	}
	terrain.Plot[0].SetMinHeight(0)
	terrain.Plot[0].SetMaxHeight(0)
	terrain.Plot[1].SetMinHeight(20)
	terrain.Plot[1].SetMaxHeight(20)
	p := Profile{FootPrintX: 2, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 20, BadSlope: 10}
	if got := p.ClassifyFootprint(terrain, 0, 0); got != ClassSteep {
		t.Fatalf("aggregate slope equality/soft tier got %v want steep", got)
	}
	p.MaxSlope = 19
	if got := p.ClassifyFootprint(terrain, 0, 0); got != ClassBlocked {
		t.Fatalf("aggregate slope over hard limit got %v want blocked", got)
	}
}

func TestMoveLaneFinalCompletionRemainsUnknown(t *testing.T) {
	if (&System{}).finalGoalReached(nil, true) {
		t.Fatal("route-prune tolerance must not imply order completion")
	}
}
