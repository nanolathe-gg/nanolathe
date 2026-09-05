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

// The Mapping box is the explored-memory word grid verbatim, exact-size gated
// like the two Plotmap boxes [08 R-SAVE-02 §12].
func TestApplyRetailMappingWordsIsExactSizeGated(t *testing.T) {
	words := make([]uint16, 3)
	for _, data := range [][]byte{nil, make([]byte, 5), make([]byte, 8)} {
		if err := applyRetailMappingWords(words, data); err == nil {
			t.Fatalf("Mapping box of %d bytes accepted for %d words", len(data), len(words))
		}
	}
	for i := range words {
		words[i] = 0x03ff // the fill a history-disabled rebuild leaves behind
	}
	if err := applyRetailMappingWords(words, []byte{0x01, 0x00, 0x00, 0x02, 0xff, 0x03}); err != nil {
		t.Fatalf("apply Mapping words: %v", err)
	}
	for i, want := range []uint16{0x0001, 0x0200, 0x03ff} {
		if words[i] != want {
			t.Fatalf("word %d = %#04x, want %#04x", i, words[i], want)
		}
	}
}

// An absent Meteor account decodes as nine zeros, which "silently disables and
// de-activates the shower rather than failing the load"
// [08 "Account inventory"]. The four coordinates are sixteen-bit globals the
// reader truncates back to sixteen bits.
func TestRestoreRetailMeteorAppliesSavedScalars(t *testing.T) {
	s := &Session{}
	s.Meteor.Active = true
	s.Meteor.NextStrike = 4242
	restoreRetailMeteor(s, save.MeteorScalars{})
	if !s.Meteor.Initialized {
		t.Fatal("restore did not install the authored meteor parameters")
	}
	if s.Meteor.Enabled || s.Meteor.Active || s.Meteor.NextStrike != 0 || s.Meteor.StrikeEnds != 0 || s.Meteor.NextHit != 0 {
		t.Fatalf("missing Meteor box did not take the zero default: %+v", s.Meteor)
	}

	s = &Session{}
	restoreRetailMeteor(s, save.MeteorScalars{
		Enabled: 1, Active: 1, NextStrikeTime: 9000, TimeStrikeEnds: 8700, NextHitTime: 8650,
		OriginX: -7, OriginZ: 33, TargetX: int32(int16(-32768)), TargetZ: 44,
	})
	if !s.Meteor.Enabled || !s.Meteor.Active {
		t.Fatalf("saved enable/active lost: %+v", s.Meteor)
	}
	if s.Meteor.NextStrike != 9000 || s.Meteor.StrikeEnds != 8700 || s.Meteor.NextHit != 8650 {
		t.Fatalf("saved deadlines lost: %+v", s.Meteor)
	}
	if s.Meteor.OriginX != -7 || s.Meteor.OriginZ != 33 || s.Meteor.TargetX != -32768 || s.Meteor.TargetZ != 44 {
		t.Fatalf("saved geometry lost: %+v", s.Meteor)
	}
}
