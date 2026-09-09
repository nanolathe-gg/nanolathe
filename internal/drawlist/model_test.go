package drawlist

import "testing"

func TestModelGeometryCloneOwnsFaceStorage(t *testing.T) {
	geometry := &ModelGeometry{
		Eligible: true,
		Faces: []ModelFace{{
			Color:    7,
			Vertices: []ModelVertex{{X: 1, Y: 2, Key: -3, U: 4, V: 5, Shade: 6}, {X: 7, Y: 8, Key: 9}},
		}},
		LiveFaces: []ModelFace{{Vertices: []ModelVertex{{X: 10, Y: 11, Key: 12}}}},
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
	geometry.LiveFaces[0].Vertices[0].X = 99
	if got := clone.model[0].Geometry.LiveFaces[0].Vertices[0].X; got != 10 {
		t.Fatalf("cloned geometry aliases live face vertices: X=%d, want 10", got)
	}
	classic.Body.Color[0], classic.Body.Coverage[0], classic.Body.Key[0] = 99, false, 99
	if got := clone.model[0].Classic.Body.Color[0]; got != 8 {
		t.Fatalf("cloned classic color aliases recorder storage: %d", got)
	}
	if !clone.model[0].Classic.Body.Coverage[0] || clone.model[0].Classic.Body.Key[0] != 50 {
		t.Fatal("cloned classic coverage or key aliases recorder storage")
	}
}

// C-G5: recording a second subject or resetting the source list cannot change
// a retained frame's owned body, shadow, placement, or painter-order key mode.
func TestClassicImageStoragePreservesRetainedFrame(t *testing.T) {
	var list List
	source := ClassicModelImage{Color: []byte{7}, Coverage: []bool{true}, Key: []byte{80}, Width: 1, Height: 1, AnchorX: 3}
	body := list.CopyClassicImage(source)
	source.Color[0], source.Coverage[0], source.Key[0] = 9, false, 90
	shadow := list.CopyClassicImage(source)
	if body.Color[0] != 7 || !body.Coverage[0] || body.Key[0] != 80 {
		t.Fatal("recorded body aliases scratch or another subject")
	}
	list.RecordModel(Model{Classic: &ClassicModel{Body: body, Shadow: shadow}})
	saved := list.Clone()
	list.Reset()
	list.CopyClassicImage(ClassicModelImage{Color: []byte{1, 2}, Coverage: []bool{true, true}, Width: 2, Height: 1})
	packet := saved.model[0].Classic
	if packet.Body.Color[0] != 7 || packet.Body.Key[0] != 80 || packet.Body.AnchorX != 3 || packet.Shadow.Color[0] != 9 {
		t.Fatal("reset overwrote retained classic planes or placement")
	}
	list.Reset()
	keyless := list.CopyClassicImage(ClassicModelImage{Color: []byte{4}, Coverage: []bool{true}, Width: 1, Height: 1})
	if keyless.Key != nil {
		t.Fatal("keyless subject acquired a stale height plane")
	}
}
