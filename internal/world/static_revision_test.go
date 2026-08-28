package world

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

func TestStaticObstacleRevisionSaturates(t *testing.T) {
	terrain := &Terrain{staticObstacleRevision: math.MaxUint64}
	terrain.BumpStaticObstacleRevision()
	if got := terrain.StaticObstacleRevision(); got != math.MaxUint64 {
		t.Fatalf("saturated revision = %d, want %d", got, uint64(math.MaxUint64))
	}
}

func TestLowLevelFeatureStampDoesNotChooseRevisionPolicy(t *testing.T) {
	terrain := &Terrain{CellW: 4, CellH: 4, Plot: make([]PlotCell, 16)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(PlotFeatureNone)
	}
	terrain.FeatureDefs = []*content.FeatureDef{{Blocking: true, FootprintX: 1, FootprintZ: 1}}
	if err := terrain.StampFeatureRect(1, 1, 0, 1, 1); err != nil {
		t.Fatal(err)
	}
	if got := terrain.StaticObstacleRevision(); got != 0 {
		t.Fatalf("low-level stamp changed revision to %d", got)
	}
}
