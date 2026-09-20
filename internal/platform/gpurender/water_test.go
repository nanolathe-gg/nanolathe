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

// Shore distance is measured from a rounded coast, not the strict wet set: on a
// steep beach the strict boundary is a staircase of height cells, and every
// wave front and the shallow tint would trace it (§26.3). So no water texel on
// the strict boundary may carry a world pixel of distance, red stays strict, and open water still
// reaches full depth.
func TestWaterShoreDistanceStartsOffTheStrictBoundary(t *testing.T) {
	ter := waterFixtureTerrain()
	// Step the bank two cells west on alternate pairs of rows.
	attrs := make([]formats.TNTAttribute, 16*16)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			attrs[y*16+x].Height = 4
			if x >= 8-2*((y/2)%2) {
				attrs[y*16+x].Height = 28
			}
		}
	}
	ter.Plot = world.ExpandPlot(attrs, 16, 16)
	p, w, h, _, _, _, _ := waterMaskPixels(ter)
	edge, deep, worst := 0, false, 0
	// The map's first and last cell rows project onto invalid ground; the coast
	// under test is the stepped bank between them.
	for y := 32; y < h-32; y++ {
		for x := 1; x < w-1; x++ {
			i := (y*w + x) * 4
			if p[i] != 255 {
				continue
			}
			deep = deep || p[i+1] == 255
			if p[i-4] == 255 && p[i+4] == 255 && p[i-w*4] == 255 && p[i+w*4] == 255 {
				continue
			}
			edge++
			worst = max(worst, int(p[i+1]))
		}
	}
	// Eight levels are one world pixel of the 32 the channel spans; the strict
	// boundary read twenty before the coast was rounded.
	if worst >= 8 {
		t.Fatalf("a strict boundary texel carries shore distance %d, a world pixel or more", worst)
	}
	if edge == 0 || !deep {
		t.Fatalf("fixture lost its coast or its open water: edge=%d deep=%v", edge, deep)
	}
}

