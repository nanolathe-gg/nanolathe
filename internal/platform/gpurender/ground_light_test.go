package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Enhanced presentation policy: absolute elevation cannot move the ground
// overlay away from the projected emitter (GPU design §31.3; SC20).
func TestGroundLightUsesProjectedSourceAtEveryScale(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, rest := range []float32{1, .75} {
			for kind := lightKind(0); kind < lightKindCount; kind++ {
				r := &Renderer{w: 400, h: 300}
				r.sched.worldOn, r.sched.worldScale = true, rest
				source := battleLight{position: [3]float32{100 * scale, 80 * scale, 48 * scale}, radius: 72 * scale, kind: kind}
				r.lighting.lights = []battleLight{source}
				r.appendGroundLights()
				if len(r.ground.verts) != 4 {
					t.Fatalf("kind %d scale %v: missing pool", kind, scale)
				}
				v := r.ground.verts[0]
				if v.Custom0 != 100*scale*rest || v.Custom1 != 56*scale*rest || v.Custom2 != 48*scale*rest || v.Custom3 != 72*scale*rest {
					t.Fatalf("kind %d scale %v rest %v: source operands %+v", kind, scale, rest, v)
				}
				if r.lighting.lights[0] != source {
					t.Fatal("ground projection changed physical source")
				}
				r.lighting.lights[0].position[2] = source.radius
				r.appendGroundLights()
				if len(r.ground.verts) != 0 {
					t.Fatal("air burst at reach still illuminated ground")
				}
			}
		}
	}
}

func TestElevatedExplosionAndNanoGroundSources(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		r := &Renderer{w: 400, h: 300}
		r.displayPalette[230] = [4]byte{255, 90, 20, 255}
		art := &formats.GAFFrame{Width: 16, Height: 16, XOffset: 8, YOffset: 8, Pixels: bytes.Repeat([]byte{230}, 256)}
		if scale == 2 {
			art = art.Doubled()
		}
		var list drawlist.List
		list.RecordSprite(drawlist.Sprite{Frame: art, X: int32(100 * scale), Y: int32(50 * scale), Kind: drawlist.BlitKeyed, Anchored: true, LightingKind: drawlist.SpriteLightingExplosion, WorldHeight: 48 * scale, LightingScale: scale})
		r.prepareBattleLighting(&list)
		r.appendGroundLights()
		if len(r.ground.verts) != 4 || r.ground.verts[0].Custom1 != 48*scale {
			t.Fatalf("explosion detached from lifted event: %+v", r.ground.verts)
		}
		list.Reset()
		for i := 161; i <= 167; i++ {
			r.displayPalette[i] = [4]byte{40, 255, 90, 255}
		}
		list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: int32(100 * scale), Y: int32(50 * scale), W: 2, H: 2}, Nano: true, WorldHeight: 32 * scale, LightingScale: scale})
		r.prepareBattleLighting(&list)
		r.appendGroundLights()
		if len(r.ground.verts) != 4 || r.ground.verts[0].Custom1 != 50*scale {
			t.Fatalf("nano pool detached from particle: %+v", r.ground.verts)
		}
	}
}

