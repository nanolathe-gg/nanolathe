package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A synthetic coast: flat water west of a raised bank. No retail bytes.
func waterFixtureTerrain() *world.Terrain {
	const cells = 16
	attrs := make([]formats.TNTAttribute, cells*cells)
	for y := 0; y < cells; y++ {
		for x := 0; x < cells; x++ {
			h := byte(4)
			if x >= 8 {
				h = 28
			}
			attrs[y*cells+x].Height = h
		}
	}
	t := &world.Terrain{CellW: cells, CellH: cells, SeaLevel: 16, Plot: world.ExpandPlot(attrs, cells, cells), TileIndices: make([]uint16, cells*cells/4), TileSet: make([][1024]byte, 1)}
	for i := range t.TileSet[0] {
		t.TileSet[0][i] = byte(70 + (i%32)/4)
	}
	return t
}

func TestWaterMaskProjectionAndLiquidGate(t *testing.T) {
	ter := waterFixtureTerrain()
	p, w, h, step, _, _, _ := waterMaskPixels(ter)
	if w == 0 || h == 0 {
		t.Fatal("empty coastal mask")
	}
	at := func(x, y int) byte { return p[((y/step)*w+x/step)*4] }
	dry := func(x, y int) byte { return p[((y/step)*w+x/step)*4+2] }
	if at(48, 80) != 255 || at(180, 80) != 0 {
		t.Fatal("mask lost wet/dry coast")
	}
	// The last world row returns the raw height sentinel, not more ocean.
	if at(48, 252) != 0 {
		t.Fatal("invalid ground treated as water")
	}
	for _, hot := range []bool{false, true} {
		if hot {
			ter.LavaWorld = true
		} else {
			ter.WaterDoesDamage, ter.WaterDamage = 1, 1
		}
		p, _, _, _, _, _, _ = waterMaskPixels(ter)
		if dry(48, 80) != 0 || dry(48, 252) != 0 || dry(180, 80) != 255 {
			t.Fatal("excluded liquid or invalid terrain classified as dry")
		}
		for i := 0; i < len(p); i += 4 {
			if p[i] != 0 {
				t.Fatal("hot/damaging liquid got ocean foam")
			}
		}
	}
}

