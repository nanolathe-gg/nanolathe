package gpurender

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// Enhanced camera presentation retains the same fractional translation at
// native/detail rest factors and between quantized zoom values (design §16.5).
func TestWorldAffineRetainsPreciseFactorAndRestTranslation(t *testing.T) {
	for _, tc := range []struct {
		factor float32
		step   camera.ViewScale
	}{{1, camera.ViewScaleNative}, {2, camera.ViewScaleDetail}, {1.50025, camera.ViewScaleDetail}} {
		r, _ := schedulerFixture(t)
		r.sched.resetFrame(64, 64)
		r.World(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit, Factor: tc.factor, Step: tc.step, OffsetX: -.375, OffsetY: .625, RecordW: 128, RecordH: 128})
		r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 8, Y: 12, W: 16, H: 20}, Index: 3})
		k := tc.factor / float32(tc.step.Float())
		want := []float32{8*k - .375, 12*k + .625, 24*k - .375, 32*k + .625}
		got := quadCorners(r)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("factor %v step %v: corners %v want %v", tc.factor, tc.step, got, want)
			}
		}
		if r.sched.txf(4) != 4*k {
			t.Fatal("translation changed a length")
		}
		ox, oy := r.sched.inverseOrigin(100, 200, tc.factor)
		if math.Abs(float64(ox+(8*k-.375)/tc.factor-(100+8/float32(tc.step.Float())))) > 1e-5 || math.Abs(float64(oy+(12*k+.625)/tc.factor-(200+12/float32(tc.step.Float())))) > 1e-5 {
			t.Fatal("inverse sampling differs from forward placement")
		}
		r.World(drawlist.WorldSpace{})
		if r.sched.worldOn || r.sched.txx(8) != 8 || r.sched.txy(12) != 12 {
			t.Fatal("world translation escaped into interface")
		}
	}
}

func TestTranslatedLitPointsSampleEachFramebufferPixelOnce(t *testing.T) {
	for _, k := range []float32{1, .75, .3125} {
		for _, offset := range [][2]float32{{.75, -.25}, {-.75, .25}, {.25, .75}} {
			r, _ := schedulerFixture(t)
			r.sched.resetFrame(64, 64)
			r.World(drawlist.WorldSpace{Begin: true, Factor: k, Step: camera.ViewScaleNative, OffsetX: offset[0], OffsetY: offset[1], RecordW: 64, RecordH: 64})
			var pts []drawlist.Point
			for y := 0; y < 24; y++ {
				for x := 0; x < 24; x++ {
					pts = append(pts, drawlist.Point{X: int32(x), Y: int32(y), Index: 20})
				}
			}
			r.Points(drawlist.Points{Kind: drawlist.PointLit, Points: pts})
			cover := litCoverage(r, 64, 64)
			for y := 0; y < 32; y++ {
				for x := 0; x < 32; x++ {
					rx, ry := math.Floor((float64(x)+.5-float64(offset[0]))/float64(k)), math.Floor((float64(y)+.5-float64(offset[1]))/float64(k))
					want := 0
					if rx >= 0 && ry >= 0 && rx < 24 && ry < 24 {
						want = 1
					}
					if got := cover[[2]int{x, y}]; got != want {
						t.Fatalf("scale %v offset %v pixel %d,%d lit %d times, want %d", k, offset, x, y, got, want)
					}
				}
			}
		}
	}
}