// Read the real ground pass at native and doubled recording scales. Equal
// samples above/below the projected source must match, despite its elevation.
func checkProjectedGroundLightDevicePixels() error {
	if err := checkExplosionGroundFlashDevicePixels(); err != nil {
		return err
	}
	for _, scale := range []int{1, 2} {
		w, h := 240*scale, 120*scale
		pal := fixturePalette()
		pal.Base[230] = [4]byte{255, 90, 20, 255}
		r, err := NewChecked(&pal, w, h)
		if err != nil {
			return err
		}
		art := &formats.GAFFrame{Width: 16, Height: 16, XOffset: 8, YOffset: 8, Pixels: bytes.Repeat([]byte{230}, 256)}
		step := camera.ViewScaleNative
		if scale == 2 {
			art = art.Doubled()
			step = camera.ViewScaleDetail
		}
		var list drawlist.List
		list.RecordClear()
		list.RecordTerrain(drawlist.Terrain{Terrain: groundFixtureTerrain(40), Cam: &camera.Camera{}, DstW: int32(w), DstH: int32(h), Scale: step})
		list.RecordSprite(drawlist.Sprite{Frame: art, X: int32(120 * scale), Y: int32(50 * scale), Kind: drawlist.BlitKeyed, Anchored: true, LightingKind: drawlist.SpriteLightingExplosion, WorldHeight: 48 * float32(scale), LightingScale: float32(scale)})
		list.RecordExpand()
		read := func(on bool) []byte {
			r.setBattleLighting(on)
			p := make([]byte, w*h*4)
			r.Execute(&list, w, h).ReadPixels(p)
			return p
		}
		off, on := read(false), read(true)
		at := func(p []byte, x, y int) int { return int(p[(y*w+x)*4]) }
		// Pixel centres at these rows are symmetric about the projected row 48.
		x, up, down := 96*scale, 36*scale, 60*scale-1
		if at(on, x, up) <= at(off, x, up)+4 || at(on, x, up) != at(on, x, down) {
			return fmt.Errorf("scale %d elevated source pool not centred: upper %d lower %d base %d", scale, at(on, x, up), at(on, x, down), at(off, x, up))
		}
		if !bytes.Equal(off, read(false)) {
			return fmt.Errorf("scale %d lighting switch failed to restore terrain", scale)
		}
		if dir := os.Getenv("NANOLATHE_LIGHTING_SHOTS"); dir != "" {
			for _, shot := range []struct {
				name string
				p    []byte
			}{{"off", off}, {"on", on}} {
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("ground-projected-%dx-%s.png", scale, shot.name)))
				if err != nil {
					return err
				}
				err = png.Encode(f, &image.RGBA{Pix: shot.p, Stride: w * 4, Rect: image.Rect(0, 0, w, h)})
				closeErr := f.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
		}
	}
	return nil
}

// User-authored terrain policy (§31.6, §31.7), independent of source emission:
// every family declares its own share of the terrain gain, and none of it may
// reach the model or smoke receivers, which read the light itself.
func TestGroundReceiverAppliesTheFamilyShareOnly(t *testing.T) {
	r := &Renderer{w: 400, h: 300}
	for kind := lightKind(0); kind < lightKindCount; kind++ {
		source := battleLight{position: [3]float32{100, 100, 10}, color: [3]float32{1, .5, .2}, radius: 100, kind: kind, ageKnown: true}
		for _, age := range []float32{0, 2, 7, 12, 30} {
			source.age = age
			r.lighting.lights = []battleLight{source}
			r.appendGroundLights()
			if r.lighting.lights[0] != source {
				t.Fatal("terrain fade mutated model/smoke emission")
			}
			if kind == lightExplosion && age >= 12 {
				if len(r.ground.indices) != 0 {
					t.Fatal("expired terrain flash retained geometry")
				}
			} else {
				if len(r.ground.verts) != 4 {
					t.Fatal("active light lost terrain receiver")
				}
				gain := r.ground.verts[0].ColorR
				if kind != lightExplosion && gain != groundKindScale[kind] {
					t.Fatalf("family %d terrain gain = %v, want its declared share %v", kind, gain, groundKindScale[kind])
				}
				if kind == lightExplosion && (gain <= 0 || gain >= 1) {
					t.Fatal("terrain flash did not soften")
				}
			}
		}
	}
	peak := explosionGroundScale(2, true)
	if peak != explosionGroundScale(0, true) || explosionGroundScale(7, true) != peak/4 || explosionGroundScale(12, true) != 0 {
		t.Fatal("flash hold/fade boundary changed")
	}
	if explosionGroundScale(100, false) != peak {
		t.Fatal("unknown timing invented an expiration")
	}
}

