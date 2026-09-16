package gpurender

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Enhanced presentation choices, not retail arithmetic (GPU design §23, §31).
// The light budget bounds CPU work without adding model passes or textures.
const battleLightLimit = 64
const subjectLightLimit = 8

// lightKind is the emitter family a selected source came from. It exists for
// the budget, diagnostics and terrain flash selection; model and smoke
// receivers retain the shared falloff (§31 "Budget", §31.6).
type lightKind uint8

const (
	lightExplosion lightKind = iota
	lightNano
	lightFire
	lightProjectile
	lightWreck
	// lightSpark is a flame-stream TRAIL particle: the flame a burning debris
	// piece drags behind it. It is a spark in flight, not a burning place, so
	// it is its own family with its own reach, energy and budget (§31.7).
	lightSpark
	lightKindCount
)

// lightKindCap is the most sources one kind may hold, and lightKindReserve the
// slots it is guaranteed when the shared budget is full. The reserves sum to
// the budget, so no kind can be evicted below its reserve by another, and the
// caps stop a field of fires or a volley of plasma from filling the budget on
// their own (§31 "Budget"). Explosions and nanolathe clusters keep the
// pre-existing behaviour of competing for the whole budget.
var lightKindCap = [lightKindCount]int{
	lightExplosion: battleLightLimit, lightNano: battleLightLimit,
	lightFire: 16, lightProjectile: 24, lightWreck: 16, lightSpark: 16,
}

// One unit's death throws dozens of burning pieces, so sparks take their own
// small reserve rather than sharing the standing fire's: a debris shower can no
// longer take fire below eight slots, where it could previously take it below
// twelve and could fill fire's whole cap (§31.7).
//
// The reserves are named constants and the array is built from those names, so
// the partition assertion below constrains the same symbols the table uses. It
// restated its own literals before, and an edit to the table alone compiled.
const (
	reserveExplosion = 24
	reserveNano      = 12
	reserveFire      = 8
	reserveProj      = 12
	reserveWreck     = 4
	reserveSpark     = 4
)

var lightKindReserve = [lightKindCount]int{
	lightExplosion: reserveExplosion, lightNano: reserveNano, lightFire: reserveFire,
	lightProjectile: reserveProj, lightWreck: reserveWreck, lightSpark: reserveSpark,
}

// The reserves are a partition of the budget; this fails to compile otherwise.
const _ = uint(battleLightLimit - (reserveExplosion + reserveNano + reserveFire + reserveProj + reserveWreck + reserveSpark))
const _ = uint(reserveExplosion + reserveNano + reserveFire + reserveProj + reserveWreck + reserveSpark - battleLightLimit)

// Artistic constants for the added emitter families (§31). Radii are world
// pixels at record scale; the record scale multiplies them at gather time.
const (
	fireRadiusMin    = 128
	fireRadiusMax    = 160
	fireFlickerTicks = 15 // about half a second at the 30 Hz tick
	fireDimPeak      = 0.25
	// fireEnergy lifts the measured flame emission into the range the rest of
	// the prototype was tuned for. Burning art measures 0.39-0.51 peak on the
	// reference install, well under an explosion's, so without this a fire's
	// light is technically present and visually unreadable (§31.1).
	fireEnergy = 1.6
	// A spark is measured from its own art with a narrow clamp: it stands for
	// a burning fragment, not a burning place, so it never takes the standing
	// fire's wide floor. sparkEnergy stays well under fireEnergy for the same
	// reason (§31.7).
	sparkRadiusMin     = 20
	sparkRadiusMax     = 56
	sparkEnergy        = 0.9
	projRadiusMin      = 56
	projRadiusMax      = 96
	strokeRadiusMin    = 48
	strokeRadiusMax    = 160
	strokeEnergy       = 0.8
	wreckRadius        = 80
	wreckEmissionScale = 0.6
)

// fireWarmFallback is the hue a flame source takes when the installed art
// measures too dim to carry one. Its ENERGY still comes from the measurement,
// so a dying fire still fades (§31).
var fireWarmFallback = [3]float32{0.95, 0.55, 0.18}

