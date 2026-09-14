package gpurender

import (
	"bytes"
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"testing"
)

func TestAircraftShadowShaderCompiles(t *testing.T) {
	if _, err := newAircraftShadowShader(); err != nil {
		t.Fatal(err)
	}
}

// Authored Enhanced tuning: landing and lower flight retain the reviewed
// curve; upper flight reaches a wider, bounded penumbra at every view scale.
func TestAircraftShadowUpperAltitudeSoftness(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2} {
		for _, tc := range []struct{ height, radius float32 }{{0, 0}, {60, 1}, {120, 2}, {200, 4.5}, {500, 4.5}} {
			if got := aircraftShadowRadius(tc.height*scale, scale); got != tc.radius*scale {
				t.Fatalf("height=%v scale=%v: radius=%v want=%v", tc.height, scale, got, tc.radius*scale)
			}
		}
	}
}

// These relationships lock the Enhanced filter, not a retail shadow contract.
// Real GPU pixels cover atlas isolation, pause, receiving medium and zoom.
func checkAircraftShadowDevicePixels() error {
	pal := fixturePalette()
	const w, h = 200, 180
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	terrain := waterFixtureTerrain()
	dryTerrain := waterFixtureTerrain()
	dryTerrain.SeaLevel = 0
	body := directSubject(40, 10, 40, 12, directFace(0, 0, 40, 12, 210, 100, 100))
	body.Shadow = &drawlist.ModelGeometry{Silhouette: true, Width: 40, Height: 12, AnchorX: 40, AnchorY: 80, Scale: 1}
	body.AircraftShadowScale = 1
	render := func(height float32, water bool, tick uint32, zoom bool, neighbor bool) []byte {
		body.AircraftShadowHeight = height
		var l drawlist.List
		l.RecordClear()
		if zoom {
			l.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit * 3 / 4, Step: camera.ViewScaleNative, RecordW: 267, RecordH: 240})
		}
		receiver := dryTerrain
		if water {
			receiver = terrain
		}
		l.RecordTerrain(drawlist.Terrain{Terrain: receiver, DstW: 267, DstH: 240, Scale: camera.ViewScaleNative, Water: drawlist.WaterSurface{Enabled: true, Tick: tick}})
		// Stable background isolates the shadow from the animated water itself.
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 267, H: 240}, Index: 200})
		l.RecordModel(drawlist.Model{Geometry: body})
		if neighbor {
			l.RecordModel(drawlist.Model{Geometry: directSubject(120, 10, 40, 12, directFace(0, 0, 40, 12, 230, 100, 100))})
		}
		if zoom {
			l.RecordWorld(drawlist.WorldSpace{})
		}
		l.RecordExpand()
		img := r.Execute(&l, w, h)
		p := make([]byte, w*h*4)
		img.ReadPixels(p)
		return p
	}
	for _, zoom := range []bool{false, true} {
		low := render(1, false, 30, zoom, false)
		high := render(200, false, 30, zoom, false)
		if bytes.Equal(low, high) {
			return fmt.Errorf("aircraft shadow failed height softening, zoom=%v", zoom)
		}

		// A normalized filter spreads the footprint without adding shadow mass.
		lowMass, highMass, lowMoment, highMoment := 0.0, 0.0, 0.0, 0.0
		centre := 86.0
		if zoom {
			centre *= 0.75
		}
		for y := 45; y < 115; y++ {
			for x := 15; x < 100; x++ {
				a, b := float64(200-low[(y*w+x)*4]), float64(200-high[(y*w+x)*4])
				lowMass += a
				highMass += b
				lowMoment += a * (float64(y) - centre) * (float64(y) - centre)
				highMoment += b * (float64(y) - centre) * (float64(y) - centre)
			}
		}
		if highMass < lowMass*0.95 || highMass > lowMass*1.05 || highMoment/highMass <= lowMoment/lowMass {
			return fmt.Errorf("aircraft blur changed mass or failed to widen: mass %v/%v moment %v/%v", lowMass, highMass, lowMoment/lowMass, highMoment/highMass)
		}
		// Neighbouring atlas subjects must not enter even the outermost blur taps.
		neighbour := render(200, false, 30, zoom, true)
		if !bytes.Equal(high[60*w*4:120*w*4], neighbour[60*w*4:120*w*4]) {
			return fmt.Errorf("aircraft shadow sampled neighboring atlas subject")
		}
		if !bytes.Equal(high, render(200, false, 80, zoom, false)) {
			return fmt.Errorf("dry aircraft shadow moved with water time")
		}
		wet := render(200, true, 30, zoom, false)
		if !bytes.Equal(wet, render(200, true, 30, zoom, false)) {
			return fmt.Errorf("aircraft shadow changed on frozen replay")
		}
		late := render(200, true, 80, zoom, false)
		if bytes.Equal(wet, late) {
			return fmt.Errorf("water aircraft shadow distortion is static, zoom=%v", zoom)
		}
		dryDark, wetDark := 0, 0
		for y := 55; y < 115; y++ {
			for x := 20; x < 95; x++ {
				i := (y*w + x) * 4
				dryDark += 200 - int(high[i])
				wetDark += 200 - int(wet[i])
			}
		}
		if dryDark <= 0 || wetDark >= dryDark*3/4 {
			return fmt.Errorf("water shadow not weaker: dry=%d wet=%d zoom=%v", dryDark, wetDark, zoom)
		}
	}
	return nil
}
