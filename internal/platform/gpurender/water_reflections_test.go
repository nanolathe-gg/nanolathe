package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Authored geometry separates physical height from depth key: a high-key
// submerged face must not reflect, and a hidden tall face must not leak out.
func checkWaterReflectionDevicePixels() error {
	pal := fixturePalette()
	const w, h = 160, 240
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
			l.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit * 3 / 4, Step: camera.ViewScaleNative, RecordW: 214, RecordH: 320})
		}
		l.RecordTerrain(drawlist.Terrain{Terrain: ter, DstW: 214, DstH: 320, Scale: camera.ViewScaleNative, Water: drawlist.WaterSurface{Enabled: true, Tick: tick, Energy: .7}})
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
	boatBeforeFlight := read(body(30, 10), true, 30, false, false, false)
	// Enhanced prototype: flight-height geometry must survive the former
	// 160-world-pixel fade cutoff, including when the view is zoomed out.
	for _, zoom := range []bool{false, true} {
		g := body(180, 10)
		on := read(g, true, 30, zoom, false, false)
		if bytes.Equal(on, read(g, false, 30, zoom, false, false)) {
			return fmt.Errorf("flight-height model reflection absent, zoom=%v", zoom)
		}
		if !bytes.Equal(on, read(g, true, 30, zoom, false, false)) {
			return fmt.Errorf("flight-height distortion changed on frozen replay, zoom=%v", zoom)
		}
	}
	if !bytes.Equal(boatBeforeFlight, read(body(30, 10), true, 30, false, false, false)) {
		return fmt.Errorf("flight blur metadata persisted into boat-only frame")
	}
	previousAlpha := 256
	for _, height := range []float32{30, 110, 200} {
		read(body(height, 10), true, 30, false, false, false)
		pixels := make([]byte, w*h*4)
		r.reflections.source.ReadPixels(pixels)
		peak := 0
		for i := 3; i < len(pixels); i += 4 {
			peak = max(peak, int(pixels[i]))
		}
		if peak == 0 || peak >= previousAlpha {
			return fmt.Errorf("reflection opacity did not decrease with physical height %v: %d after %d", height, peak, previousAlpha)
		}
		previousAlpha = peak
	}
	if err := checkReflectionBlurFootprint(r.reflections.softResolveShader); err != nil {
		return err
	}
	for _, variant := range []struct {
		name   string
		shader *ebiten.Shader
	}{{"resolve", r.reflections.resolveShader}, {"soft resolve", r.reflections.softResolveShader}} {
		if err := checkReflectionShorelineCoverage(variant.shader); err != nil {
			return fmt.Errorf("%s: %w", variant.name, err)
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
	if err := checkSurfaceImpactReflection(r, ter); err != nil {
		return err
	}
	r.ResetSources()
	if r.reflections.source != nil || r.reflections.height != nil || len(r.reflections.verts) != 0 {
		return fmt.Errorf("reflection storage survived source reset")
	}
	return nil
}

// A surface impact sits at sea level, so its reflection mirrors about its own
// anchor: the opaque upper half of the art lands below the anchor, where the
// art itself is keyed out, and the water mask still holds it off dry ground
// (GPU design §32). Height zero is the case the model waterline clip rejects.
func checkSurfaceImpactReflection(r *Renderer, ter *world.Terrain) error {
	const w, h = 160, 240
	const anchorX, anchorY, side = 40, 120, 16
	f := &formats.GAFFrame{Width: side, Height: side, XOffset: side / 2, YOffset: side / 2,
		Pixels: make([]byte, side*side), Transparent: make([]bool, side*side)}
	for i := range f.Pixels {
		f.Pixels[i] = 240
		// Only the half above the anchor carries fire; the lower half is keyed
		// out, which is where the mirrored upper half becomes visible.
		f.Transparent[i] = i/side >= side/2
	}
	read := func(on bool, dry bool) []byte {
		var l drawlist.List
		l.RecordClear()
		l.RecordTerrain(drawlist.Terrain{Terrain: ter, DstW: 214, DstH: 320, Scale: camera.ViewScaleNative, Water: drawlist.WaterSurface{Enabled: true, Tick: 30, Energy: .7}})
		x := int32(anchorX)
		if dry {
			x = 150
		}
		l.RecordSprite(drawlist.Sprite{Frame: f, X: x, Y: anchorY, Kind: drawlist.BlitKeyed, Anchored: true, ReflectWater: !dry, ReflectionHeight: 0})
		l.RecordExpand()
		r.SetWaterReflections(on)
		img := r.Execute(&l, w, h)
		p := make([]byte, w*h*4)
		img.ReadPixels(p)
		return p
	}
	off, on := read(false, false), read(true, false)
	changed := 0
	for y := anchorY + 1; y < anchorY+side/2; y++ {
		for x := anchorX - side/2; x < anchorX+side/2; x++ {
			i := (y*w + x) * 4
			if !bytes.Equal(off[i:i+4], on[i:i+4]) {
				changed++
			}
		}
	}
	if changed == 0 {
		return fmt.Errorf("surface impact cast no reflection below its anchor")
	}
	for y := 0; y < h; y++ {
		for x := 140; x < w; x++ {
			i := (y*w + x) * 4
			if !bytes.Equal(off[i:i+4], on[i:i+4]) {
				return fmt.Errorf("surface impact reflection crossed onto land at %d,%d", x, y)
			}
		}
	}
	// An impact standing on dry ground is never admitted, so the switch cannot
	// change a single byte of that frame.
	if !bytes.Equal(read(false, true), read(true, true)) {
		return fmt.Errorf("dry-ground impact was admitted as a reflection source")
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

// Authored filter fixture: increasing altitude metadata spreads the same bright
// stripe and reduces its peak without brightening it. No retail blur is claimed.
func checkReflectionBlurFootprint(shader *ebiten.Shader) error {
	const size = 64
	bounds := image.Rect(0, 0, size, size)
	mask := image.NewRGBA(bounds)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			mask.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	water := ebiten.NewImageFromImage(mask)
	defer water.Deallocate()
	target := ebiten.NewImage(size, size)
	defer target.Deallocate()
	render := func(patch, metadata *image.RGBA, zoom float32) []byte {
		source, heights := ebiten.NewImageFromImage(patch), ebiten.NewImageFromImage(metadata)
		defer source.Deallocate()
		defer heights.Deallocate()
		vertices := []ebiten.Vertex{{DstX: 0, DstY: 0}, {DstX: size, DstY: 0}, {DstX: size, DstY: size}, {DstX: 0, DstY: size}}
		for i := range vertices {
			vertices[i].ColorB = 1
			vertices[i].ColorA = 1
			vertices[i].Custom2 = zoom
			vertices[i].Custom3 = 1
		}
		target.Clear()
		target.DrawTrianglesShader(vertices, []uint16{0, 1, 2, 0, 2, 3}, shader, &ebiten.DrawTrianglesShaderOptions{Images: [4]*ebiten.Image{source, water, heights}})
		pixels := make([]byte, size*size*4)
		target.ReadPixels(pixels)
		return pixels
	}
	for _, stripeWidth := range []int{1, 4} {
		for _, zoom := range []float32{.75, 1, 2} {
			previousPeak, previousEnergy := 256, 0
			previousSpread := float64(-1)
			for _, strength := range []byte{0, 128, 255} {
				patch, metadata := image.NewRGBA(bounds), image.NewRGBA(bounds)
				for y := 20; y < 44; y++ {
					for x := 30; x < 30+stripeWidth; x++ {
						patch.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
						metadata.SetRGBA(x, y, color.RGBA{strength, 0, 0, 255})
					}
				}
				pixels := render(patch, metadata, zoom)
				peak, energy := 0, 0
				moment, mass, centroid := float64(0), float64(0), float64(0)
				for y := 0; y < size; y++ {
					for x := 0; x < size; x++ {
						a := int(pixels[(y*size+x)*4+3])
						energy += a
						peak = max(peak, a)
						mass += float64(a)
						centroid += float64(x * a)
						moment += float64(x * x * a)
					}
				}
				spread := moment/mass - (centroid/mass)*(centroid/mass)
				if strength > 0 && (spread <= previousSpread || peak > previousPeak || energy*5 < previousEnergy*4 || energy*4 > previousEnergy*5) {
					return fmt.Errorf("altitude blur failed stripe=%d zoom=%v strength=%d spread=%v/%v peak=%d/%d energy=%d/%d", stripeWidth, zoom, strength, spread, previousSpread, peak, previousPeak, energy, previousEnergy)
				}
				// Every row through the stripe must have one connected footprint.
				left, right := size, 0
				for x := 0; x < size; x++ {
					if pixels[(32*size+x)*4+3] > 1 {
						left = min(left, x)
						right = max(right, x)
					}
				}
				for x := left; x <= right; x++ {
					if pixels[(32*size+x)*4+3] == 0 {
						return fmt.Errorf("detached blur lobe: stripe=%d zoom=%v strength=%d", stripeWidth, zoom, strength)
					}
				}
				previousPeak, previousEnergy, previousSpread = peak, energy, spread
			}
		}
	}
	// Red boat and adjacent green aircraft: aircraft blur must not alter red.
	patch, metadata := image.NewRGBA(bounds), image.NewRGBA(bounds)
	for y := 20; y < 44; y++ {
		for x := 30; x < 34; x++ {
			patch.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			metadata.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	boat := render(patch, metadata, 1)
	for y := 20; y < 44; y++ {
		for x := 34; x < 38; x++ {
			patch.SetRGBA(x, y, color.RGBA{0, 255, 0, 255})
			metadata.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	mixed := render(patch, metadata, 1)
	for i := 0; i < len(boat); i += 4 {
		if boat[i] != mixed[i] {
			return fmt.Errorf("nearby aircraft changed low reflection colour")
		}
	}
	return nil
}

// Authored shoreline fixture: an unbroken reflection straddling the wet/dry
// boundary must fade across it, not end on a whole mask texel. Mask texels
// cover eight screen pixels here, as they do on a large map, and the source is
// uniformly opaque so only the mask can shape the edge (GPU design §26.4).
func checkReflectionShorelineCoverage(shader *ebiten.Shader) error {
	const size, maskStep, shore = 64, 8, 32
	bounds := image.Rect(0, 0, size, size)
	opaque := image.NewRGBA(bounds)
	for i := range opaque.Pix {
		opaque.Pix[i] = 255
	}
	coast := image.NewRGBA(image.Rect(0, 0, size/maskStep, size/maskStep))
	for y := 0; y < size/maskStep; y++ {
		for x := 0; x < size/maskStep; x++ {
			wet := byte(255)
			if x >= shore/maskStep {
				wet = 0
			}
			coast.SetRGBA(x, y, color.RGBA{wet, 0, 0, 255})
		}
	}
	source, water, heights := ebiten.NewImageFromImage(opaque), ebiten.NewImageFromImage(coast), ebiten.NewImageFromImage(opaque)
	defer source.Deallocate()
	defer water.Deallocate()
	defer heights.Deallocate()
	target := ebiten.NewImage(size, size)
	defer target.Deallocate()
	vertices := []ebiten.Vertex{{DstX: 0, DstY: 0}, {DstX: size, DstY: 0}, {DstX: size, DstY: size}, {DstX: 0, DstY: size}}
	for i := range vertices {
		// Recording origin 0, native scale, and the mask's own texel step.
		vertices[i].ColorB = 1
		vertices[i].ColorA = maskStep
		vertices[i].Custom2 = 1
	}
	target.DrawTrianglesShader(vertices, []uint16{0, 1, 2, 0, 2, 3}, shader, &ebiten.DrawTrianglesShaderOptions{Images: [4]*ebiten.Image{source, water, heights}})
	pixels := make([]byte, size*size*4)
	target.ReadPixels(pixels)
	// Stay clear of the map edge, where a bilinear mask legitimately fades.
	const first, last = 16, 48
	row := func(x int) int { return int(pixels[((size/2)*size+x)*4+3]) }
	open := row(first)
	if open < 32 {
		return fmt.Errorf("open water lost its reflection: alpha %d", open)
	}
	if row(last) != 0 {
		return fmt.Errorf("reflection reached dry land: alpha %d", row(last))
	}
	soft := 0
	for x := first; x < last; x++ {
		if row(x+1) > row(x) {
			return fmt.Errorf("shoreline coverage is not monotone at %d: %d then %d", x, row(x), row(x+1))
		}
		if a := row(x); a > 4 && a < open-4 {
			soft++
		}
	}
	if soft == 0 {
		return fmt.Errorf("reflection still steps from %d to 0 at the shore", open)
	}
	return nil
}

// The culling grid must admit a ramp-crossing face even when none of its
// vertices lies inside the fade range, and cover filter spill across tile edges.
func TestReflectionSofteningBounds(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		s := waterReflections{
			runs:    []reflectionRun{{page: 0, count: 3, indexCount: 3}},
			indices: []uint32{0, 1, 2},
			transformed: []ebiten.Vertex{
				{DstX: 61, DstY: 61, Custom0: 32 * scale},
				{DstX: 63, DstY: 61, Custom0: 330 * scale},
				{DstX: 63, DstY: 63, Custom0: 330 * scale},
			},
		}
		// Transformed corners are screen coordinates; native effective scale
		// equals record scale in this case.
		if !s.markSofteningTiles(192, 192, scale, scale, .7, 0, 0) || !s.softTiles[0] || !s.softTiles[1] || !s.softTiles[3] || !s.softTiles[4] || s.softTiles[8] {
			t.Fatalf("ramp crossing or filter margin culled incorrectly: scale=%v tiles=%v", scale, s.softTiles)
		}
		for _, height := range []float32{32, 330} {
			for i := range s.transformed {
				s.transformed[i].Custom0 = height * scale
			}
			if s.markSofteningTiles(192, 192, scale, scale, .7, 0, 0) {
				t.Fatalf("inactive height retained blur work: %v", height)
			}
		}
		for i := range s.transformed {
			s.transformed[i].Custom0 = 180 * scale
			s.transformed[i].DstX += 300
		}
		if s.markSofteningTiles(192, 192, scale, scale, .7, 0, 0) {
			t.Fatal("offscreen reflection retained blur work")
		}
	}
}

func TestReflectionSofteningFractionalCoordinates(t *testing.T) {
	s := waterReflections{runs: []reflectionRun{{page: 0, count: 3, indexCount: 3}}, indices: []uint32{0, 1, 2}, transformed: []ebiten.Vertex{{DstX: 300, DstY: 100, Custom0: 180}, {DstX: 301, DstY: 100, Custom0: 180}, {DstX: 301, DstY: 101, Custom0: 180}}}
	if !s.markSofteningTiles(512, 256, 1, .75, .7, 0, 0) || !s.softTiles[2*8+6] || s.softTiles[1*8+4] {
		t.Fatalf("screen bounds not converted to recording coordinates: %v", s.softTiles)
	}
}
