package drawlist

import "testing"

func TestCloneMarkersOwnPlacementAndShareImmutableAtlas(t *testing.T) {
	atlas := &MarkerAtlas{Width: 1, Height: 1, Pixels: []byte{255, 0, 0, 0}}
	marks := []Marker{{IconAtlas: atlas, IconRect: Rect{W: 1, H: 1}, X: 20, Size: 20, Alpha: 128, Selected: true}}
	var l List
	l.RecordMarkers(Markers{Marks: marks})
	c := l.Clone()
	l.Reset()
	marks[0] = Marker{X: 99}
	got := c.MarkerBatches()[0].Marks[0]
	if got.X != 20 || !got.Selected || got.Alpha != 128 || got.IconAtlas != atlas || &got.IconAtlas.Pixels[0] != &atlas.Pixels[0] {
		t.Fatal("retained marker lost placement or immutable atlas identity")
	}
}
