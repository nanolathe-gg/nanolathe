package gpurender

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// The minimap producer now uses the existing Surface executor. Check real
// device ownership across successive uploads, opaque zero, and later markers.
func checkMinimapSurfaceDevicePixels() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 8, 6)
	if err != nil {
		return err
	}
	var list drawlist.List
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 8, H: 6}, Index: 251})
	list.RecordSurface(drawlist.Surface{Pixels: []byte{0, 1, 2, 3, 4, 5}, SrcW: 3, SrcH: 2, Dst: drawlist.Rect{X: 1, Y: 1, W: 3, H: 2}})
	list.RecordSurface(drawlist.Surface{Pixels: []byte{9, 10, 11, 12}, SrcW: 2, SrcH: 2, Dst: drawlist.Rect{X: 5, Y: 2, W: 2, H: 2}})
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 2, Y: 1, W: 1, H: 1}, Index: 77})
	list.RecordExpand()
	img := r.Execute(&list, 8, 6)
	if img == nil {
		return fmt.Errorf("minimap surface device returned no image")
	}
	rgba := make([]byte, 8*6*4)
	img.ReadPixels(rgba)
	expected := map[int]byte{9: 0, 10: 77, 11: 2, 17: 3, 18: 4, 19: 5, 21: 9, 22: 10, 29: 11, 30: 12}
	for i := 0; i < 8*6; i++ {
		want, ok := expected[i]
		if !ok {
			want = 251
		}
		if rgba[i*4] != want {
			return fmt.Errorf("minimap surface device pixel %d: got %d want %d", i, rgba[i*4], want)
		}
	}
	return nil
}
