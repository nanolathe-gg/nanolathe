package gpurender

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Enhanced presentation choices, not retail arithmetic (GPU design §23).
// The light budget bounds CPU work without adding model passes or textures.
const battleLightLimit = 64
const subjectLightLimit = 8

type battleLight struct {
	position [3]float32 // recorded screen X, unsheared screen Y, scaled height
	color    [3]float32
	radius   float32
}

type battleLighting struct {
	disabled  bool
	lights    []battleLight
	colors    map[*formats.GAFFrame][3]float32
	nano      [battleLightLimit]nanoLightCluster
	nanoCount int
}

// SetBattleLighting controls the prototype for capture comparisons. It changes
// only this executor's presentation; normal Enhanced rendering enables it.
func (r *Renderer) SetBattleLighting(on bool) { r.lighting.disabled = !on }

// prepareBattleLighting gathers explicitly classified, visible named explosion
// art before any model is rasterized. Smoke and generic bloom flags are never
// sources. Coordinates stay in RECORD space until the ordinary world commit.
func (r *Renderer) prepareBattleLighting(list *drawlist.List) {
	l := &r.lighting
	l.lights = l.lights[:0]
	if l.disabled {
		return
	}
	if l.colors == nil {
		l.colors = make(map[*formats.GAFFrame][3]float32)
	}
	list.VisitLightSources(func(sp drawlist.Sprite) {
		if sp.LightingKind != drawlist.SpriteLightingExplosion || sp.Frame == nil {
			return
		}
		color, ok := l.colors[sp.Frame]
		if !ok {
			color = explosionColor(sp.Frame, &r.displayPalette)
			l.colors[sp.Frame] = color
		}
		if max(color[0], color[1], color[2]) < 0.015 {
			return
		}
		scale := sp.LightingScale
		if scale <= 0 {
			scale = 1
		}
		radius := min(max(float32(max(sp.Frame.Width, sp.Frame.Height))*1.4, 48*scale), 192*scale) * 1.5
		light := battleLight{
			position: [3]float32{float32(sp.X), float32(sp.Y) + sp.WorldHeight*0.5, sp.WorldHeight + float32(sp.Frame.Height)*0.25},
			color:    color, radius: radius,
		}
		// The producer has already checked local visibility. Only art intersecting
		// the recorded clip contributes; offscreen glow is outside this prototype.
		x, y := sp.X-int32(sp.Frame.XOffset), sp.Y-int32(sp.Frame.YOffset)
		if sp.HasClip && (x+int32(sp.Frame.Width) <= sp.Clip.X || y+int32(sp.Frame.Height) <= sp.Clip.Y || x >= sp.Clip.X+sp.Clip.W || y >= sp.Clip.Y+sp.Clip.H) {
			return
		}
		l.add(light)
	})
	r.prepareNanoLighting(list)
	r.modelStats.BattleLights = len(l.lights)
}

// add keeps the strongest sources within the shared budget, with stable ties.
func (l *battleLighting) add(light battleLight) {
	if len(l.lights) < battleLightLimit {
		l.lights = append(l.lights, light)
		return
	}
	weakest := 0
	for i := 1; i < len(l.lights); i++ {
		if lightPower(l.lights[i]) < lightPower(l.lights[weakest]) {
			weakest = i
		}
	}
	if lightPower(light) > lightPower(l.lights[weakest]) {
		l.lights[weakest] = light
	}
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
// use the wrapping composition key; the doubled raster changes XY only.
func (r *Renderer) modelFaceLight(f *drawlist.ModelFace) float32 {
	d := &r.modelDirect
	if d.lightSources.count == 0 || f.Normal == [3]float32{} || len(f.Vertices) == 0 {
		return 0
	}
	var x, y, h float32
	for _, v := range f.Vertices {
		x += float32(v.X)
		y += float32(v.Y)
		h += v.Height
	}
	inv := 1 / float32(len(f.Vertices))
	return packBattleLight(d.lightSources.irradiance(d.lightX+x*inv*d.lightScale, d.lightY+y*inv*d.lightScale, d.lightHeight+h*inv, f.Normal, false))
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
