package features

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestRetailFeatureImageUsesRowMajorFamiliesAndExactWords(t *testing.T) {
	normal := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"}, FootprintX: 1, FootprintZ: 1, Damage: 10}
	anim := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "burning"}, FootprintX: 1, FootprintZ: 1, Damage: 10, Filename: "burn.gaf"}
	model := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wreck"}, FootprintX: 1, FootprintZ: 1, Damage: 10, Object: "wreck.3do"}
	terrain := &world.Terrain{CellW: 2, CellH: 2, Plot: make([]world.PlotCell, 4), FeatureNames: []string{"TREE", "BURNING", "WRECK"}, FeatureDefs: []*content.FeatureDef{normal, anim, model}}
	terrain.Plot[0].SetFeature(0)
	terrain.Plot[1].SetFeature(1)
	terrain.Plot[1].SetOccupied(true)
	terrain.Plot[2].SetFeature(2)
	terrain.Plot[3].SetFeature(world.PlotFeatureNone)
	s := NewService(terrain, nil, nil, nil)
	s.instances[1] = &Instance{Def: anim, Terrain: terrain, CX: 1, CZ: 0, IsAnimating: true, AnimationState: 0x1234, AnimationFrame: 7, AnimationSelector: 2, AnimationCountdown: 0x0b}
	s.instances[2] = &Instance{Def: model, Terrain: terrain, CX: 0, CZ: 1, OpaqueState: [18]byte{1, 2, 3}, AnimationState: 0x4567}
	image, err := s.RetailFeatureImage()
	if err != nil {
		t.Fatal(err)
	}
	if len(image.Normal) != 1 || len(image.Animating) != 1 || len(image.ThreeD) != 1 {
		t.Fatalf("families = %d/%d/%d, want one each", len(image.Normal), len(image.Animating), len(image.ThreeD))
	}
	if image.Normal[0].TypeName != "TREE" || image.Animating[0].TypeName != "BURNING" || image.ThreeD[0].TypeName != "WRECK" {
		t.Fatalf("authored names were not retained: %#v", image)
	}
	if got := binary.LittleEndian.Uint16(image.Normal[0].Data[6:]); got != 0 {
		t.Fatalf("normal anchor word %#x, want 0", got)
	}
	if got := binary.LittleEndian.Uint16(image.Animating[0].Data[6:]); got != 0x1234 {
		t.Fatalf("animation state %#x, want 0x1234", got)
	}
	if image.Animating[0].Data[9] != 0xb2 {
		t.Fatalf("animation selector/countdown byte %#x, want 0xb2", image.Animating[0].Data[9])
	}
	if got := binary.LittleEndian.Uint16(image.ThreeD[0].Data[6:]); got != 0x4567 || image.ThreeD[0].Data[8] != 1 || image.ThreeD[0].Data[10] != 3 {
		t.Fatalf("3D words not copied exactly: %x", image.ThreeD[0].Data)
	}
}

func TestRetailFeatureImageRefusesMissingAnimationState(t *testing.T) {
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "animated"}, Filename: "x.gaf"}
	terrain := &world.Terrain{CellW: 1, CellH: 1, Plot: make([]world.PlotCell, 1), FeatureNames: []string{"ANIMATED"}, FeatureDefs: []*content.FeatureDef{def}}
	terrain.Plot[0].SetFeature(0)
	terrain.Plot[0].SetOccupied(true)
	if _, err := NewService(terrain, nil, nil, nil).RetailFeatureImage(); err == nil {
		t.Fatal("animated feature without live state accepted")
	}
}

func TestRetailFeatureImageSkipsNonFamilyAnimationSequence(t *testing.T) {
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "animated"}, Filename: "x.gaf"}
	terrain := &world.Terrain{CellW: 1, CellH: 1, Plot: make([]world.PlotCell, 1), FeatureNames: []string{"ANIMATED"}, FeatureDefs: []*content.FeatureDef{def}}
	terrain.Plot[0].SetFeature(0)
	terrain.Plot[0].SetOccupied(true)
	service := NewService(terrain, nil, nil, nil)
	service.instances[0] = &Instance{Def: def, Terrain: terrain, CX: 0, CZ: 0, IsAnimating: true, AnimationSelector: 7, AnimationState: 0x9999}
	image, err := service.RetailFeatureImage()
	if err != nil {
		t.Fatal(err)
	}
	if len(image.Animating) != 0 || len(image.Normal) != 0 || len(image.ThreeD) != 0 {
		t.Fatalf("non-family animation was emitted: %#v", image)
	}
}
