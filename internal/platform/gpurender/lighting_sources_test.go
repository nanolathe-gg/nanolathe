package gpurender

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// warmArt is the four-by-four emitter every source test measures, so the
// admitted colour is the same for each family and only the family differs.
func warmArt(r *Renderer) *formats.GAFFrame {
	r.displayPalette[3] = [4]byte{255, 90, 20, 255}
	return &formats.GAFFrame{Width: 4, Height: 4, Pixels: bytes.Repeat([]byte{3}, 16)}
}

func TestAddedLightKindsAdmittedAndRejected(t *testing.T) {
	r := &Renderer{}
	art := warmArt(r)
	for _, tc := range []struct {
		kind   drawlist.SpriteLightingKind
		want   lightKind
		emits  bool
		radius float32
	}{
		{drawlist.SpriteLightingFire, lightFire, true, fireRadiusMin},
		{drawlist.SpriteLightingProjectile, lightProjectile, true, projRadiusMin},
		{drawlist.SpriteLightingSmoke, 0, false, 0},
		{drawlist.SpriteLightingNone, 0, false, 0},
	} {
		var list drawlist.List
		list.RecordSprite(drawlist.Sprite{Frame: art, X: 40, Y: 40, LightingKind: tc.kind, LightingScale: 1})
		r.prepareBattleLighting(&list)
		if got := len(r.lighting.lights); got != map[bool]int{true: 1, false: 0}[tc.emits] {
			t.Fatalf("kind %d: %d lights", tc.kind, got)
		}
		if !tc.emits {
			continue
		}
		got := r.lighting.lights[0]
		if got.kind != tc.want || got.radius != tc.radius {
			t.Fatalf("kind %d: %+v want kind %d radius %v", tc.kind, got, tc.want, tc.radius)
		}
		if r.modelStats.BattleLightKinds[tc.want] != 1 {
			t.Fatalf("kind %d: diagnostics %v", tc.kind, r.modelStats.BattleLightKinds)
		}
		// The Lighting switch gates every added family exactly as it gates the
		// explosion prototype (§30).
		r.setBattleLighting(false)
		r.prepareBattleLighting(&list)
		if len(r.lighting.lights) != 0 || r.modelStats.BattleLights != 0 {
			t.Fatalf("kind %d emitted with lighting off", tc.kind)
		}
		r.setBattleLighting(true)
	}
}

// The flicker is bounded, smooth and reproducible from the committed tick, and
// two fires a few pixels apart do not pulse together.
func TestFireFlickerBoundedAndDeterministic(t *testing.T) {
	same := true
	for i := 0; i < 4*fireFlickerTicks; i++ {
		tick := float32(i) * 0.25
		got := fireFlicker(tick, 100, 60)
		if got < 0.75 || got > 1.0 {
			t.Fatalf("tick %v: flicker %v out of [0.75, 1]", tick, got)
		}
		if got != fireFlicker(tick, 100, 60) {
			t.Fatalf("tick %v: flicker not deterministic", tick)
		}
		if fireFlicker(tick, 100, 60) != fireFlicker(tick, 137, 61) {
			same = false
		}
	}
	if same {
		t.Fatal("neighbouring fires share a phase")
	}
	// A full period returns to the same value: the phase is the only per-source
	// difference, and the wave itself is continuous.
	if a, b := fireFlicker(0, 100, 60), fireFlicker(fireFlickerTicks, 100, 60); math.Abs(float64(a-b)) > 1e-4 {
		t.Fatalf("period is not %d ticks: %v vs %v", fireFlickerTicks, a, b)
	}
}

func TestFireEmissionCarriesFlicker(t *testing.T) {
	r := &Renderer{}
	art := warmArt(r)
	peak := func(tick float32) float32 {
		var list drawlist.List
		list.RecordSprite(drawlist.Sprite{Frame: art, X: 40, Y: 40, LightingKind: drawlist.SpriteLightingFire, LightingScale: 1, LightingTime: tick})
		r.prepareBattleLighting(&list)
		return lightPower(r.lighting.lights[0])
	}
	var lo, hi float32 = 1e9, 0
	for i := 0; i < 4*fireFlickerTicks; i++ {
		p := peak(float32(i) * 0.25)
		lo, hi = min(lo, p), max(hi, p)
	}
	// Measured energy is 1 for this art, so the emitted peak is the flicker
	// scaled by the family's energy gain, and it still spans the whole wave.
	if lo < 0.749*fireEnergy || hi > 1.001*fireEnergy || hi-lo < 0.2*fireEnergy {
		t.Fatalf("flickered emission range [%v, %v]", lo, hi)
	}
}