// Translating the complete world by whole framebuffer pixels must translate
// the resulting image, even with a fractional baseline and inverse map reads.
// The HUD stays fixed. Assets are synthetic; no retail bytes are required.
func checkWorldAffineDevicePixels() error {
	const w, h = 160, 120
	pal := fixturePalette()
	pal.Base[100] = [4]byte{30, 60, 90, 255}
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	ter := waterFixtureTerrain()
	art := &formats.GAFFrame{Width: 8, Height: 8, Pixels: make([]byte, 64)}
	for i := range art.Pixels {
		art.Pixels[i] = byte(140 + i%8)
	}
	fogArt := &formats.GAFFrame{Width: 16, Height: 16, XOffset: -16, YOffset: -16, Pixels: make([]byte, 256), Transparent: make([]bool, 256)}
	for y := 0; y < 16; y++ {
		for x := 8; x < 16; x++ {
			fogArt.Transparent[y*16+x] = true
		}
	}
	entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: fogArt}}}
	for _, factor := range []float32{1, .8125} {
		for _, fog := range []bool{false, true} {
			read := func(dx, dy float32) []byte {
				var l drawlist.List
				l.RecordClear()
				l.RecordWorld(drawlist.WorldSpace{Begin: true, Factor: factor, Step: camera.ViewScaleNative, OffsetX: .25 + dx, OffsetY: -.25 + dy, RecordW: 200, RecordH: 160})
				l.RecordTerrain(drawlist.Terrain{Terrain: ter, OriginX: 16, OriginY: 16, DstW: 200, DstH: 160, Scale: camera.ViewScaleNative, Water: drawlist.WaterSurface{Enabled: true, Tick: 30, Energy: .7, DriftX: 1}})
				l.RecordSurfaceWakes(drawlist.SurfaceWakes{Marks: []drawlist.SurfaceWake{{X: 100, Y: 50, AxisX: 38, CrossY: 12, Alpha: .5, Age: .3, Foam: true}}})
				l.RecordSprite(drawlist.Sprite{Frame: art, X: 48, Y: 50, Kind: drawlist.BlitKeyed})
				l.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true, fixtureFace(72, 74, 15, 11, 20, 180))})
				l.RecordScorchMarks(drawlist.ScorchMarks{Marks: []drawlist.ScorchMark{{X: 130, Y: 76, Radius: 20, Age: 2, Variant: 13}}})
				l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 25, Y: 70, W: 20, H: 10}, Index: 100})
				if fog {
					op := fogOpAtScale(2, 2, 16, 16, camera.ViewScaleNative, render.FogKindGAFCh0)
					l.RecordFog(drawlist.Fog{Ops: []render.FogOp{op}, Gray: [4]*formats.GAFEntry{entry}, Black: [4]*formats.GAFEntry{entry}})
				}
				l.RecordWorld(drawlist.WorldSpace{})
				l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 140, Y: 5, W: 10, H: 10}, Index: 211})
				l.RecordExpand()
				p := make([]byte, w*h*4)
				r.Execute(&l, w, h).ReadPixels(p)
				return p
			}
			base, shift := read(0, 0), read(8, 6)
			if dir := os.Getenv("NANOLATHE_AFFINE_SHOTS"); dir != "" {
				for i, pix := range [][]byte{base, shift} {
					if err := saveAircraftCapture(filepath.Join(dir, fmt.Sprintf("affine-%v-fog-%t-%d.png", factor, fog, i)), &image.RGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}); err != nil {
						return err
					}
				}
			}
			changed := false
			for y := 18; y < 100; y++ {
				for x := 18; x < 125; x++ {
					a, b := ((y+6)*w+x+8)*4, (y*w+x)*4
					for c := 0; c < 4; c++ {
						d := int(shift[a+c]) - int(base[b+c])
						if d < -1 || d > 1 {
							return fmt.Errorf("affine world factor %v fog %v sample %d,%d channel %d: shifted %d baseline %d", factor, fog, x, y, c, shift[a+c], base[b+c])
						}
					}
					changed = changed || shift[b] != base[b]
				}
			}
			if !changed {
				return fmt.Errorf("affine fixture did not move")
			}
			for y := 5; y < 15; y++ {
				for x := 140; x < 150; x++ {
					i := (y*w + x) * 4
					for c := 0; c < 4; c++ {
						if base[i+c] != shift[i+c] {
							return fmt.Errorf("world transform moved HUD")
						}
					}
				}
			}
		}
	}
	return nil
}

// These screen-space effects bypass scheduler geometry. Their centres and clips
// translate, while heights/radii remain lengths (Enhanced design §16.3).
func TestTranslatedEffectsKeepTheirSizes(t *testing.T) {
	r := &Renderer{w: 200, h: 160}
	r.sched.setWorldTransform(.75, .375, -.625)
	r.lighting.lights = []battleLight{{position: [3]float32{80, 80, 20}, radius: 40}}
	r.appendGroundLights()
	v := r.ground.verts[0]
	if v.Custom0 != 60.375 || v.Custom1 != 51.875 || v.Custom2 != 15 || v.Custom3 != 30 {
		t.Fatalf("translated ground source: %+v", v)
	}
	r.distortion.candidates = []blastWave{{x: 80, y: 80, radius: 20, width: 4, strength: 2, hasClip: true, clip: drawlist.Rect{X: 60, Y: 60, W: 40, H: 40}}}
	r.appendBlastWaves()
	v = r.distortion.verts[0]
	if v.Custom0 != 60.375 || v.Custom1 != 59.375 || v.Custom2 != 15 || v.Custom3 != 3 || v.ColorR != 45.375 || v.ColorG != 44.375 {
		t.Fatalf("translated blast centre/clip/length: %+v", v)
	}
	r.heat.sources = []treeHeatSource{{x: 60, y: 70, width: 20, height: 30, scale: 1, strength: 1, hasClip: true, clip: drawlist.Rect{X: 50, Y: 50, W: 100, H: 100}}}
	r.distortion.verts = nil
	r.appendTreeHeat()
	v = r.distortion.verts[0]
	if v.Custom0 != 52.875 || v.Custom1 != r.sched.txy(83.5) || v.ColorR != 37.875 || v.ColorG != 36.875 {
		t.Fatalf("translated tree heat centre/clip: %+v", v)
	}
	x0, y0, x1, y1 := r.sched.txRect(-1, -1, 2, 2)
	if x0 != -1 || y0 != -2 || x1 != 2 || y1 != 1 {
		t.Fatalf("conservative bounds = %d,%d..%d,%d", x0, y0, x1, y1)
	}
}
