package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestRestoreRetailBattleCoreRejectsIncompleteStage(t *testing.T) {
	if err := RestoreRetailBattleCore(nil); err == nil {
		t.Fatal("nil staged restore accepted")
	}
}

func TestRestoreRetailFeaturesPrevalidatesBeforePlacement(t *testing.T) {
	const width, height = 4, 4
	attrs := make([]formats.TNTAttribute, width*height)
	for i := range attrs {
		attrs[i].Feature = world.PlotFeatureNone
	}
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"}, FootprintX: 1, FootprintZ: 1}
	terrain := &world.Terrain{CellW: width, CellH: height, Plot: world.ExpandPlot(attrs, width, height), FeatureDefs: []*content.FeatureDef{def}}
	svc := features.NewService(terrain, nil, nil, nil)
	img := save.FeatureImage{Normal: []save.FeatureRecord{
		{X: 1, Z: 1, TypeID: 0, Data: make([]byte, 8)},
		{X: 2, Z: 2, TypeID: 1, Data: make([]byte, 8)}, // unresolved after the first valid row
	}}
	if err := restoreRetailFeatures(svc, nil, img); err == nil {
		t.Fatal("feature restore accepted an unresolved definition")
	}
	if len(svc.Instances()) != 0 || terrain.PlotAt(1, 1).Occupied() {
		t.Fatal("feature restore mutated placement before validation completed")
	}
}