// A flame frame too dark to carry a hue keeps its measured energy and takes the
// authored warm one, so a dying fire still fades rather than jumping to orange.
func TestDimFlameArtTakesWarmFallbackHue(t *testing.T) {
	r := &Renderer{}
	r.displayPalette[5] = [4]byte{160, 150, 150, 255}
	dim := &formats.GAFFrame{Width: 4, Height: 4, Pixels: bytes.Repeat([]byte{5}, 16)}
	measured := explosionColor(dim, &r.displayPalette)
	peak := max(measured[0], measured[1], measured[2])
	if peak >= fireDimPeak {
		t.Fatalf("fixture art is not dim: %v", measured)
	}
	var list drawlist.List
	list.RecordSprite(drawlist.Sprite{Frame: dim, X: 40, Y: 40, LightingKind: drawlist.SpriteLightingFire, LightingScale: 1})
	r.prepareBattleLighting(&list)
	got := r.lighting.lights[0].color
	if got[0] <= got[1] || got[1] <= got[2] {
		t.Fatalf("fallback hue is not warm: %v", got)
	}
	// The fallback replaces the HUE, never the measured energy: the emitted
	// peak is the measurement carried through the family's own gain, not more.
	if lightPower(r.lighting.lights[0]) > peak*fireEnergy {
		t.Fatalf("fallback raised energy: %v from measured peak %v", got, peak)
	}
}

func TestPerKindLightBudget(t *testing.T) {
	r := &Renderer{}
	art := warmArt(r)
	fires := func(l *drawlist.List, n int) {
		for i := 0; i < n; i++ {
			l.RecordSprite(drawlist.Sprite{Frame: art, X: int32(i * 7), Y: 40, LightingKind: drawlist.SpriteLightingFire, LightingScale: 1})
		}
	}
	blasts := func(l *drawlist.List, n int) {
		for i := 0; i < n; i++ {
			l.RecordSprite(drawlist.Sprite{Frame: art, X: int32(i * 7), Y: 90, LightingKind: drawlist.SpriteLightingExplosion, LightingScale: 1})
		}
	}
	var many drawlist.List
	fires(&many, 200)
	r.prepareBattleLighting(&many)
	if got := r.lighting.counts[lightFire]; got != lightKindCap[lightFire] {
		t.Fatalf("fire cap: %d", got)
	}
	// Fires arriving after a full field of explosions still reach their cap, and
	// the explosions keep at least their reserve.
	var after drawlist.List
	blasts(&after, battleLightLimit)
	fires(&after, 200)
	r.prepareBattleLighting(&after)
	if r.lighting.counts[lightFire] != lightKindCap[lightFire] || r.lighting.counts[lightExplosion] < lightKindReserve[lightExplosion] {
		t.Fatalf("fires after explosions: %v", r.lighting.counts)
	}
	// And the reverse: a full field of explosions cannot push the fires that
	// arrived first below their reserve.
	var before drawlist.List
	fires(&before, 200)
	blasts(&before, 200)
	r.prepareBattleLighting(&before)
	if r.lighting.counts[lightFire] < lightKindReserve[lightFire] || r.lighting.counts[lightExplosion] < lightKindReserve[lightExplosion] {
		t.Fatalf("explosions after fires: %v", r.lighting.counts)
	}
	if len(r.lighting.lights) != battleLightLimit {
		t.Fatalf("budget = %d", len(r.lighting.lights))
	}
}

