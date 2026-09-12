package gpurender

import (
	"bytes"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// User-requested artistic presentation: emission adds no submissions, heat
// reuses one distortion resolve, and transparent model texels stay transparent.
func checkWreckHeatDevicePixels() error {
	const w, h = 240, 180
	pal := fixturePalette()
	pal.Base[90] = [4]byte{60, 60, 60, 255}
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	makeList := func(emission [3]float32, strength float32, count int) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit, Step: camera.ViewScaleNative, RecordW: w, RecordH: h})
		for y := 0; y < h; y += 8 {
			for x := 0; x < w; x += 8 {
				l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: int32(x), Y: int32(y), W: 8, H: 8}, Index: uint8(50 + ((x/8+y/8)%2)*120)})
			}
		}
		for i := 0; i < count; i++ {
			g := directSubject(90, 100, 60, 40, directFace(0, 0, 60, 40, 90, 10, 10), directFace(20, 10, 40, 30, 1, 20, 20))
			g.WreckEmission = emission
			g.WreckHeatStrength, g.WreckHeatScale, g.WreckHeatTime = strength, 1, 4
			l.RecordModel(drawlist.Model{Geometry: g})
		}
		l.RecordWorld(drawlist.WorldSpace{})
		l.RecordExpand()
		return l
	}
	read := func(l drawlist.List) ([]byte, ModelStats) {
		p := make([]byte, w*h*4)
		r.Execute(&l, w, h).ReadPixels(p)
		return p, r.ModelStats()
	}
	cold, cs := read(makeList([3]float32{}, 0, 1))
	hot, hs := read(makeList([3]float32{.9, .75, .435}, 0, 1))
	if bytes.Equal(cold, hot) || cs.DeviceDraws != hs.DeviceDraws || cs.Passes != hs.Passes {
		return fmt.Errorf("wreck emission missing or added GPU submissions: cold=%+v hot=%+v", cs, hs)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x >= 89 && x <= 150 && y >= 99 && y <= 140 && !(x > 112 && x < 128 && y > 112 && y < 128) {
				continue
			}
			at := (y*w + x) * 4
			if !bytes.Equal(cold[at:at+4], hot[at:at+4]) {
				return fmt.Errorf("wreck glow escaped model coverage at %d,%d", x, y)
			}
		}
	}
	heat, ss := read(makeList([3]float32{.9, .75, .435}, .55, 1))
	if bytes.Equal(heat, hot) || ss.WreckHeatPlumes != 1 || ss.DeviceDraws != hs.DeviceDraws+2 {
		return fmt.Errorf("wreck shimmer failed shared-pass budget: %+v", ss)
	}
	// Tiny positive amplitude must remain heat rather than alias the blast tag.
	tiny, _ := read(makeList([3]float32{.9, .75, .435}, .000001, 1))
	if !bytes.Equal(tiny, hot) {
		return fmt.Errorf("near-cold wreck selected a different distortion family")
	}
	again, _ := read(makeList([3]float32{}, 0, 1))
	if !bytes.Equal(cold, again) {
		return fmt.Errorf("cold wreck retained heat")
	}
	_, crowd := read(makeList([3]float32{}, .55, 40))
	if crowd.WreckHeatPlumes != 32 {
		return fmt.Errorf("wreck shimmer cap=%d", crowd.WreckHeatPlumes)
	}
	return nil
}