func checkWaterDevicePixels() error {
	pal := fixturePalette()
	ter := waterFixtureTerrain()
	const w, h = 160, 120
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	read := func(tick uint32, zoom float32, enabled bool, wakes int) []byte {
		var l drawlist.List
		l.RecordClear()
		if zoom != 1 {
			l.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit * 3 / 4, Step: camera.ViewScaleNative, RecordW: w*4/3 + 1, RecordH: h*4/3 + 1})
		}
		rw, rh := int32(w), int32(h)
		if zoom != 1 {
			rw, rh = w*4/3+1, h*4/3+1
		}
		l.RecordTerrain(drawlist.Terrain{Terrain: ter, OriginX: 16, OriginY: 16, DstW: rw, DstH: rh, Scale: camera.ViewScaleNative, Water: drawlist.WaterSurface{Enabled: enabled, Tick: tick, WindStrength: 3500, WindHeading: 12000, Energy: .7, DriftX: float32(tick) / 30}})
		if wakes != 0 {
			l.RecordSurfaceWakes(drawlist.SurfaceWakes{Marks: []drawlist.SurfaceWake{{X: 100, Y: 50, AxisX: 38, AxisY: 0, CrossY: 12, Alpha: .5, Age: .3, Dust: wakes == 2, Foam: wakes == 1}}})
		}
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 20, Y: 20, W: 12, H: 12}, Index: 211})
		if zoom != 1 {
			l.RecordWorld(drawlist.WorldSpace{})
		}
		l.RecordExpand()
		out := r.Execute(&l, w, h)
		p := make([]byte, w*h*4)
		out.ReadPixels(p)
		return p
	}
	for _, zoom := range []float32{1, .75} {
		off, on, later := read(30, zoom, false, 0), read(30, zoom, true, 0), read(42, zoom, true, 0)
		if bytes.Equal(off, on) || bytes.Equal(on, later) {
			return fmt.Errorf("water animation absent at zoom %v", zoom)
		}
		if !bytes.Equal(on, read(30, zoom, true, 0)) {
			return fmt.Errorf("water same-phase replay differs")
		}
		// Surface motion must reach both deep water and the shore. A single
		// whole-image comparison allowed an animated centre to hide a static beach.
		for _, band := range []struct {
			name   string
			x0, x1 float32
		}{{"open water", 35, 65}, {"shore", 85, 105}} {
			changed := false
			for y := int(50 * zoom); y < int(80*zoom); y++ {
				for x := int(band.x0 * zoom); x < int(band.x1*zoom); x++ {
					i := (y*w + x) * 4
					changed = changed || !bytes.Equal(on[i:i+4], later[i:i+4])
				}
			}
			if !changed {
				return fmt.Errorf("%s animation absent at zoom %v", band.name, zoom)
			}
		}
		withWake := read(30, zoom, true, 1)
		if bytes.Equal(withWake, on) {
			return fmt.Errorf("building foam absent")
		}
		if !bytes.Equal(on, read(30, zoom, true, 3)) {
			return fmt.Errorf("removed white wake still draws")
		}
		withDust := read(30, zoom, true, 2)
		if bytes.Equal(withDust, on) {
			return fmt.Errorf("hover dust absent")
		}
		// A land region and an opaque object drawn after water must remain exact.
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dry := float32(x)/zoom+16 > 148
				wet := float32(x)/zoom+16 < 100
				i := (y*w + x) * 4
				if (wet && !bytes.Equal(withDust[i:i+4], on[i:i+4])) || (dry && !bytes.Equal(withWake[i:i+4], on[i:i+4])) {
					return fmt.Errorf("wake/dust crossed shore at %d,%d zoom %v", x, y, zoom)
				}
				for ch := 0; ch < 3; ch++ {
					if withDust[i+ch] < on[i+ch] {
						return fmt.Errorf("hover particle darkened terrain")
					}
				}
				object := x >= int(20*zoom) && x < int(32*zoom) && y >= int(20*zoom) && y < int(32*zoom)
				if dry || object {
					i := (y*w + x) * 4
					if !bytes.Equal(off[i:i+4], on[i:i+4]) {
						return fmt.Errorf("water touched land/object at %d,%d zoom %v", x, y, zoom)
					}
				}
			}
		}
	}
	// A very long game keeps a live, well-formed surface: the noise lattice is
	// wrapped before hashing, so elapsed time and accumulated drift cannot push
	// the hash argument out of float precision (§26.3).
	const distant = 10_000_000
	far, farOff, farLater := read(distant, 1, true, 0), read(distant, 1, false, 0), read(distant+300, 1, true, 0)
	if bytes.Equal(far, farOff) {
		return fmt.Errorf("water surface vanished after a long elapsed time")
	}
	if bytes.Equal(far, farLater) {
		return fmt.Errorf("water animation stopped after a long elapsed time")
	}
	// Detail, not just difference: a hash argument that has outrun its float
	// precision flattens the field into blocks or a constant.
	gap := func(a, b byte) int { return max(int(a)-int(b), int(b)-int(a)) }
	detail := func(p []byte) int {
		total, n := 0, 0
		for y := 8; y < h-8; y++ {
			for x := 8; x+16 < 100; x++ {
				i := (y*w + x) * 4
				if p[i] == 0 && p[i+1] == 0 && p[i+2] == 0 {
					return -1
				}
				total += gap(p[i], p[i+4]) + gap(p[i], p[i+w*4])
				n++
			}
		}
		// Hundredths of a level: the fixture's painted terrain is deliberately
		// low contrast, so a whole-level average would quantize this away.
		return total * 100 / max(n, 1)
	}
	fresh, aged := detail(read(30, 1, true, 0)), detail(far)
	if aged < 0 {
		return fmt.Errorf("water produced a black texel after a long elapsed time")
	}
	if aged*2 < fresh {
		return fmt.Errorf("water pattern degraded after a long elapsed time: detail %d, was %d", aged, fresh)
	}
	if err := checkWaterSurfaceAdditions(); err != nil {
		return err
	}
	r.ResetSources()
	if r.water.mask != nil || r.water.source != nil {
		return fmt.Errorf("coastal mask survived source reset")
	}
	return nil
}

