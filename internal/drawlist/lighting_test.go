package drawlist

import "testing"

func TestLightingMetadataSurvivesOwnedReplayCopy(t *testing.T) {
	var list List
	list.RecordSprite(Sprite{LightingKind: SpriteLightingExplosion, LightingSize: 80, WorldHeight: 12, LightingScale: 1.5})
	list.RecordSprite(Sprite{LightingKind: SpriteLightingSmoke, WorldHeight: 7, LightingScale: 1.5})
	list.RecordModel(Model{Geometry: &ModelGeometry{WorldHeight: 12, Faces: []ModelFace{{Normal: [3]float32{0, 0, 1}, Vertices: []ModelVertex{{Height: 3}}}}}})
	copy := list.Clone()
	var kinds []SpriteLightingKind
	copy.VisitSprites(func(s Sprite) {
		kinds = append(kinds, s.LightingKind)
		if s.LightingKind == SpriteLightingExplosion && s.LightingSize != 80 {
			t.Fatal("explosion sequence extent was lost")
		}
		if s.LightingScale != 1.5 {
			t.Fatal("sprite recording scale was lost")
		}
	})
	if len(kinds) != 2 || kinds[0] != SpriteLightingExplosion || kinds[1] != SpriteLightingSmoke {
		t.Fatalf("sprite identity/order changed: %v", kinds)
	}
	copy.VisitModels(func(m Model) {
		g := m.Geometry
		if g.WorldHeight != 12 || g.Faces[0].Normal != [3]float32{0, 0, 1} || g.Faces[0].Vertices[0].Height != 3 {
			t.Fatal("owned model copy lost lighting metadata")
		}
	})
}