type battleLight struct {
	position [3]float32 // recorded screen X, unsheared screen Y, scaled height
	color    [3]float32
	radius   float32
	// Receiver-specific terrain flash timing; model/smoke color stays intact (§31.6).
	age      float32
	ageKnown bool
	// ground is the terrain height under the source, in the same units as
	// position[2]. ONLY the ground pass reads it: it attenuates by the source's
	// height above the GROUND rather than above the sea datum, which is the
	// receiver height §31.3 had to do without (§31.7). Model and smoke
	// receivers keep the physical source of §23.2 untouched.
	ground float32
	// fade is the source's own remaining emission and fadeKnown whether its
	// producer carried one at all, so a spent source's zero is distinct from a
	// family that never fades. Only the terrain receiver applies it (§31.7).
	fade      float32
	fadeKnown bool
	kind      lightKind
}

type battleLighting struct {
	disabled  bool
	lights    []battleLight
	counts    [lightKindCount]int
	colors    map[*formats.GAFFrame][3]float32
	nano      [battleLightLimit]nanoLightCluster
	nanoCount int
	// recordW, recordH are the record-space viewport sources are culled
	// against. Gathering precedes replay, so the framebuffer size is not the
	// record extent below a rest zoom factor (§16.3).
	recordW, recordH float32
}

// setBattleLighting is the executor gate the player's Lighting switch drives
// (§30). It changes only this executor's presentation.
func (r *Renderer) setBattleLighting(on bool) { r.lighting.disabled = !on }

// prepareBattleLighting gathers explicitly classified, visible emitter art
// before any model is rasterized. Smoke and generic bloom flags are never
// sources. Coordinates stay in RECORD space until the ordinary world commit.
//
// Six families reach the budget: named explosion art, nanolathe clusters
// (§23.5), standing flame strips, flame-stream TRAIL sparks (§31.7), emissive
// projectile bodies and emissive strokes, and cooling fresh wrecks (§31). Every one of them is already
// visibility-admitted by its producer, and the player's Lighting switch gates
// the whole gather.
func (r *Renderer) prepareBattleLighting(list *drawlist.List) {
	l := &r.lighting
	l.lights = l.lights[:0]
	l.counts = [lightKindCount]int{}
	r.modelStats.BattleLights = 0
	r.modelStats.BattleLightKinds = [lightKindCount]int{}
	if l.disabled {
		return
	}
	if l.colors == nil {
		l.colors = make(map[*formats.GAFFrame][3]float32)
	}
	w, h := list.RecordedWorldExtent()
	l.recordW, l.recordH = float32(w), float32(h)
	if w <= 0 || h <= 0 {
		l.recordW, l.recordH = float32(r.clipW()), float32(r.clipH())
	}
	list.VisitLightSources(func(sp drawlist.Sprite) { r.addSpriteLight(sp) })
	r.prepareStrokeLighting(list)
	r.prepareWreckLighting(list)
	r.prepareNanoLighting(list)
	r.modelStats.BattleLights = len(l.lights)
	r.modelStats.BattleLightKinds = l.counts
}

