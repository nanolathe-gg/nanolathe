package gpurender

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// The fixture's only opaque pixel lies beyond a cell ending exactly at the
// upper-left framebuffer boundary [03 R-RR16-A §3]. Both atlas and ordered
// composite execution must preserve that overhang.
func checkFogOffscreenOverhangDevicePixelsAt(scale camera.ViewScale) error {
	for _, composite := range []bool{false, true} {
		for _, gray := range []bool{false, true} {
			pal := fixturePalette()
			pal.Base[100] = [4]byte{30, 60, 90, 255}
			pal.Base[3] = [4]byte{10, 20, 30, 255}
			r, err := NewChecked(&pal, 8, 8)
			if err != nil {
				return err
			}
			leaf := &formats.GAFFrame{Width: 1, Height: 1, XOffset: -32, YOffset: -32, Pixels: []byte{3}, Transparent: []bool{false}}
			fr := leaf
			if composite {
				fr = &formats.GAFFrame{Width: 1, Height: 1, Subframes: []*formats.GAFFrame{leaf}}
			}
			entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: fr}}}
			kind := render.FogKindGAFCh0
			if gray {
				kind = render.FogKindGAFCh1
			}
			op := fogOpAtScale(0, 0, 48, 48, scale, kind)
			op.Variant, op.Frame = 0, 0
			var list drawlist.List
			list.RecordClear()
			list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 8, H: 8}, Index: 100, Style: drawlist.FillSolid})
			list.RecordFog(drawlist.Fog{Ops: []render.FogOp{op}, Gray: [4]*formats.GAFEntry{entry}, Black: [4]*formats.GAFEntry{entry}})
			list.RecordExpand()
			img := r.Execute(&list, 8, 8)
			pixels := make([]byte, 8*8*4)
			img.ReadPixels(pixels)
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					want := [4]byte{30, 60, 90, 255}
					if x < int(scale.Px(1)) && y < int(scale.Px(1)) {
						want = [4]byte{10, 20, 30, 255}
						if gray {
							want = [4]byte{60, 60, 60, 255}
						}
					}
					at := (y*8 + x) * 4
					if got := [4]byte(pixels[at : at+4]); got != want {
						return fmt.Errorf("fog overhang scale %d composite %v gray %v pixel (%d,%d)=%v, want %v", scale, composite, gray, x, y, got, want)
					}
				}
			}
		}
	}
	return nil
}