// The damp band's ring term is built into the mask's alpha once per terrain
// identity, so the shader reads it out of the tap it already takes rather than
// sampling eight bilinear water readings on every pixel of a viewport-wide quad
// (§32). It reaches about eight world pixels inland from the coast and is zero
// beyond, which is the band the sampled ring drew.
func TestWaterMaskCarriesTheDampBandRing(t *testing.T) {
	p, w, _, step, _, _, _ := waterMaskPixels(waterFixtureTerrain())
	if step != 1 {
		t.Fatalf("fixture mask step %d, want the finest level", step)
	}
	red := func(x, y int) byte { return p[(y*w+x)*4] }
	ring := func(x, y int) byte { return p[(y*w+x)*4+3] }
	const row = 80
	coast := 0
	for x := 0; x < w; x++ {
		if red(x, row) == 0 {
			coast = x
			break
		}
	}
	if coast == 0 || coast >= w {
		t.Fatalf("no coast on row %d", row)
	}
	if ring(coast-1, row) == 0 {
		t.Fatal("the last wet texel carries no ring term; the field must be continuous across the boundary")
	}
	if ring(coast+1, row) < 128 {
		t.Fatalf("one texel inland the ring term is %d, want the band at full strength", ring(coast+1, row))
	}
	if got := ring(coast+8, row); got != 0 {
		t.Fatalf("eight world pixels inland the ring term is %d, want the band ended", got)
	}
	if got := ring(coast+40, row); got != 0 {
		t.Fatalf("well inland the ring term is %d, want zero", got)
	}
	for x := coast + 1; x < coast+8; x++ {
		if ring(x, row) > ring(x-1, row) {
			t.Fatalf("the ring term rises with distance from the water at %d", x)
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
	if err := checkWaterDampBandMatchesRing(r, read); err != nil {
		return err
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

// dampBandRingReference is the damp band's gate as the shader once computed it:
// eight bilinear water taps around the fragment, every frame, on top of the
// twelve reads the other channels cost. waterRingField now builds the same
// product once per terrain identity into the mask's alpha, and this is the
// reference the built field is checked against.
const dampBandRingReference = `
func refWater(p vec2) float {
 q := p-vec2(0.5)
 a := floor(q)
 f := fract(q)
 o := imageSrc0Origin()
 return mix(mix(imageSrc1AtFromSrc0Pos(o+a).r,imageSrc1AtFromSrc0Pos(o+a+vec2(1,0)).r,f.x),mix(imageSrc1AtFromSrc0Pos(o+a+vec2(0,1)).r,imageSrc1AtFromSrc0Pos(o+a+vec2(1,1)).r,f.x),f.y)
}

func refRing(p vec2, r float) float {
 d := r*0.70710678
 s := vec4(refWater(p+vec2(r,0.0)),refWater(p-vec2(r,0.0)),refWater(p+vec2(0.0,r)),refWater(p-vec2(0.0,r)))
 u := vec4(refWater(p+vec2(d,d)),refWater(p-vec2(d,d)),refWater(p+vec2(d,-d)),refWater(p-vec2(d,-d)))
 m := max(max(max(s.x,s.y),max(s.z,s.w)),max(max(u.x,u.y),max(u.z,u.w)))
 return smoothstep(0.2,1.0,m)*smoothstep(0.10,0.45,(s.x+s.y+s.z+s.w+u.x+u.y+u.z+u.w)*0.125)
}
`

// checkWaterDampBandMatchesRing draws the coastal fixture through the shipped
// shader and through one that recomputes the ring per fragment, and requires the
// two to agree. The built field is sampled at texel centres and filtered back,
// so a fragment reads the ring's own numbers blended across one texel; a
// difference beyond a level or two, or one away from the shore, would mean the
// field is not the ring it replaced.
func checkWaterDampBandMatchesRing(r *Renderer, read func(tick uint32, zoom float32, enabled bool, wakes int) []byte) error {
	const old = " damp := mask.w*dry"
	if !strings.Contains(waterShaderSource, old) {
		return fmt.Errorf("water shader no longer gates the damp band on the mask's ring term")
	}
	source := strings.Replace(waterShaderSource, old, " damp := refRing(world/custom.x,8.0/custom.x)*dry", 1) + dampBandRingReference
	reference, err := ebiten.NewShader([]byte(source))
	if err != nil {
		return err
	}
	defer reference.Deallocate()
	shipped := r.water.shader
	on := read(30, 1, true, 0)
	r.water.shader = reference
	sampled := read(30, 1, true, 0)
	r.water.shader = shipped
	worst, at := 0, [2]int{}
	const w, h = 160, 120
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			for ch := 0; ch < 3; ch++ {
				if d := max(int(on[i+ch])-int(sampled[i+ch]), int(sampled[i+ch])-int(on[i+ch])); d > worst {
					worst, at = d, [2]int{x, y}
				}
			}
		}
	}
	if worst > 2 {
		return fmt.Errorf("the built ring field differs from the per-fragment ring by %d levels at %d,%d", worst, at[0], at[1])
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
			// Alpha is the damp band's ring term (waterRingField): full within
			// the ring's eight world pixels of the authored coast and zero
			// beyond, which is what the sampled ring produced for a straight
			// boundary.
			ring := byte(0)
			if x < shore+8/surfaceFixtureStep {
				ring = 255
			}
			if x >= shore {
				m.SetRGBA(x, y, color.RGBA{0, 0, 255, ring})
				continue
			}
			// Shore distance grows inward from the last wet texel, so the
			// fixture carries both a shallow band and genuinely deep water.
			m.SetRGBA(x, y, color.RGBA{255, byte(min((shore-x)*20, 255)), 0, ring})
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
	noTint, err := variant("0.08*smoothstep(0.0,0.10,mask.y)*(1.0-smoothstep(0.10,0.40,mask.y))", "0.0*mask.y")
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