// addSpriteLight admits one classified emitter sprite. Explosions use their
// sequence extent for reach and current frame for colour, so an expanding
// animation cannot postpone its flash (§23.2). Fire and projectiles keep their
// per-frame radius clamp, and fire flickers on emitted strength (§31).
func (r *Renderer) addSpriteLight(sp drawlist.Sprite) {
	l := &r.lighting
	var kind lightKind
	switch sp.LightingKind {
	case drawlist.SpriteLightingExplosion:
		kind = lightExplosion
	case drawlist.SpriteLightingFire:
		kind = lightFire
	case drawlist.SpriteLightingProjectile:
		kind = lightProjectile
	case drawlist.SpriteLightingSpark:
		kind = lightSpark
	default:
		return
	}
	if sp.Frame == nil {
		return
	}
	color, ok := l.colors[sp.Frame]
	if !ok {
		color = explosionColor(sp.Frame, &r.displayPalette)
		l.colors[sp.Frame] = color
	}
	peak := max(color[0], color[1], color[2])
	if peak < 0.015 {
		return
	}
	scale := sp.LightingScale
	if scale <= 0 {
		scale = 1
	}
	// The recorded placement differs by family: effect, projectile and strip art
	// carry the frame ANCHOR, while feature art carries the top-left the blitter
	// writes from [03 §5.3.1]. Normalising to the anchor keeps one position and
	// one clip test for every emitter.
	ax, ay := float32(sp.X), float32(sp.Y)
	if sp.Kind == drawlist.BlitFeatureNormal {
		ax += float32(sp.Frame.XOffset)
		ay += float32(sp.Frame.YOffset)
	}
	art := float32(max(sp.Frame.Width, sp.Frame.Height))
	var radius float32
	switch kind {
	case lightFire:
		// Flame art is small but the fire it stands for lights a wide patch of
		// ground, so its clamp floor is well above the art's own size.
		radius = min(max(art*1.4, fireRadiusMin*scale), fireRadiusMax*scale)
		// A measured hue below the warm threshold is the installed art being too
		// dark to carry one; keep its energy and take the authored warm hue.
		if peak < fireDimPeak {
			color = [3]float32{fireWarmFallback[0] * peak, fireWarmFallback[1] * peak, fireWarmFallback[2] * peak}
		}
		strength := fireEnergy * fireFlicker(sp.LightingTime, ax, ay)
		for j := range color {
			color[j] *= strength
		}
	case lightSpark:
		radius = min(max(art*1.4, sparkRadiusMin*scale), sparkRadiusMax*scale)
		// A spark's art measures as dim as a standing flame's, so it takes the
		// same warm fallback — at the spark family's own lower energy, and with
		// no flicker, whose phase hash is a position hash and so re-rolls every
		// frame under anything that moves (§31.5, §31.7).
		if peak < fireDimPeak {
			color = [3]float32{fireWarmFallback[0] * peak, fireWarmFallback[1] * peak, fireWarmFallback[2] * peak}
		}
		for j := range color {
			color[j] *= sparkEnergy
		}
	case lightProjectile:
		radius = min(max(art*1.4, projRadiusMin*scale), projRadiusMax*scale)
	default:
		// The first flash can be much smaller than the following dim art. A
		// radius that grows with that art delays illumination at receivers near
		// its reach, especially above elevated ground (§23.2, §31.3).
		if sp.LightingSize > 0 {
			art = sp.LightingSize * scale
		}
		radius = min(max(art*1.4, 48*scale), 192*scale) * 1.5
	}
	// The producer has already checked local visibility. Only art intersecting
	// the recorded clip contributes; offscreen glow is outside this prototype.
	x, y := int32(ax)-int32(sp.Frame.XOffset), int32(ay)-int32(sp.Frame.YOffset)
	if sp.HasClip && (x+int32(sp.Frame.Width) <= sp.Clip.X || y+int32(sp.Frame.Height) <= sp.Clip.Y || x >= sp.Clip.X+sp.Clip.W || y >= sp.Clip.Y+sp.Clip.H) {
		return
	}
	if !l.inRecordView(ax, ay, radius) {
		return
	}
	l.add(battleLight{
		position: [3]float32{ax, ay + sp.WorldHeight*0.5, sp.WorldHeight + float32(sp.Frame.Height)*0.25},
		color:    color, radius: radius, kind: kind, age: sp.LightingAge, ageKnown: sp.HasLightingAge,
		ground: max(sp.LightingGround, 0),
		fade:   min(max(sp.LightingFade, 0), 1), fadeKnown: sp.HasLightingFade,
	})
}

// prepareStrokeLighting admits the emissive beam and lightning strokes. A
// stroke has no art to measure: its colour is the palette colour the executor
// actually draws it in, at a fixed fraction of full energy, and its physical
// height comes from the committed endpoints the recorder carried (§31).
func (r *Renderer) prepareStrokeLighting(list *drawlist.List) {
	l := &r.lighting
	list.VisitLines(func(line drawlist.Line) {
		if !line.Emissive {
			return
		}
		c := r.displayPalette[line.Index]
		color := [3]float32{
			float32(c[0]) / 255 * strokeEnergy,
			float32(c[1]) / 255 * strokeEnergy,
			float32(c[2]) / 255 * strokeEnergy,
		}
		if max(color[0], color[1], color[2]) < 0.015 {
			return
		}
		scale := line.LightingScale
		if scale <= 0 {
			scale = 1
		}
		dx := float32(line.X1 - line.X0)
		dy := float32(line.Y1 - line.Y0)
		length := float32(math.Sqrt(float64(dx*dx + dy*dy)))
		radius := min(max(0.6*length, strokeRadiusMin*scale), strokeRadiusMax*scale)
		midX := float32(line.X0+line.X1) * 0.5
		midY := float32(line.Y0+line.Y1) * 0.5
		height := (line.WorldHeight0 + line.WorldHeight1) * 0.5
		if !l.inRecordView(midX, midY, radius) {
			return
		}
		l.add(battleLight{
			position: [3]float32{midX, midY + height*0.5, height},
			color:    color, radius: radius, kind: lightProjectile,
		})
	})
}

