package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Artistic tuning for the requested modern experiment, not retail behavior
// (docs/DESIGN_GPU_RENDERER.md §27). Budget is applied after viewport culling.
const treeHeatLimit = 128

// Retain values only: decoded art belongs to the borrowed draw list and must
// not survive a map reset through this preparation buffer (GPU design §2.3).
type treeHeatSource struct {
	x, y, width, height, time, scale float32
	strength                         float32
	wreck                            bool
	clip                             drawlist.Rect
	hasClip                          bool
}

type treeHeat struct {
	sources  []treeHeatSource
	disabled bool
}

// SetTreeHeat controls the modern prototype for visual comparisons.
func (r *Renderer) SetTreeHeat(on bool) { r.heat.disabled = !on }

func (r *Renderer) prepareTreeHeat(list *drawlist.List) {
	d := &r.heat
	d.sources = d.sources[:0]
	if d.disabled {
		return
	}
	// Wrecks precede trees in the shared draw, preserving tree priority.
	list.VisitModels(func(cmd drawlist.Model) {
		g := cmd.Geometry
		if cmd.ShadowOnly || g == nil || !g.Eligible || g.Fallback != drawlist.ModelFallbackNone || g.WreckHeatStrength <= 0 || g.WreckHeatScale <= 0 {
			return
		}
		b := modelWorldBounds(g)
		d.sources = append(d.sources, treeHeatSource{
			x: float32(b.Min.X), y: float32(b.Min.Y), width: float32(b.Dx()), height: float32(b.Dy()),
			time: g.WreckHeatTime, scale: g.WreckHeatScale, strength: g.WreckHeatStrength, wreck: true,
		})
	})
	list.VisitSprites(func(sp drawlist.Sprite) {
		if sp.HeatSource && sp.Frame != nil && sp.LightingScale > 0 {
			d.sources = append(d.sources, treeHeatSource{
				x: float32(sp.X), y: float32(sp.Y), width: float32(sp.Frame.Width), height: float32(sp.Frame.Height),
				time: sp.HeatTime, scale: sp.LightingScale, strength: 1, clip: sp.Clip, hasClip: sp.HasClip,
			})
		}
	})
}

// Append after blast geometry so heat wins only where its shader covers pixels.
func (r *Renderer) appendTreeHeat() {
	if r.heat.disabled {
		return
	}
	d := &r.distortion
	k := r.sched.txf(1)
	trees, wrecks := 0, 0
	for _, sp := range r.heat.sources {
		if sp.wreck && wrecks >= 32 || !sp.wreck && trees >= treeHeatLimit {
			continue
		}
		scale := sp.scale * k
		width := min(max(sp.width*0.55*k, 14*scale), 38*scale)
		height := min(max(sp.height*1.25*k, 56*scale), 112*scale)
		x := r.sched.txx(sp.x + sp.width*0.5)
		bottom := r.sched.txy(sp.y + sp.height*0.45)
		if sp.wreck {
			// Lower, broader rising air over the wreck's metal body.
			width = min(max(sp.width*0.55*k, 12*scale), 40*scale)
			height = min(max(sp.height*0.85*k, 32*scale), 64*scale)
			bottom = r.sched.txy(sp.y + sp.height*0.65)
		}
		loX, loY, hiX, hiY := float32(0), float32(0), float32(r.w), float32(r.h)
		if sp.hasClip {
			loX, loY = max(loX, r.sched.txx(float32(sp.clip.X))), max(loY, r.sched.txy(float32(sp.clip.Y)))
			hiX, hiY = min(hiX, r.sched.txx(float32(sp.clip.X+sp.clip.W))), min(hiY, r.sched.txy(float32(sp.clip.Y+sp.clip.H)))
		}
		x0, y0, x1, y1 := max(loX, x-width), max(loY, bottom-height), min(hiX, x+width), min(hiY, bottom)
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		base := uint32(len(d.verts))
		for _, p := range [4][2]float32{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
			d.verts = append(d.verts, ebiten.Vertex{DstX: p[0], DstY: p[1],
				SrcX: p[0] + sp.time, SrcY: p[1] + 1 + scale*sp.strength,
				ColorR: loX, ColorG: loY, ColorB: hiX, ColorA: hiY,
				Custom0: x, Custom1: bottom, Custom2: width, Custom3: height})
		}
		d.indices = append(d.indices, base, base+1, base+2, base+1, base+2, base+3)
		r.modelStats.HeatPlumes++
		if sp.wreck {
			wrecks++
			r.modelStats.WreckHeatPlumes++
		} else {
			trees++
		}
	}
}

// Appended to the common distortion shader; both formulas share its sampler.
const treeHeatShaderSource = `
// XY is displacement; Z is coverage so Fragment can discard outside the plume.
func treeHeatOffset(p, src vec2, plume vec4, scale float) vec3 {
 t:=(src.x-imageSrc0Origin().x-p.x)*0.20943951
 rise:=(plume.y-p.y)/plume.w
 // Slow lateral drift, widening aloft, with a soft envelope on every edge.
 center:=plume.x+sin(rise*5.0-t*0.5)*plume.z*0.13*rise
 across:=(p.x-center)/(plume.z*(0.58+0.42*rise))
 if rise<=0.0 || rise>=1.0 || abs(across)>=1.0 { return vec3(0.0) }
 edge:=1.0-across*across
 envelope:=edge*edge*sin(rise*3.14159265)*sin(rise*3.14159265)
 wave:=sin(rise*18.0-t*2.0+across*2.5)+0.35*sin(rise*33.0-t*3.0-across*4.0)
 offset:=vec2(wave,0.24*sin(rise*22.0-t*2.0+across*3.0))*envelope*1.8*scale
 return vec3(offset,1.0)
}
`
