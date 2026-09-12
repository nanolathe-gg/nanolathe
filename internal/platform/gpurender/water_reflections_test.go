package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Authored geometry separates physical height from depth key: a high-key
// submerged face must not reflect, and a hidden tall face must not leak out.
func checkWaterReflectionDevicePixels() error {
	pal := fixturePalette()
	const w, h = 160, 120
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	ter := waterFixtureTerrain()
	body := func(height float32, key int32) *drawlist.ModelGeometry {
		f := directFace(0, 0, 20, 12, 210, key, key)
		for i := range f.Vertices {
			f.Vertices[i].Height = height
		}
		g := directSubject(30, 20, 20, 12, f)
		g.ReflectWater = true
		g.WorldHeight = 16
		g.ReflectionSea = 16
		return g
	}
	read := func(g *drawlist.ModelGeometry, on bool, tick uint32, zoom bool, occlude bool, line bool) []byte {
		var l drawlist.List
		l.RecordClear()
		if zoom {
			l.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit * 3 / 4, Step: camera.ViewScaleNative, RecordW: 214, RecordH: 160})
		}
		l.RecordTerrain(drawlist.Terrain{Terrain: ter, DstW: 214, DstH: 160, Scale: camera.ViewScaleNative, Water: drawlist.WaterSurface{Enabled: true, Tick: tick, Energy: .7}})
		if g != nil {
			l.RecordModel(drawlist.Model{Geometry: g})
		}
		if line {
			f := &formats.GAFFrame{Width: 12, Height: 8, Pixels: make([]byte, 96), Transparent: make([]bool, 96)}
			for i := range f.Pixels {
				f.Pixels[i] = 240
				f.Transparent[i] = i%12 < 2
			}
			l.RecordSprite(drawlist.Sprite{Frame: f, X: 64, Y: 16, Kind: drawlist.BlitKeyed, ReflectWater: true, ReflectionHeight: 48})
			l.RecordLine(drawlist.Line{X0: 90, Y0: 15, X1: 155, Y1: 15, Index: 240, ReflectWater: true, ReflectionHeight0: 45, ReflectionHeight1: 45})
		}
		if occlude {
			l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 20, Y: 42, W: 45, H: 40}, Index: 7})
		}
		if zoom {
			l.RecordWorld(drawlist.WorldSpace{})
		}
		l.RecordExpand()
		cloned := l.Clone()
		r.SetWaterReflections(on)
		img := r.Execute(&cloned, w, h)
		p := make([]byte, w*h*4)
		img.ReadPixels(p)
		return p
	}
	for _, zoom := range []bool{false, true} {
		g := body(30, 10)
		off, on := read(g, false, 30, zoom, false, false), read(g, true, 30, zoom, false, false)
		if bytes.Equal(off, on) {
			return fmt.Errorf("above-water model reflection absent, zoom=%v", zoom)
		}
		if !bytes.Equal(on, read(g, true, 30, zoom, false, false)) {
			return fmt.Errorf("reflection phase replay differs")
		}
		// Ordinary water also animates; compare the reflected/off pair at each phase.
		late, lateOff := read(g, true, 45, zoom, false, false), read(g, false, 45, zoom, false, false)
		same := true
		for i := range on {
			if int(on[i])-int(off[i]) != int(late[i])-int(lateOff[i]) {
				same = false
				break
			}
		}
		if same {
			return fmt.Errorf("reflection distortion is static")
		}
		if !bytes.Equal(read(g, true, 30, zoom, true, false), read(g, false, 30, zoom, true, false)) {
			return fmt.Errorf("reflection painted over foreground object")
		}
		submerged := body(-5, 240)
		if !bytes.Equal(read(submerged, true, 30, zoom, false, false), read(submerged, false, 30, zoom, false, false)) {
			return fmt.Errorf("comparison key mistaken for physical height")
		}
		hidden := body(48, 10)
		top := directFace(0, 0, 20, 12, 210, 200, 200)
		for i := range top.Vertices {
			top.Vertices[i].Height = -5
		}
		hidden.Faces = append(hidden.Faces, top)
		if !bytes.Equal(read(hidden, true, 30, zoom, false, false), read(hidden, false, 30, zoom, false, false)) {
			return fmt.Errorf("hidden model piece leaked into reflection")
		}
	}
	// A sloping triangle samples its own stored key at a texel centre. Using
	// the interpolated key at another subpixel point produces reflection holes.
	triangle := body(30, 10)
	triangle.Faces[0].Vertices = triangle.Faces[0].Vertices[:3]
	triangle.Faces[0].Vertices[1].Key = 90
	triangle.Faces[0].Vertices[2].Key = 90
	triOff, triOn := read(triangle, false, 30, false, false, false), read(triangle, true, 30, false, false, false)
	if bytes.Equal(triOff, triOn) {
		return fmt.Errorf("sloping triangle self-rejected")
	}
	off, on := read(nil, false, 30, false, false, true), read(nil, true, 30, false, false, true)
	for _, band := range []struct {
		name   string
		x0, x1 int
	}{{"sprite", 64, 76}, {"stroke", 92, 115}} {
		changed := false
		for y := 48; y < 70; y++ {
			for x := band.x0; x < band.x1; x++ {
				i := (y*w + x) * 4
				changed = changed || !bytes.Equal(off[i:i+4], on[i:i+4])
			}
		}
		if !changed {
			return fmt.Errorf("%s projectile reflection absent", band.name)
		}
	}
	for y := 0; y < h; y++ {
		for x := 140; x < w; x++ {
			i := (y*w + x) * 4
			if !bytes.Equal(off[i:i+4], on[i:i+4]) {
				return fmt.Errorf("projectile reflection crossed onto land")
			}
		}
	}
	if !bytes.Equal(read(nil, true, 30, false, false, false), read(nil, false, 30, false, false, false)) {
		return fmt.Errorf("old reflection persisted without source")
	}
	r.ResetSources()
	if r.reflections.source != nil || len(r.reflections.verts) != 0 {
		return fmt.Errorf("reflection storage survived source reset")
	}
	return nil
}

