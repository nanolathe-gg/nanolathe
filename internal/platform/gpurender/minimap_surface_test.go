package gpurender

import (
	"fmt"

	"github.com/nanolathe/nanolathe/formats"

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
	list.RecordSurface(drawlist.Surface{Pixels: []byte{0, 1, 2, 3, 4, 5}, SrcW: 3, SrcH: 2, Dst: drawlist.Rect{X: 1, Y: 1, W: 3, H: 2}, Identity: 1, Revision: 1})
	list.RecordSurface(drawlist.Surface{Pixels: []byte{9, 10, 11, 12}, SrcW: 2, SrcH: 2, Dst: drawlist.Rect{X: 5, Y: 2, W: 2, H: 2}, Identity: 2, Revision: 1})
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 2, Y: 1, W: 1, H: 1}, Index: 77})
	list.RecordSurface(drawlist.Surface{Pixels: []byte{7, 8, 9}, SrcW: 3, SrcH: 1, Dst: drawlist.Rect{Y: 5, W: 6, H: 1}, HasClip: true, Clip: drawlist.Rect{X: 2, Y: 5, W: 2, H: 1}})
	list.RecordSprite(drawlist.Sprite{Frame: &formats.GAFFrame{Width: 3, Height: 1, Pixels: []byte{13, 17, 23}, Transparent: make([]bool, 3)}, X: 4, Kind: drawlist.BlitLit, LightRow: 1, Pal: &pal, HasClip: true, Clip: drawlist.Rect{X: 5, W: 1, H: 1}})
	list.RecordExpand()
	img := r.Execute(&list, 8, 6)
	if img == nil {
		return fmt.Errorf("minimap surface device returned no image")
	}
	writes := r.surfaceWrites
	if r.Execute(&list, 8, 6) == nil || r.surfaceWrites != writes {
		return fmt.Errorf("unchanged durable surfaces uploaded again: %d -> %d", writes, r.surfaceWrites)
	}
	cacheWrites := r.surfaceWrites
	for identity := uint64(11); identity <= 15; identity++ {
		if e := r.uploadSurface(drawlist.Surface{Pixels: []byte{byte(identity)}, SrcW: 1, SrcH: 1, Identity: identity, Revision: 1}); !e.ok {
			return fmt.Errorf("durable surface %d did not upload", identity)
		}
	}
	if got, want := r.surfaceWrites, cacheWrites+5; got != want {
		return fmt.Errorf("same-size cache eviction skipped upload: writes=%d want %d", got, want)
	}
	rgba := make([]byte, 8*6*4)
	img.ReadPixels(rgba)
	// Every command here is opaque, so §13.4's exactness rule applies: the
	// composite must equal the classic byte plane expanded through PAL.
	expected := map[int]byte{5: pal.Light[256+17], 9: 0, 10: 77, 11: 2, 17: 3, 18: 4, 19: 5, 21: 9, 22: 10, 29: 11, 30: 12, 42: 8, 43: 8}
	for i := 0; i < 8*6; i++ {
		want, ok := expected[i]
		if !ok {
			want = 251
		}
		if err := checkExactIndex(fmt.Sprintf("minimap surface device pixel %d", i), rgba, i*4, &pal, want); err != nil {
			return err
		}
	}
	return nil
}
