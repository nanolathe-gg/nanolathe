package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func TestBattleLightingDirectionAndDistance(t *testing.T) {
	s := subjectLights{count: 1, lights: [subjectLightLimit]battleLight{{position: [3]float32{0, 0, 20}, color: [3]float32{1, 0.3, 0.1}, radius: 100}}}
	up := s.irradiance(0, -10, 0, [3]float32{0, 0, 1}, false)
	down := s.irradiance(0, -10, 0, [3]float32{0, 0, -1}, false)
	far := s.irradiance(200, 0, 0, [3]float32{0, 0, 1}, false)
	if up[0] <= up[1] || up[1] <= up[2] || down != [3]float32{} || far != [3]float32{} {
		t.Fatalf("up %v, down %v, far %v", up, down, far)
	}
	// A common camera translation and scale must not change irradiance.
	scaled := s
	scaled.lights[0].position = [3]float32{14, 34, 40}
	scaled.lights[0].radius = 200
	got := scaled.irradiance(14, 14, 0, [3]float32{0, 0, 1}, false)
	if got != up {
		t.Fatalf("projection changed light: %v -> %v", up, got)
	}
}

func TestBattleLightSourcesAndReset(t *testing.T) {
	r := &Renderer{}
	r.displayPalette[3] = [4]byte{255, 80, 20, 255}
	art := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{3}}
	var list drawlist.List
	list.RecordSprite(drawlist.Sprite{Frame: art, Emissive: true, LightingKind: drawlist.SpriteLightingSmoke})
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 0 {
		t.Fatal("smoke emitted light")
	}
	list.RecordSprite(drawlist.Sprite{Frame: art, LightingKind: drawlist.SpriteLightingExplosion})
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 1 {
		t.Fatal("explicit explosion did not emit")
	}
	clone := list.Clone()
	list.Reset()
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 0 {
		t.Fatal("empty frame retained light")
	}
	r.prepareBattleLighting(&clone)
	if len(r.lighting.lights) != 1 {
		t.Fatal("clone lost lighting metadata")
	}
	r.setBattleLighting(false)
	r.prepareBattleLighting(&clone)
	if len(r.lighting.lights) != 0 {
		t.Fatal("disabled prototype emitted light")
	}
}

func TestBattleLightingShadersCompile(t *testing.T) {
	for _, src := range []string{modelDirectColourShaderSource(), scene2DShaderSource(), sceneDestShaderSource()} {
		if _, err := ebiten.NewShader([]byte(src)); err != nil {
			t.Fatal(err)
		}
	}
}