func TestEmissiveStrokesLight(t *testing.T) {
	r := &Renderer{}
	r.displayPalette[9] = [4]byte{40, 255, 90, 255}
	var list drawlist.List
	list.RecordLine(drawlist.Line{X0: 10, Y0: 10, X1: 10, Y1: 20, Index: 9})
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 0 {
		t.Fatal("a non-emissive stroke lit the world")
	}
	list.RecordLine(drawlist.Line{X0: 100, Y0: 60, X1: 500, Y1: 60, Index: 9, Emissive: true,
		WorldHeight0: 20, WorldHeight1: 40, LightingScale: 1})
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 1 {
		t.Fatalf("emissive stroke: %d lights", len(r.lighting.lights))
	}
	got := r.lighting.lights[0]
	if got.kind != lightProjectile || got.radius != strokeRadiusMax {
		t.Fatalf("stroke light %+v", got)
	}
	// Midpoint, the average endpoint height, and the unshear the receivers use.
	if got.position != [3]float32{300, 60 + 15, 30} {
		t.Fatalf("stroke position %v", got.position)
	}
	if got.color[1] <= got.color[0] || got.color[1] >= 1 {
		t.Fatalf("stroke colour %v is not the drawn palette colour", got.color)
	}
	// A short stroke keeps the floor of the clamp.
	var short drawlist.List
	short.RecordLine(drawlist.Line{X0: 100, Y0: 60, X1: 110, Y1: 60, Index: 9, Emissive: true, LightingScale: 1})
	r.prepareBattleLighting(&short)
	if r.lighting.lights[0].radius != strokeRadiusMin {
		t.Fatalf("short stroke radius %v", r.lighting.lights[0].radius)
	}
}

func TestFreshWreckLights(t *testing.T) {
	r := &Renderer{}
	g := &drawlist.ModelGeometry{Width: 20, Height: 20, AnchorX: 110, AnchorY: 70, WorldHeight: 8, WreckHeatScale: 1}
	var cold drawlist.List
	cold.RecordModel(drawlist.Model{Geometry: g})
	r.prepareBattleLighting(&cold)
	if len(r.lighting.lights) != 0 {
		t.Fatal("a cooled wreck emitted light")
	}
	g.WreckEmission = [3]float32{0.75, 0.2, 0.02}
	var hot drawlist.List
	hot.RecordModel(drawlist.Model{Geometry: g})
	r.prepareBattleLighting(&hot)
	if len(r.lighting.lights) != 1 {
		t.Fatalf("fresh wreck: %d lights", len(r.lighting.lights))
	}
	got := r.lighting.lights[0]
	if got.kind != lightWreck || got.radius != wreckRadius {
		t.Fatalf("wreck light %+v", got)
	}
	if math.Abs(float64(got.color[0]-0.75*wreckEmissionScale)) > 1e-6 {
		t.Fatalf("wreck colour %v is not its cooling emission", got.color)
	}
	// Centre of the body, unsheared by its own origin height.
	if got.position != [3]float32{120, 80 + 4, 8} {
		t.Fatalf("wreck position %v", got.position)
	}
	// A shadow packet carries the same geometry and must not emit twice.
	var shadow drawlist.List
	shadow.RecordModel(drawlist.Model{Geometry: g, ShadowOnly: true})
	r.prepareBattleLighting(&shadow)
	if len(r.lighting.lights) != 0 {
		t.Fatal("a shadow packet emitted light")
	}
}

// Sources whose reach misses the recorded viewport never reach the budget, so a
// battle offscreen cannot starve the one on screen.
func TestOffscreenSourcesAreCulled(t *testing.T) {
	r := &Renderer{w: 320, h: 200}
	art := warmArt(r)
	var list drawlist.List
	list.RecordWorld(drawlist.WorldSpace{Begin: true, RecordW: 320, RecordH: 200})
	list.RecordSprite(drawlist.Sprite{Frame: art, X: 160, Y: 100, LightingKind: drawlist.SpriteLightingFire, LightingScale: 1})
	list.RecordSprite(drawlist.Sprite{Frame: art, X: 4000, Y: 100, LightingKind: drawlist.SpriteLightingFire, LightingScale: 1})
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 1 || r.lighting.lights[0].position[0] != 160 {
		t.Fatalf("viewport culling: %+v", r.lighting.lights)
	}
}

