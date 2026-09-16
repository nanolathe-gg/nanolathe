package client

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"testing"
)

// A one-pixel authored cloud reaches past each nominal cell edge. Its opacity
// is explicit, so this tests visible overhang rather than dimensions alone
// [03 R-RR16-A §3]. The child case also proves parents do not establish clips.
func TestFogWindowAndSinkRetainAuthoredOverhang(t *testing.T) {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		for _, tc := range []struct {
			name       string
			camX, camZ int32
			xoff, yoff int16
			px, py     int
		}{
			{"left", 48, 16, -32, 0, 0, 0},
			{"top", 16, 48, 0, -32, 0, 0},
			{"right", -48, 16, 1, 0, 63, 0},
			{"bottom", 16, -48, 0, 1, 0, 63},
		} {
			for _, composite := range []bool{false, true} {
				for _, gray := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/scale%d/composite%v/gray%v", tc.name, scale, composite, gray), func(t *testing.T) {
						size := int(scale.Px(64))
						c, err := New(Options{Width: size, Height: size})
						if err != nil {
							t.Fatal(err)
						}
						pal := &palette.Tables{}
						pal.Gray[200] = 100
						c.SetPalette(pal)
						for i := range c.indexed {
							c.indexed[i] = 200
						}
						cam := &camera.Camera{X: tc.camX, Z: tc.camZ, Scale: scale}
						c.SetCamera(cam)
						fr := &formats.GAFFrame{Width: 1, Height: 1, XOffset: tc.xoff, YOffset: tc.yoff, Pixels: []byte{0}, Transparent: []bool{false}, ColorKey: 9}
						if composite {
							fr = &formats.GAFFrame{Width: 1, Height: 1, Subframes: []*formats.GAFFrame{fr}}
						}
						entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: fr}}}
						c.fogGAF = &formats.GAF{}
						c.fogGray[2], c.fogBlack[2] = entry, entry
						ch0, ch1 := byte(1), byte(0)
						want := byte(0)
						if gray {
							ch0, ch1, want = 0, 1, 100
						}
						cache := visibility.NewFogCacheFromChannelsAt(1, 1, 0, 0, []byte{ch0}, []byte{ch1})
						ops := render.BuildFogOpsWindowWithArtInto(nil, cache, cam, int32(size), int32(size), pal, false, c.fogGray, c.fogBlack)
						if len(ops) != 1 {
							t.Fatalf("overhanging cell yielded %d ops", len(ops))
						}
						classicSink{c: c}.Fog(drawlist.Fog{Ops: ops, Gray: c.fogGray, Black: c.fogBlack})
						x, y := int(scale.Project(int32(tc.px))), int(scale.Project(int32(tc.py)))
						if got := c.indexed[y*size+x]; got != want {
							t.Fatalf("overhang (%d,%d)=%d, want %d", x, y, got, want)
						}
					})
				}
			}
		}
	}
}