// Authored surface fixture for the three §32 additions. A flat grey terrain and
// a straight authored coast isolate each term: the painted colour contributes
// no variation of its own, so any difference from the term-free variant of the
// same shader source is that term. No retail bytes are involved.
const (
	surfaceFixtureSize  = 128
	surfaceFixtureStep  = 2
	surfaceFixtureCoast = 64
)

func waterSurfaceFixtureImages() (terrain, mask *ebiten.Image) {
	flat := image.NewRGBA(image.Rect(0, 0, surfaceFixtureSize, surfaceFixtureSize))
	for i := range flat.Pix {
		flat.Pix[i] = 128
		if i%4 == 3 {
			flat.Pix[i] = 255
		}
	}
	const texels = surfaceFixtureSize / surfaceFixtureStep
	const shore = surfaceFixtureCoast / surfaceFixtureStep
	m := image.NewRGBA(image.Rect(0, 0, texels, texels))
	for y := 0; y < texels; y++ {
		for x := 0; x < texels; x++ {
			if x >= shore {
				m.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
				continue
			}
			// Shore distance grows inward from the last wet texel, so the
			// fixture carries both a shallow band and genuinely deep water.
			m.SetRGBA(x, y, color.RGBA{255, byte(min((shore-x)*20, 255)), 0, 255})
		}
	}
	return ebiten.NewImageFromImage(flat), ebiten.NewImageFromImage(m)
}