// Real pixels prove the ground can return to its unlit value while the nearby
// opaque model still carries the same warm explosion light (§31.6).
func checkExplosionGroundFlashDevicePixels() error {
	const w, h = 240, 140
	pal := fixturePalette()
	pal.Base[230] = [4]byte{255, 90, 20, 255}
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	art := &formats.GAFFrame{Width: 12, Height: 12, XOffset: 6, YOffset: 6, Pixels: bytes.Repeat([]byte{230}, 144)}
	pixel := func(p []byte, x, y int) []byte { return p[(y*w+x)*4 : (y*w+x)*4+4] }
	read := func(age float32, lighting bool) []byte {
		var l drawlist.List
		l.RecordClear()
		l.RecordTerrain(drawlist.Terrain{Terrain: groundFixtureTerrain(40), Cam: &camera.Camera{}, DstW: w, DstH: h, Scale: camera.ViewScaleNative})
		face := directFace(0, 0, 18, 18, 95, 10, 10)
		face.Normal = [3]float32{1, 0, 0}
		l.RecordModel(drawlist.Model{Geometry: directSubject(62, 48, 18, 18, face)})
		l.RecordLightSource(drawlist.Sprite{Frame: art, X: 105, Y: 65, WorldHeight: 12, LightingScale: 1, LightingSize: 66, LightingKind: drawlist.SpriteLightingExplosion, LightingAge: age, HasLightingAge: true})
		l.RecordExpand()
		r.setBattleLighting(lighting)
		p := make([]byte, w*h*4)
		r.Execute(&l, w, h).ReadPixels(p)
		return p
	}
	unlit := read(0, false)
	early := read(2, true)
	late := read(7, true)
	expired := read(12, true)
	ground := func(p []byte) byte { return pixel(p, 120, 40)[0] }
	if !(ground(early) > ground(late) && ground(late) > ground(unlit)) {
		return fmt.Errorf("terrain flash did not fade: %d %d base=%d", ground(early), ground(late), ground(unlit))
	}
	if !bytes.Equal(pixel(expired, 120, 40), pixel(unlit, 120, 40)) {
		return fmt.Errorf("terrain still lit after flash")
	}
	for _, p := range [][]byte{late, expired} {
		if !bytes.Equal(pixel(p, 70, 56), pixel(early, 70, 56)) {
			return fmt.Errorf("the fading terrain flash altered nearby model color")
		}
	}
	warm := pixel(expired, 70, 56)
	if warm[0] <= pixel(unlit, 70, 56)[0]+5 || warm[0] <= warm[1] {
		return fmt.Errorf("nearby model lost warm illumination")
	}
	if !bytes.Equal(early, read(2, true)) {
		return fmt.Errorf("replay changed terrain flash")
	}
	return nil
}

// The pass copies only the pixels its shader samples. Every ground fragment
// reads its own pixel, so the read region is exactly the union of the clipped
// discs — a lit frame no longer costs a full-frame blit (readcopy.go).
func TestGroundLightReadRegionIsTheDiscUnion(t *testing.T) {
	r := &Renderer{w: 400, h: 300}
	r.lighting.lights = []battleLight{
		{position: [3]float32{100, 80, 0}, radius: 20},
		{position: [3]float32{300, 200, 0}, radius: 10},
	}
	r.appendGroundLights()
	if len(r.ground.verts) != 8 {
		t.Fatalf("expected two pools, got %d vertices", len(r.ground.verts))
	}
	x0, y0, x1, y1 := r.ground.verts[0].DstX, r.ground.verts[0].DstY, r.ground.verts[0].DstX, r.ground.verts[0].DstY
	for _, v := range r.ground.verts {
		x0, y0 = min(x0, v.DstX), min(y0, v.DstY)
		x1, y1 = max(x1, v.DstX), max(y1, v.DstY)
	}
	got := r.ground.read
	if !got.any || float32(got.x0) != x0 || float32(got.y0) != y0 || float32(got.x1) != x1 || float32(got.y1) != y1 {
		t.Fatalf("read region %+v want %v,%v..%v,%v", got, x0, y0, x1, y1)
	}
	if area := (got.x1 - got.x0) * (got.y1 - got.y0); area >= r.w*r.h/2 {
		t.Fatalf("two small pools copied %d of %d pixels", area, r.w*r.h)
	}
	r.lighting.lights = nil
	r.appendGroundLights()
	if r.ground.read.any {
		t.Fatal("a frame with no light still asked for a copy")
	}
}

