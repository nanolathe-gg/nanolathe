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

// TestMoveLaneFootprintSlopeIsPerCell locks the correction of [04 R-SLOPE-01]
// §3: the footprint's tier is the minimum of the per-cell tiers, each computed
// from that cell's own derived pair, never from a height span aggregated over
// the rectangle.
//
// Authored fixture: a 5×5 map, every cell's own pair spanning 12 (steep for a
// class whose BadSlope is 10 and MaxSlope 20), but the cells stepping upward
// so the rectangle's aggregate span is 24 — over the hard limit. Retail's rule
// gives steep; the aggregate gave blocked, and that is the difference that
// penned stock vehicles into their start plateau [04 R-SLOPE-01 §4].
func TestMoveLaneFootprintSlopeIsPerCell(t *testing.T) {
	terrain := &world.Terrain{CellW: 5, CellH: 5, SeaLevel: 0, Plot: make([]world.PlotCell, 25)}
	for z := int32(0); z < 5; z++ {
		for x := int32(0); x < 5; x++ {
			c := terrain.PlotAt(x, z)
			c.SetFeature(world.PlotFeatureNone)
			// Each cell's own span is 12; the pairs climb by 6 per step, so a
			// 3×3 rectangle spans 12 + 2*6 + 2*6 = 36 in the aggregate.
			base := uint8(20 + 6*(x+z))
			c.SetHeight(base)
			c.SetMinHeight(base)
			c.SetMaxHeight(base + 12)
		}
	}
	p := Profile{FootPrintX: 3, FootPrintZ: 3, MinWaterDepth: -10000, MaxWaterDepth: 10000, MaxSlope: 20, BadSlope: 10}
	// Anchor 1,1 keeps the 3×3 footprint and its ring inside the map without
	// touching the last column or row [04 R-COLL-01 §2 steps 1-4].
	if got := p.ClassifyFootprint(terrain, 1, 1); got != ClassSteep {
		t.Fatalf("per-cell slope 12 under MaxSlope 20 got %v want steep [04 R-SLOPE-01 §3]", got)
	}
	// Equality with MaxSlope is steep, never blocked [04 R-SLOPE-01 §2].
	p.MaxSlope = 12
	if got := p.ClassifyFootprint(terrain, 1, 1); got != ClassSteep {
		t.Fatalf("slope equal to MaxSlope got %v want steep [04 R-SLOPE-01 §2]", got)
	}
	// One height byte lower and every cell is blocked, so the minimum is 0.
	p.MaxSlope = 11
	if got := p.ClassifyFootprint(terrain, 1, 1); got != ClassBlocked {
		t.Fatalf("slope over MaxSlope got %v want blocked", got)
	}
	// A single blocked cell blocks the whole footprint: the tier is the
	// MINIMUM over the covered cells [04 R-SLOPE-01 §3 item 2].
	p.MaxSlope = 20
	terrain.PlotAt(2, 2).SetMaxHeight(terrain.PlotAt(2, 2).MinHeight() + 21)
	if got := p.ClassifyFootprint(terrain, 1, 1); got != ClassBlocked {
		t.Fatalf("one blocked cell got %v want blocked", got)
	}
}

// TestMoveLaneFootprintRingDemotion locks the ring rule: an all-clear
// footprint keeps its clear tier only while every cell of the one-cell ring is
// clear too, and a non-clear ring cell demotes it to steep rather than
// blocking it [04 R-SLOPE-01 §3 item 1 closed form].
func TestMoveLaneFootprintRingDemotion(t *testing.T) {
	terrain := &world.Terrain{CellW: 8, CellH: 8, SeaLevel: 0, Plot: make([]world.PlotCell, 64)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i].SetHeight(40)
		terrain.Plot[i].SetMinHeight(40)
		terrain.Plot[i].SetMaxHeight(40)
	}
	p := Profile{FootPrintX: 2, FootPrintZ: 2, MinWaterDepth: -10000, MaxWaterDepth: 10000, MaxSlope: 20, BadSlope: 10}
	if got := p.ClassifyFootprint(terrain, 3, 3); got != ClassClear {
		t.Fatalf("flat footprint with a flat ring got %v want clear", got)
	}
	// A ring corner only: slope 15 is steep for this class, not blocked.
	terrain.PlotAt(2, 2).SetMaxHeight(55)
	if got := p.ClassifyFootprint(terrain, 3, 3); got != ClassSteep {
		t.Fatalf("steep ring corner got %v want the clear result demoted to steep", got)
	}
	// A blocked ring cell demotes; it never blocks the anchor.
	terrain.PlotAt(2, 2).SetMaxHeight(40)
	terrain.PlotAt(5, 4).SetMaxHeight(99)
	if got := p.ClassifyFootprint(terrain, 3, 3); got != ClassSteep {
		t.Fatalf("blocked ring cell got %v want steep, not blocked", got)
	}
}

// TestMoveLaneFootprintEdgeBounds locks the two bounds at the two sites
// [04 R-SLOPE-01 §3]: the map-load layer builder's window zeroes an anchor
// only when its footprint LEAVES the map, while the rectangle restamp zeroes
// any anchor whose extent reaches column W−1 or row H−1 [04 R-COLL-01 §2
// steps 3-4]. Retail's void strips make the two agree on a loaded map
// [03 R-TERR-01 §2]; that strip pass is not implemented here, so both are
// asserted where they live.
func TestMoveLaneFootprintEdgeBounds(t *testing.T) {
	terrain := &world.Terrain{CellW: 6, CellH: 6, SeaLevel: 0, Plot: make([]world.PlotCell, 36)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i].SetHeight(40)
		terrain.Plot[i].SetMinHeight(40)
		terrain.Plot[i].SetMaxHeight(40)
	}
	p := Profile{FootPrintX: 2, FootPrintZ: 2, MinWaterDepth: -10000, MaxWaterDepth: 10000, MaxSlope: 20, BadSlope: 10}
	// Map-load bound: columns 4..5 stay inside the map, so the anchor is not 0.
	if got := p.ClassifyFootprint(terrain, 4, 3); got == ClassBlocked {
		t.Fatal("map-load bound zeroed an anchor whose footprint is still inside the map")
	}
	if got := p.ClassifyFootprint(terrain, 5, 3); got != ClassBlocked {
		t.Fatalf("footprint leaving the map got %v want blocked", got)
	}
	// Restamp bound: the same anchor is 0 once the rectangle restamp rewrites
	// it, because its extent reaches the last column.
	l := NewClassLayer(p, terrain, nil)
	if got := l.Value(4, 3); got == LayerBlocked {
		t.Fatal("map-load stamp zeroed an anchor whose footprint is still inside the map")
	}
	l.RestampRect(0, 0, 5, 5)
	if got := l.Value(4, 3); got != LayerBlocked {
		t.Fatalf("restamped anchor reaching the last column got %d want 0", got)
	}
	if got := l.Value(3, 4); got != LayerBlocked {
		t.Fatalf("restamped anchor reaching the last row got %d want 0", got)
	}
	if got := l.Value(3, 3); got == LayerBlocked {
		t.Fatal("restamp zeroed an interior anchor")
	}
}

func TestMoveLaneFinalCompletionRemainsUnknown(t *testing.T) {
	if (&System{}).finalGoalReached(nil, true) {
		t.Fatal("route-prune tolerance must not imply order completion")
	}
}