func TestGroundLightQuadsCulledToViewport(t *testing.T) {
	r := &Renderer{w: 320, h: 200}
	r.lighting.lights = []battleLight{
		{position: [3]float32{160, 100, 0}, color: [3]float32{1, 0.5, 0.2}, radius: 60, kind: lightExplosion},
		{position: [3]float32{-900, 100, 0}, color: [3]float32{1, 0.5, 0.2}, radius: 60, kind: lightFire},
		// Lifted higher than its own reach: no ground point is inside the radius.
		{position: [3]float32{160, 100, 90}, color: [3]float32{1, 0.5, 0.2}, radius: 60, kind: lightProjectile},
	}
	r.appendGroundLights()
	if r.modelStats.GroundLights != 1 || len(r.ground.indices) != 6 {
		t.Fatalf("ground lights %d, %d indices", r.modelStats.GroundLights, len(r.ground.indices))
	}
	v := r.ground.verts
	if v[0].DstX != 100 || v[0].DstY != 40 || v[3].DstX != 220 || v[3].DstY != 160 {
		t.Fatalf("disc quad %v .. %v", v[0], v[3])
	}
	if v[0].Custom0 != 160 || v[0].Custom1 != 100 || v[0].Custom3 != 60 {
		t.Fatalf("disc operands %+v", v[0])
	}
	// A light against the left edge clips to the framebuffer rather than
	// spilling negative geometry.
	r.lighting.lights = []battleLight{{position: [3]float32{4, 100, 0}, color: [3]float32{1, 1, 1}, radius: 60}}
	r.modelStats.GroundLights = 0
	r.appendGroundLights()
	if r.modelStats.GroundLights != 1 || r.ground.verts[0].DstX != 0 {
		t.Fatalf("edge clip %v", r.ground.verts[0])
	}
	// With the switch off the pass builds nothing, so it copies and submits
	// nothing either; the retained batch from the previous frame is never drawn.
	r.lighting.disabled = true
	r.modelStats.GroundLights = 0
	r.drawGroundLighting()
	if r.modelStats.GroundLights != 0 {
		t.Fatal("disabled lighting reached the ground pass")
	}
	r.lighting.disabled = false
	r.lighting.lights = r.lighting.lights[:0]
	r.drawGroundLighting()
	if r.modelStats.GroundLights != 0 {
		t.Fatal("an empty light set reached the ground pass")
	}
}

func TestGroundLightShaderCompiles(t *testing.T) {
	if _, err := ebiten.NewShader([]byte(groundLightShaderSource)); err != nil {
		t.Fatal(err)
	}
}

// groundFixtureTerrain is a flat map of one mid-grey tile, wide enough to cover
// the fixture window: the ground pass has to be checked against a base the
// map's own painting does not vary, so any brightening is the light.
func groundFixtureTerrain(index byte) *world.Terrain {
	t := &world.Terrain{CellW: 16, CellH: 8, TileSet: make([][1024]byte, 1)}
	t.TileIndices = make([]uint16, 8*4)
	for i := range t.TileSet[0] {
		t.TileSet[0][i] = index
	}
	return t
}

// checkGroundAndSourceLightingDevicePixels is the real-device half of §31: a
// stroke lights the ground beneath it, a flame sprite lights a facing model
// face, and with the Lighting switch off every pixel returns to the composite
// the executor draws without the pass.
func checkGroundAndSourceLightingDevicePixels() error {
	if err := checkStrokeGroundLightDevicePixels(); err != nil {
		return err
	}
	return checkFlameLightDevicePixels()
}