// A spark is the burning fragment a death throws, and the terrain receiver has
// to treat it as one: a reach taken from its own art rather than the standing
// fire's wide floor, a small share of the gain, and a pool that leaves with the
// flame rather than switching off with the particle (§31.7).
func TestSparkGroundPoolIsSmallAndLeavesWithTheFlame(t *testing.T) {
	if groundKindScale[lightSpark] >= groundKindScale[lightFire] {
		t.Fatal("a spark in flight must light the ground less than a place that is burning")
	}
	if sparkRadiusMax >= fireRadiusMin {
		t.Fatal("a spark's widest reach must stay inside the standing fire's floor")
	}
	r := &Renderer{w: 400, h: 300}
	source := battleLight{position: [3]float32{100, 100, 10}, color: [3]float32{1, .5, .2}, radius: 40, kind: lightSpark}
	// No producer fade: the pool stands at the family's own share.
	r.lighting.lights = []battleLight{source}
	r.appendGroundLights()
	if len(r.ground.verts) != 4 || r.ground.verts[0].ColorR != groundKindScale[lightSpark] {
		t.Fatalf("unfaded spark gain = %+v", r.ground.verts)
	}
	// Half a tail left halves it, and a spent source draws no quad at all.
	for _, c := range []struct {
		fade float32
		want float32
	}{{1, groundKindScale[lightSpark]}, {0.5, groundKindScale[lightSpark] / 2}, {0, 0}} {
		faded := source
		faded.fade, faded.fadeKnown = c.fade, true
		r.lighting.lights = []battleLight{faded}
		r.appendGroundLights()
		if c.want == 0 {
			if len(r.ground.indices) != 0 {
				t.Fatal("a spent source kept its terrain quad")
			}
			continue
		}
		if len(r.ground.verts) != 4 || r.ground.verts[0].ColorR != c.want {
			t.Fatalf("fade %v gain = %+v, want %v", c.fade, r.ground.verts, c.want)
		}
	}
	// The reserves stay a partition of the budget with the added family.
	total := 0
	for _, n := range lightKindReserve {
		total += n
	}
	if total != battleLightLimit {
		t.Fatalf("reserves sum to %d, want the budget %d", total, battleLightLimit)
	}
}

// The terrain receiver attenuates by the source's height above the GROUND, not
// above the sea datum. Without it a pool narrower than the map's elevation is
// discarded outright, which is what suppressed every spark on rising ground
// (§31.5, §31.7).
func TestGroundPoolMeasuresHeightAboveTheGroundUnderIt(t *testing.T) {
	r := &Renderer{w: 400, h: 300}
	// A spark-sized pool standing on ground a hundred units up: from the datum
	// its height is past its own reach and the quad vanishes.
	source := battleLight{position: [3]float32{100, 100, 104}, color: [3]float32{1, .5, .2}, radius: 40, kind: lightSpark}
	r.lighting.lights = []battleLight{source}
	r.appendGroundLights()
	if len(r.ground.indices) != 0 {
		t.Fatal("the datum measurement is what this test contrasts with; it must still discard")
	}
	// Told what the ground under it is, the same source is at rest on it.
	standing := source
	standing.ground = 104
	r.lighting.lights = []battleLight{standing}
	r.appendGroundLights()
	if len(r.ground.verts) != 4 {
		t.Fatal("a source resting on high ground lost its pool")
	}
	if h := r.ground.verts[0].Custom2; h != 0 {
		t.Fatalf("height above ground = %v, want 0 for a source at rest on it", h)
	}
	// Lifted above that ground it attenuates again, and the physical source the
	// model and smoke receivers read is never touched.
	lifted := standing
	lifted.position[2] = 104 + 30
	r.lighting.lights = []battleLight{lifted}
	r.appendGroundLights()
	if len(r.ground.verts) != 4 || r.ground.verts[0].Custom2 != 30 {
		t.Fatalf("lifted source operands %+v", r.ground.verts)
	}
	if r.lighting.lights[0] != lifted {
		t.Fatal("the ground measurement mutated the physical source")
	}
}
