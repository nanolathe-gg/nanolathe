package world

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"testing"
)

func TestPlacementOccupantExceptionDoesNotWaiveFeatureOrAllocation(t *testing.T) {
	terrain := placementFixture(t, &content.FeatureDef{Blocking: true})
	extent, _ := NewFootprintExtent(1, 1)
	rect, _ := NewFootprintRect(NewFootprintAnchor(2, 2), extent)
	yard, _ := ParseYardMap("o", 1, 1)
	q := PlacementQuery{Rect: rect, Yard: yard, SkipTerrainAggregates: true}
	terrain.PlotAt(2, 2).SetOccupantA(42)
	if _, err := terrain.CheckPlacement(q); err == nil {
		t.Fatal("ordinary allocation admitted occupant")
	}
	q.AdmitOccupant = func(h uint16) bool { return h == 42 }
	got, err := terrain.CheckPlacement(q)
	if err != nil || !got.OccupantsAdmitted {
		t.Fatalf("preview exception: %+v %v", got, err)
	}
	terrain.PlotAt(2, 2).SetFeature(0)
	if _, err := terrain.CheckPlacement(q); err == nil {
		t.Fatal("occupant exception waived blocking feature")
	}
	if terrain.PlotAt(2, 2).OccupantA() != 42 {
		t.Fatal("preview mutated occupancy")
	}
}