// prepareWreckLighting admits a fresh wreck's own cooling emission as light.
// The emission and its cooling curve belong to the wreck prototype (§28); this
// only borrows the colour, so a wreck that has cooled emits nothing and no new
// clock is read (§31).
func (r *Renderer) prepareWreckLighting(list *drawlist.List) {
	l := &r.lighting
	list.VisitModels(func(cmd drawlist.Model) {
		g := cmd.Geometry
		if cmd.ShadowOnly || g == nil {
			return
		}
		peak := max(g.WreckEmission[0], g.WreckEmission[1], g.WreckEmission[2])
		if peak*wreckEmissionScale < 0.015 {
			return
		}
		scale := g.WreckHeatScale
		if scale <= 0 {
			scale = 1
		}
		b := modelWorldBounds(g)
		x := float32(b.Min.X+b.Max.X) * 0.5
		y := float32(b.Min.Y+b.Max.Y) * 0.5
		radius := wreckRadius * scale
		if !l.inRecordView(x, y, radius) {
			return
		}
		l.add(battleLight{
			position: [3]float32{x, y + g.WorldHeight*0.5, g.WorldHeight},
			color: [3]float32{
				g.WreckEmission[0] * wreckEmissionScale,
				g.WreckEmission[1] * wreckEmissionScale,
				g.WreckEmission[2] * wreckEmissionScale,
			},
			radius: radius, kind: lightWreck,
		})
	})
}

// inRecordView rejects a source whose reach misses the recorded viewport, so an
// offscreen battle cannot consume budget a visible one needs.
func (l *battleLighting) inRecordView(x, y, radius float32) bool {
	if l.recordW <= 0 || l.recordH <= 0 {
		return true
	}
	return x+radius > 0 && y+radius > 0 && x-radius < l.recordW && y-radius < l.recordH
}

// fireFlicker is the bounded [0.75, 1] multiplier a flame source's emission
// takes. The argument is committed ticks plus the presentation fraction, so a
// replayed or paused frame reproduces it exactly; the per-source phase is a
// hash of the recorded position, so neighbouring fires do not pulse together
// and no RNG stream is touched (§31).
func fireFlicker(t, x, y float32) float32 {
	phase := lightPhase(x, y)
	wave := 0.5 + 0.5*float32(math.Sin(float64(t*(2*math.Pi/fireFlickerTicks)+phase)))
	return 0.75 + 0.25*wave
}

// lightPhase maps a recorded position to a fixed phase in [0, 2pi). It is a
// plain integer mix, deterministic and independent of any simulation stream.
func lightPhase(x, y float32) float32 {
	h := uint32(int32(x))*73856093 ^ uint32(int32(y))*19349663
	h ^= h >> 13
	h *= 2654435761
	return float32(h>>22) * (2 * math.Pi / 1024)
}

// add keeps the strongest sources within the shared budget, with stable ties.
// A kind at its cap competes only with itself. When the budget is full the slot
// is taken from the kind furthest ABOVE its reserve, so no family can be pushed
// below its guaranteed share by another (§31 "Budget").
func (l *battleLighting) add(light battleLight) {
	k := light.kind
	if k >= lightKindCount {
		return
	}
	if l.counts[k] >= lightKindCap[k] {
		if at := l.weakestOf(k); at >= 0 && lightPower(light) > lightPower(l.lights[at]) {
			l.lights[at] = light
		}
		return
	}
	if len(l.lights) < battleLightLimit {
		l.lights = append(l.lights, light)
		l.counts[k]++
		return
	}
	victim, over := -1, 0
	for i := lightKind(0); i < lightKindCount; i++ {
		if d := l.counts[i] - lightKindReserve[i]; d > over {
			if at := l.weakestOf(i); at >= 0 {
				victim, over = at, d
			}
		}
	}
	if victim < 0 {
		victim = l.weakestOf(k)
	}
	if victim < 0 {
		return
	}
	// Taking a slot from an over-share kind is the point of the reserve; within
	// one kind the stronger source still wins, with stable ties.
	if l.lights[victim].kind == k && lightPower(light) <= lightPower(l.lights[victim]) {
		return
	}
	l.counts[l.lights[victim].kind]--
	l.lights[victim] = light
	l.counts[k]++
}