// Equal-height points on a diagonal hull retain their footprint direction;
// increasing height reverses only the camera's half-height shear. A whole-image
// flip incorrectly reverses the hull's lengthwise slope as well.
func TestReflectionPreservesHullFootprint(t *testing.T) {
	for _, recordScale := range []int32{1, 2} {
		for _, rasterScale := range []int32{1, 2} {
			for _, direction := range [][2]int32{{40, 20}, {-40, 20}, {40, -20}, {-40, -20}, {0, 40}, {40, 0}} {
				points := [4][3]int32{{0, 0, 6}, {direction[0], direction[1], 6}, {direction[0], direction[1], 16}, {0, 0, 16}}
				var f drawlist.ModelFace
				for _, p := range points {
					f.Vertices = append(f.Vertices, drawlist.ModelVertex{X: p[0] * recordScale * rasterScale, Y: (p[1] - p[2]/2) * recordScale * rasterScale, Height: float32(p[2] * recordScale), Key: 30})
				}
				g := &drawlist.ModelGeometry{ReflectWater: true, WorldHeight: 100, ReflectionSea: 100}
				r := &Renderer{}
				r.reflections.active = g
				r.reflections.region = modelDirectRegion{x: 40, y: 60, bounds: image.Rect(100, 100, 300, 300)}
				r.reflectModelFace(&f, 40, 60, 2/float32(rasterScale), 0, 0, 0, 1, 0)
				if len(r.reflections.verts) != 4 {
					t.Fatal("missing reflected hull")
				}
				for i, p := range points {
					v := r.reflections.verts[i]
					wantX, wantY := float32(100+p[0]*recordScale), float32(100+(p[1]+p[2]/2)*recordScale)
					if v.DstX != wantX || v.DstY != wantY {
						t.Fatalf("direction=%v record=%d raster=%d corner=%d reflected (%v,%v), want footprint-preserving (%v,%v)", direction, recordScale, rasterScale, i, v.DstX, v.DstY, wantX, wantY)
					}
				}
			}
		}
	}
}
