package drawlist

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
)

func TestLensMapTruncationAndRelativeOffsets(t *testing.T) {
	l := Lens{X: 11, Y: 11}
	for _, tc := range []struct {
		x, y, sx, sy int32
		ok           bool
	}{
		{11, 11, 11, 11, true}, {10, 10, 11, 11, true}, {9, 9, 10, 10, true},
		{8, 11, 8, 11, true}, {7, 9, 7, 9, true}, {7, 8, 0, 0, false}, {6, 11, 0, 0, false},
	} {
		x, y, ok := l.Source(tc.x, tc.y)
		if x != tc.sx || y != tc.sy || ok != tc.ok {
			t.Fatalf("cell(%d,%d)=(%d,%d,%v), want(%d,%d,%v)", tc.x, tc.y, x, y, ok, tc.sx, tc.sy, tc.ok)
		}
	}
	// Each native sample retains both pixel positions when doubled.
	l.Scale = camera.ViewScaleDetail
	l.X = 22
	l.Y = 22
	for _, sub := range []int32{0, 1} {
		x, y, ok := l.Source(20+sub, 20+sub)
		if !ok || x != 22+sub || y != 22+sub {
			t.Fatalf("detail subpixel %d=(%d,%d,%v)", sub, x, y, ok)
		}
	}
}

type lensRecorder struct {
	recorder
	lenses []Lens
}

func (r *lensRecorder) Lens(l Lens) { r.tags = append(r.tags, "lens"); r.lenses = append(r.lenses, l) }
func TestLensReplayClonePreservesInterleavedOrder(t *testing.T) {
	var list List
	list.RecordLine(Line{})
	list.RecordLens(Lens{X: 20, Key: 17})
	list.RecordLine(Line{})
	list.RecordLens(Lens{X: 21, Key: 18})
	clone := list.Clone()
	list.Reset()
	list.RecordLens(Lens{X: 99})
	var got lensRecorder
	clone.Replay(&got)
	if !reflect.DeepEqual(got.tags, []string{"line", "lens", "line", "lens"}) || len(got.lenses) != 2 || got.lenses[0].Key != 17 || got.lenses[1].X != 21 {
		t.Fatalf("clone lost lens order or values: %+v", got)
	}
}