// weakestOf is the index of the dimmest light of one kind, or -1 when the kind
// holds none. Ties keep the earliest, so selection is record-order stable.
func (l *battleLighting) weakestOf(k lightKind) int {
	at := -1
	for i := range l.lights {
		if l.lights[i].kind != k {
			continue
		}
		if at < 0 || lightPower(l.lights[i]) < lightPower(l.lights[at]) {
			at = i
		}
	}
	return at
}

func lightPower(l battleLight) float32 { return max(l.color[0], l.color[1], l.color[2]) }

// explosionColor measures the actual frame's bright covered texels once. Dark
// trailing frames lose energy naturally; hue follows the installed art. This
// is artistic emission extraction, not material classification.
func explosionColor(f *formats.GAFFrame, pal *[256][4]byte) (out [3]float32) {
	if len(f.Subframes) != 0 {
		return compositeExplosionColor(f, pal)
	}
	var weight float32
	count := 0
	for i, index := range f.Pixels {
		if i < len(f.Transparent) && f.Transparent[i] {
			continue
		}
		count++
		c := pal[index]
		peak := float32(max(c[0], c[1], c[2])) / 255
		w := max((peak-0.45)/0.55, 0)
		w *= w
		weight += w
		for j := range out {
			out[j] += float32(c[j]) / 255 * w
		}
	}
	if weight == 0 || count == 0 {
		return [3]float32{}
	}
	energy := float32(math.Sqrt(float64(weight / float32(count))))
	for j := range out {
		out[j] = out[j] / weight * energy
	}
	return out
}

type subjectLights struct {
	lights [subjectLightLimit]battleLight
	count  int
}

// near chooses local sources once per subject. A screen-space bound is
// conservative for our half-height shear; final falloff uses unsheared distance.
func (l *battleLighting) near(x, y, extent float32) (out subjectLights) {
	var scores [subjectLightLimit]float32
	for _, light := range l.lights {
		dx := light.position[0] - x
		dy := light.position[1] - light.position[2]*0.5 - y
		reach := light.radius*1.5 + extent
		if dx*dx+dy*dy > reach*reach {
			continue
		}
		score := lightPower(light) / (1 + (dx*dx+dy*dy)/(light.radius*light.radius))
		at := out.count
		if at == subjectLightLimit {
			at = 0
			for i := 1; i < out.count; i++ {
				if scores[i] < scores[at] {
					at = i
				}
			}
			if score <= scores[at] {
				continue
			}
		} else {
			out.count++
		}
		out.lights[at], scores[at] = light, score
	}
	return out
}

// irradiance uses outward normals for models. Smoke is a soft scattering
// receiver and accepts light from either side, without becoming an emitter.
func (s *subjectLights) irradiance(x, y, height float32, normal [3]float32, smoke bool) (rgb [3]float32) {
	for i := 0; i < s.count; i++ {
		light := &s.lights[i]
		dx, dy, dh := light.position[0]-x, light.position[1]-(y+height*0.5), light.position[2]-height
		d2 := dx*dx + dy*dy + dh*dh
		if d2 >= light.radius*light.radius {
			continue
		}
		falloff := 1 - d2/(light.radius*light.radius)
		falloff *= falloff
		response := float32(0.8)
		strength := float32(2.5)
		if !smoke {
			response = max((dx*normal[0]+dy*normal[1]+dh*normal[2])/float32(math.Sqrt(float64(max(d2, 1)))), 0)
			// Give armour a clearer flash without increasing smoke scattering.
			strength = 3.25
		}
		gain := falloff * response * strength
		for j := range rgb {
			rgb[j] += light.color[j] * gain
		}
	}
	return rgb
}

// packBattleLight stores three numeric base-128 digits, not float bit patterns.
// Each lane is [0,2] illumination; a constant value across the face avoids
// interpolation between packed channels. The 21-bit maximum leaves rounding headroom in the
// device float, including across channel carry boundaries (GPU design §23).
func packBattleLight(rgb [3]float32) float32 {
	var packed uint32
	for i := range rgb {
		packed |= uint32(min(max(rgb[i], 0), 2)*63.5+0.5) << (7 * i)
	}
	return float32(packed)
}