// The same synthetic art is a receiver only when explicitly tagged. The loop
// checks the real backend, atlas placement, premultiplied smoke, and no-light
// identity rather than only repeating the host lighting arithmetic.
func checkBattleLightingDevicePixels() error {
	if err := checkExplosionTemporalDevicePixels(); err != nil {
		return err
	}
	if err := checkProjectedGroundLightDevicePixels(); err != nil {
		return err
	}
	if err := checkBattleLightPacking(); err != nil {
		return err
	}
	pal := fixturePalette()
	pal.Base[230] = [4]byte{255, 90, 20, 255}
	const w, h = 240, 140
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	art := &formats.GAFFrame{Width: 12, Height: 12, XOffset: 6, YOffset: 6, Pixels: bytes.Repeat([]byte{230}, 144)}
	smoke := &formats.GAFFrame{Width: 24, Height: 24, XOffset: 12, YOffset: 12, Pixels: bytes.Repeat([]byte{65}, 576)}
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 25})
	for i, normal := range [][3]float32{{1, 0, 0}, {-1, 0, 0}} {
		f := directFace(0, 0, 18, 18, 95, 10, 10)
		f.Normal = normal
		g := directSubject(62, 48+int32(i)*38, 18, 18, f)
		list.RecordModel(drawlist.Model{Geometry: g})
	}
	list.RecordSprite(drawlist.Sprite{Frame: smoke, X: 115, Y: 90, Kind: drawlist.BlitTinted, LightingKind: drawlist.SpriteLightingSmoke})
	list.RecordSprite(drawlist.Sprite{Frame: smoke, X: 115, Y: 120, Kind: drawlist.BlitTinted})
	list.RecordSprite(drawlist.Sprite{Frame: art, X: 105, Y: 65, WorldHeight: 12, LightingScale: 1, Kind: drawlist.BlitKeyed, Anchored: true, LightingKind: drawlist.SpriteLightingExplosion})
	list.RecordExpand()
	read := func(on bool) []byte {
		r.setBattleLighting(on)
		out := r.Execute(&list, w, h)
		pixels := make([]byte, w*h*4)
		out.ReadPixels(pixels)
		return pixels
	}
	off, on := read(false), read(true)
	at := func(p []byte, x, y int) []byte { return p[(y*w+x)*4 : (y*w+x)*4+4] }
	lit, base := at(on, 70, 56), at(off, 70, 56)
	if lit[0] <= base[0]+5 || lit[0] <= lit[1] {
		return fmt.Errorf("facing model not warm: off %v on %v", base, lit)
	}
	if !bytes.Equal(at(on, 70, 95), at(off, 70, 95)) {
		return fmt.Errorf("back-facing model lit")
	}
	lit, base = at(on, 115, 90), at(off, 115, 90)
	if lit[0] <= base[0]+5 || lit[0] <= lit[1] {
		return fmt.Errorf("smoke not warm: off %v on %v", base, lit)
	}
	if !bytes.Equal(at(on, 115, 120), at(off, 115, 120)) {
		return fmt.Errorf("unclassified sprite changed")
	}
	if !bytes.Equal(off, read(false)) {
		return fmt.Errorf("disabling lighting retained effect")
	}
	if dir := os.Getenv("NANOLATHE_LIGHTING_SHOTS"); dir != "" {
		for _, shot := range []struct {
			name   string
			pixels []byte
		}{{"off.png", off}, {"on.png", on}} {
			f, err := os.Create(filepath.Join(dir, shot.name))
			if err != nil {
				return err
			}
			err = png.Encode(f, &image.RGBA{Pix: shot.pixels, Stride: w * 4, Rect: image.Rect(0, 0, w, h)})
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}

// Exercise channel carry boundaries on the device: numeric packed colours must
// not let a rounded low channel erase or increment its neighbours.
func checkBattleLightPacking() error {
	shader, err := ebiten.NewShader([]byte("//kage:unit pixels\npackage main\n" + battleLightShaderSource + `
func Fragment(dst vec4, src vec2, color vec4) vec4 { return vec4(battleLight(color.r)*0.5,1.0) }`))
	if err != nil {
		return err
	}
	target := ebiten.NewImage(4, 1)
	colors := [][3]float32{{2, 2, 1.02}, {2, 1.02, 2}, {1.02, 2, 2}, {2, 2, 2}}
	var verts []ebiten.Vertex
	var idx []uint16
	for i, c := range colors {
		x := float32(i)
		code := packBattleLight(c)
		base := uint16(len(verts))
		verts = append(verts, ebiten.Vertex{DstX: x, DstY: 0, ColorR: code}, ebiten.Vertex{DstX: x + 1, DstY: 0, ColorR: code}, ebiten.Vertex{DstX: x, DstY: 1, ColorR: code}, ebiten.Vertex{DstX: x + 1, DstY: 1, ColorR: code})
		idx = append(idx, base, base+1, base+2, base+1, base+2, base+3)
	}
	target.DrawTrianglesShader(verts, idx, shader, nil)
	pixels := make([]byte, 16)
	target.ReadPixels(pixels)
	for i, c := range colors {
		for j := range c {
			want := int(c[j]*127.5 + 0.5)
			got := int(pixels[i*4+j])
			if got < want-2 || got > want+2 {
				return fmt.Errorf("packed light %v channel %d: got %d want %d", c, j, got, want)
			}
		}
	}
	return nil
}

func TestCompositeExplosionMeasuresFinalArtOnce(t *testing.T) {
	var pal [256][4]byte
	pal[3] = [4]byte{255, 90, 20, 255}
	bright := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{3}}
	dark := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{0}}
	composite := &formats.GAFFrame{Width: 1, Height: 1, Subframes: []*formats.GAFFrame{bright, bright}}
	if got, want := explosionColor(composite, &pal), explosionColor(bright, &pal); got != want {
		t.Fatalf("composite energy %v != flat %v", got, want)
	}
	composite.Subframes[1] = dark
	if got := explosionColor(composite, &pal); got != [3]float32{} {
		t.Fatalf("overwritten leaf emits %v", got)
	}
}