func checkStrokeGroundLightDevicePixels() error {
	pal := fixturePalette()
	const w, h = 192, 128
	const groundIndex = 40
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	cam := &camera.Camera{}
	var list drawlist.List
	list.RecordClear()
	list.RecordTerrain(drawlist.Terrain{
		Terrain: groundFixtureTerrain(groundIndex), Cam: cam,
		DstW: w, DstH: h, Scale: camera.ViewScaleNative,
	})
	// One bright horizontal stroke on the ground: 80 record pixels long, so its
	// radius takes the floor of the clamp and its pool is local.
	list.RecordLine(drawlist.Line{X0: 48, Y0: 40, X1: 128, Y1: 40, Index: 250, Emissive: true, LightingScale: 1})
	list.RecordExpand()
	read := func(on bool) ([]byte, error) {
		r.setBattleLighting(on)
		img := r.Execute(&list, w, h)
		if img == nil {
			return nil, fmt.Errorf("ground light fixture returned no image")
		}
		pixels := make([]byte, w*h*4)
		img.ReadPixels(pixels)
		return pixels, nil
	}
	off, err := read(false)
	if err != nil {
		return err
	}
	if r.ModelStats().GroundLights != 0 {
		return fmt.Errorf("ground lights batched with the switch off")
	}
	on, err := read(true)
	if err != nil {
		return err
	}
	if got := r.ModelStats().GroundLights; got != 1 {
		return fmt.Errorf("ground lights = %d, want 1", got)
	}
	at := func(p []byte, x, y int) []byte { return p[(y*w+x)*4 : (y*w+x)*4+4] }
	// Fifteen rows below the stroke: inside the pool, on terrain the stroke
	// itself never wrote.
	lit, base := at(on, 88, 55), at(off, 88, 55)
	if int(lit[0]) <= int(base[0])+4 {
		return fmt.Errorf("ground under the stroke not lit: off %v on %v", base, lit)
	}
	if int(base[0]) != groundIndex {
		return fmt.Errorf("ground fixture base is %v, not the flat tile", base)
	}
	// Beyond the reach the terrain is untouched, and the pass restores exactly.
	if !bytes.Equal(at(on, 10, 118), at(off, 10, 118)) {
		return fmt.Errorf("ground outside the disc changed: %v", at(on, 10, 118))
	}
	again, err := read(false)
	if err != nil {
		return err
	}
	if !bytes.Equal(off, again) {
		return fmt.Errorf("disabling ground lighting did not restore the composite")
	}
	return nil
}

func checkFlameLightDevicePixels() error {
	pal := fixturePalette()
	pal.Base[230] = [4]byte{255, 90, 20, 255}
	const w, h = 240, 140
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	flame := &formats.GAFFrame{Width: 12, Height: 12, XOffset: 6, YOffset: 6, Pixels: bytes.Repeat([]byte{230}, 144)}
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 25})
	for i, normal := range [][3]float32{{1, 0, 0}, {-1, 0, 0}} {
		f := directFace(0, 0, 18, 18, 95, 10, 10)
		f.Normal = normal
		list.RecordModel(drawlist.Model{Geometry: directSubject(62, 48+int32(i)*38, 18, 18, f)})
	}
	// A burning feature's flame strip, tagged by its producer family alone.
	list.RecordSprite(drawlist.Sprite{Frame: flame, X: 105, Y: 65, WorldHeight: 12, LightingScale: 1,
		Kind: drawlist.BlitTinted, LightingKind: drawlist.SpriteLightingFire})
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
	if int(lit[0]) <= int(base[0])+4 || lit[0] <= lit[1] {
		return fmt.Errorf("flame did not light the facing model face: off %v on %v", base, lit)
	}
	if !bytes.Equal(at(on, 70, 95), at(off, 70, 95)) {
		return fmt.Errorf("flame lit a back-facing model face")
	}
	if !bytes.Equal(off, read(false)) {
		return fmt.Errorf("disabling lighting retained the flame source")
	}
	return nil
}

// Feature art records the top-left the blitter writes from, not the frame
// anchor the effect and strip families carry, so a burning feature's light has
// to be placed from the anchor the offsets recover (§31).
func TestBurningFeatureLightUsesFrameAnchor(t *testing.T) {
	r := &Renderer{}
	r.displayPalette[3] = [4]byte{255, 90, 20, 255}
	art := &formats.GAFFrame{Width: 8, Height: 12, XOffset: 4, YOffset: 10, Pixels: bytes.Repeat([]byte{3}, 96)}
	var list drawlist.List
	list.RecordSprite(drawlist.Sprite{Frame: art, X: 96, Y: 48, Kind: drawlist.BlitFeatureNormal,
		LightingKind: drawlist.SpriteLightingFire, LightingScale: 1, WorldHeight: 6})
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 1 {
		t.Fatalf("burning feature: %d lights", len(r.lighting.lights))
	}
	got := r.lighting.lights[0].position
	if got[0] != 100 || got[1] != 58+3 {
		t.Fatalf("feature light at %v, want the anchor (100, 58) unsheared by its height", got)
	}
}