// modelFaceLight evaluates a flat face at its physical centroid. Heights never
// use the wrapping composition key; the doubled raster changes XY only. The
// centroid and the evaluation are separate so a retained lane can keep the
// one and repeat the other (model_retain.go) through the same arithmetic.
func (r *Renderer) modelFaceLight(f *drawlist.ModelFace) float32 {
	d := &r.modelDirect
	if d.lightSources.count == 0 || f.Normal == [3]float32{} || len(f.Vertices) == 0 {
		return 0
	}
	x, y, h := modelFaceCentre(f)
	return d.lightAt(x, y, h, f.Normal)
}

// modelFaceCentre is a face's mean corner in raster-local pixels and its mean
// physical height.
func modelFaceCentre(f *drawlist.ModelFace) (x, y, h float32) {
	for _, v := range f.Vertices {
		x += float32(v.X)
		y += float32(v.Y)
		h += v.Height
	}
	inv := 1 / float32(len(f.Vertices))
	return x * inv, y * inv, h * inv
}

// lightAt packs the subject's chosen sources' irradiance at a raster-local
// centroid (x, y) and relative height h for a face of normal n.
func (d *modelDirectLane) lightAt(x, y, h float32, n [3]float32) float32 {
	return packBattleLight(d.lightSources.irradiance(d.lightX+x*d.lightScale, d.lightY+y*d.lightScale, d.lightHeight+h, n, false))
}

const battleLightShaderSource = `
func battleLight(packed float) vec3 {
 p := floor(packed+0.5)
 r := mod(p, 128.0)
 g := mod(floor(p/128.0), 128.0)
 b := floor(p/16384.0)
 return vec3(r,g,b)/63.5
}

func battleLit(albedo vec3, shade float, packed float) vec3 {
 base := albedo*shade
 if packed < 0.5 { return base }
 // Add diffuse reflected light while retaining texture and bright cores.
 return min(base + albedo*battleLight(packed), vec3(1.0))
}
`

// Composite source measurement follows ordered leaf coverage over black, once
// per immutable frame. A bounded sampling grid avoids an image allocation and
// includes alternate (half-alpha) children, which PlainPixels omits.
func compositeExplosionColor(f *formats.GAFFrame, pal *[256][4]byte) (out [3]float32) {
	if f.Width == 0 || f.Height == 0 {
		return
	}
	nx, ny := min(int(f.Width), 32), min(int(f.Height), 32)
	var weights float32
	count := 0
	for y := 0; y < ny; y++ {
		for x := 0; x < nx; x++ {
			px := (2*x+1)*int(f.Width)/(2*nx) - int(f.XOffset)
			py := (2*y+1)*int(f.Height)/(2*ny) - int(f.YOffset)
			rgb, covered := compositeEmissionAt(f, px, py, pal, false, [3]float32{}, false)
			if !covered {
				continue
			}
			count++
			w := max((max(rgb[0], rgb[1], rgb[2])-0.45)/0.55, 0)
			w *= w
			weights += w
			for j := range out {
				out[j] += rgb[j] * w
			}
		}
	}
	if weights == 0 || count == 0 {
		return [3]float32{}
	}
	energy := float32(math.Sqrt(float64(weights / float32(count))))
	for j := range out {
		out[j] = out[j] / weights * energy
	}
	return
}

func compositeEmissionAt(f *formats.GAFFrame, x, y int, pal *[256][4]byte, tinted bool, rgb [3]float32, covered bool) ([3]float32, bool) {
	if f == nil {
		return rgb, covered
	}
	if len(f.Subframes) != 0 {
		for _, child := range f.Subframes {
			if child != nil {
				rgb, covered = compositeEmissionAt(child, x, y, pal, tinted || child.AlternateBlitter != 0, rgb, covered)
			}
		}
		return rgb, covered
	}
	col, row := x+int(f.XOffset), y+int(f.YOffset)
	if col < 0 || row < 0 || col >= int(f.Width) || row >= int(f.Height) {
		return rgb, covered
	}
	i := row*int(f.Width) + col
	if i >= len(f.Pixels) || (i < len(f.Transparent) && f.Transparent[i]) {
		return rgb, covered
	}
	c := pal[f.Pixels[i]]
	for j := range rgb {
		v := float32(c[j]) / 255
		if tinted {
			rgb[j] = (rgb[j] + v) * 0.5
		} else {
			rgb[j] = v
		}
	}
	return rgb, true
}
