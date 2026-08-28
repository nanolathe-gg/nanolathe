package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestMoveLaneIntegerDistance(t *testing.T) {
	for _, tc := range []struct {
		n    uint64
		want uint64
	}{
		{0, 0}, {1, 1}, {24, 4}, {25, 5}, {26, 5}, {100, 10},
		{^uint64(0), 4294967295},
	} {
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
	got := squaredDistanceFixed(maxInt64, minInt64, 0, 0)
	if got != maxUint64 {
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
	// The footprint's derived range, not an arbitrary anchor corner, controls
	// the hard slope boundary [R-P0-08].
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
	// The final predicate intentionally remains false while R-P0-01 is open;
	// route pruning is tested in route_test.go in its own integer point domain.
	if (&System{}).finalGoalReached(nil, true) {
		t.Fatal("route-prune tolerance must not imply order completion")
	}
}

func TestMoveLaneSmoothingPolicyAndFootprintLegality(t *testing.T) {
	terrain := &world.Terrain{CellW: 5, CellH: 5, Plot: make([]world.PlotCell, 25)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i].SetHeight(10)
		terrain.Plot[i].SetMinHeight(10)
		terrain.Plot[i].SetMaxHeight(10)
	}
	// A blocking cell lies on the diagonal shortcut. A 2x2 footprint must
	// reject the ray rather than cut the corner [R-P0-08].
	terrain.Plot[1+1*5].SetFeature(world.PlotFeatureVoid)
	profile := Profile{FootPrintX: 2, FootPrintZ: 2, MinWaterDepth: -10000, MaxSlope: 255}
	kbot := &content.UnitDef{MovementClass: "KBOTSS2"}
	vehicle := &content.UnitDef{MovementClass: "TANKSH2"}
	rK := &Route{}
	rK.Publish([]Point{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 2, Z: 1}})
	smoothLandRoute(rK, kbot, profile, terrain)
	if rK.Count != 3 {
		t.Fatalf("Kbot shortcut cut blocked footprint corner; count=%d", rK.Count)
	}
	rV := &Route{}
	rV.Publish([]Point{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 2, Z: 0}})
	smoothLandRoute(rV, vehicle, profile, terrain)
	if rV.Count != 3 {
		t.Fatalf("vehicle route was smoothed despite conservative policy; count=%d", rV.Count)
	}
	flatProfile := Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255}
	flatTerrain := &world.Terrain{CellW: 5, CellH: 5, Plot: make([]world.PlotCell, 25)}
	for i := range flatTerrain.Plot {
		flatTerrain.Plot[i].SetFeature(world.PlotFeatureNone)
		flatTerrain.Plot[i].SetHeight(10)
		flatTerrain.Plot[i].SetMinHeight(10)
		flatTerrain.Plot[i].SetMaxHeight(10)
	}
	flatK := &Route{}
	flatK.Publish([]Point{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 2, Z: 1}})
	smoothLandRoute(flatK, kbot, flatProfile, flatTerrain)
	if flatK.Count != 2 {
		t.Fatalf("legal Kbot route did not smooth; count=%d", flatK.Count)
	}
	if !aggressiveLandSmoothing(kbot) || aggressiveLandSmoothing(vehicle) {
		t.Fatal("land smoothing policy classifier mismatch")
	}
}