func checkWaterSurfaceAdditions() error {
	terrain, mask := waterSurfaceFixtureImages()
	defer terrain.Deallocate()
	defer mask.Deallocate()
	target := ebiten.NewImage(surfaceFixtureSize, surfaceFixtureSize)
	defer target.Deallocate()
	// Each variant removes exactly one added term from the shipped source.
	variant := func(old, new string) (*ebiten.Shader, error) {
		if !strings.Contains(waterShaderSource, old) {
			return nil, fmt.Errorf("water shader no longer contains %q", old)
		}
		return ebiten.NewShader([]byte(strings.Replace(waterShaderSource, old, new, 1)))
	}
	full, err := newWaterShader()
	if err != nil {
		return err
	}
	defer full.Deallocate()
	noGust, err := variant("smoothstep(0.30,0.80,noise((world-drift*22.0)*0.0055+vec2(3.0,7.0)))", "0.0*noise(world)")
	if err != nil {
		return err
	}
	defer noGust.Deallocate()
	noDamp, err := variant("0.12*damp*(0.5+0.5*lapDry)", "0.0*damp*lapDry")
	if err != nil {
		return err
	}
	defer noDamp.Deallocate()
	noTint, err := variant("0.08*(1.0-smoothstep(0.0,0.35,mask.y))", "0.0*mask.y")
	if err != nil {
		return err
	}
	defer noTint.Deallocate()
	draw := func(shader *ebiten.Shader, t float32) []byte {
		const s = surfaceFixtureSize
		vertices := []ebiten.Vertex{{DstX: 0, DstY: 0, SrcX: 0, SrcY: 0}, {DstX: s, DstY: 0, SrcX: s, SrcY: 0}, {DstX: s, DstY: s, SrcX: s, SrcY: s}, {DstX: 0, DstY: s, SrcX: 0, SrcY: s}}
		for i := range vertices {
			// Committed water time, no drift, full wind energy, mask step and
			// native effective scale — the uniforms drawWater supplies.
			vertices[i].ColorR, vertices[i].ColorG, vertices[i].ColorB, vertices[i].ColorA = t, 0, 0, 1
			vertices[i].Custom0, vertices[i].Custom1 = surfaceFixtureStep, 1
		}
		target.Clear()
		target.DrawTrianglesShader(vertices, []uint16{0, 1, 2, 0, 2, 3}, shader, &ebiten.DrawTrianglesShaderOptions{Images: [4]*ebiten.Image{terrain, mask}})
		p := make([]byte, surfaceFixtureSize*surfaceFixtureSize*4)
		target.ReadPixels(p)
		return p
	}
	at := func(p []byte, x, y int) []byte { return p[(y*surfaceFixtureSize+x)*4 : (y*surfaceFixtureSize+x)*4+4] }
	on, plainGust := draw(full, 3), draw(noGust, 3)
	// A held phase is a paused frame: the surface must compose the same bytes.
	if !bytes.Equal(on, draw(full, 3)) {
		return fmt.Errorf("water surface additions are not deterministic on replay")
	}
	drifted := draw(full, 9)
	if bytes.Equal(on, drifted) {
		return fmt.Errorf("water surface additions did not advance with the field")
	}
	// Gust patches roughen and darken the water they cross, and nothing else:
	// the term sits after the dry early-out, so no dry texel may move. No
	// census is pinned — the patch scale is far coarser than this fixture, so
	// how much of it a gust covers is an accident of the authored extent.
	gusted := 0
	for y := 0; y < surfaceFixtureSize; y++ {
		for x := 0; x < surfaceFixtureSize; x++ {
			same := bytes.Equal(at(on, x, y), at(plainGust, x, y))
			if x >= surfaceFixtureCoast {
				if !same {
					return fmt.Errorf("gust patch reached dry ground at %d,%d", x, y)
				}
				continue
			}
			if !same {
				gusted++
			}
		}
	}
	if gusted == 0 {
		return fmt.Errorf("gust patches absent")
	}
	// The churn is the domain warp, not the gust: with the gust held flat the
	// surface must still change between two phases of the same wind.
	if bytes.Equal(plainGust, draw(noGust, 9)) {
		return fmt.Errorf("water surface stopped moving without the gust term")
	}
	plainDamp := draw(noDamp, 3)
	band := 0
	for y := 0; y < surfaceFixtureSize; y++ {
		for x := 0; x < surfaceFixtureSize; x++ {
			lit, unlit := at(on, x, y), at(plainDamp, x, y)
			// The band belongs to every texel the painted water does not cover,
			// which the bilinear mask carries a few world pixels seaward of the
			// authored boundary; covered water must stay untouched.
			switch {
			case x < surfaceFixtureCoast-8:
				if !bytes.Equal(lit, unlit) {
					return fmt.Errorf("damp band altered water at %d,%d", x, y)
				}
			case x >= surfaceFixtureCoast+12:
				if !bytes.Equal(lit, unlit) {
					return fmt.Errorf("damp band reached %d world pixels inland at %d,%d", x-surfaceFixtureCoast, x, y)
				}
			default:
				for ch := 0; ch < 3; ch++ {
					if lit[ch] > unlit[ch] {
						return fmt.Errorf("damp band brightened the shore at %d,%d", x, y)
					}
				}
				if !bytes.Equal(lit, unlit) {
					band++
				}
			}
		}
	}
	if band == 0 {
		return fmt.Errorf("damp shoreline band absent")
	}
	plainTint := draw(noTint, 3)
	tinted := 0
	for y := 0; y < surfaceFixtureSize; y++ {
		for x := 0; x < surfaceFixtureSize; x++ {
			lit, unlit := at(on, x, y), at(plainTint, x, y)
			// The mask ramps 20 per texel of two world pixels, so 0.35 of full
			// scale is about 9 texels — 18 world pixels — from the coast.
			shallow := x >= surfaceFixtureCoast-18 && x < surfaceFixtureCoast
			if !shallow && !bytes.Equal(lit, unlit) {
				return fmt.Errorf("shallow tint left the near-shore band at %d,%d", x, y)
			}
			if shallow && !bytes.Equal(lit, unlit) {
				tinted++
			}
		}
	}
	if tinted == 0 {
		return fmt.Errorf("shallow water tint absent")
	}
	return nil
}
