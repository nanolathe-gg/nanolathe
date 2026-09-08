package drawlist

import "testing"

func TestModelGeometryCloneOwnsFaceStorage(t *testing.T) {
	geometry := &ModelGeometry{
		Eligible: true,
		Faces: []ModelFace{{
			Color:    7,
			Vertices: []ModelVertex{{X: 1, Y: 2, Key: -3, U: 4, V: 5, Shade: 6}, {X: 7, Y: 8, Key: 9}},
		}},
	}
	classic := &ClassicModel{Body: &ClassicModelImage{
		Color: []byte{8, 9}, Coverage: []bool{true, false}, Key: []byte{50, 51},
		Width: 2, Height: 1, AnchorX: 4, AnchorY: 5,
	}}
	var list List
	list.RecordModel(Model{Classic: classic, Geometry: geometry})
	clone := list.Clone()

	geometry.Faces[0].Vertices[0].X = 99
	if got := clone.model[0].Geometry.Faces[0].Vertices[0].X; got != 1 {
		t.Fatalf("cloned geometry aliases recorded face vertices: X=%d, want 1", got)
	}
	if got := len(clone.model[0].Geometry.Faces[0].Vertices); got != 2 {
		t.Fatalf("cloned face arity = %d, want 2", got)
	}
	if got := clone.model[0].Geometry.Faces[0].Vertices[0].Key; got != -3 {
		t.Fatalf("cloned pre-interpolation key = %d, want -3", got)
	}
	classic.Body.Color[0], classic.Body.Coverage[0], classic.Body.Key[0] = 99, false, 99
	if got := clone.model[0].Classic.Body.Color[0]; got != 8 {
		t.Fatalf("cloned classic color aliases recorder storage: %d", got)
	}
	if !clone.model[0].Classic.Body.Coverage[0] || clone.model[0].Classic.Body.Key[0] != 50 {
		t.Fatal("cloned classic coverage or key aliases recorder storage")
	}
}
