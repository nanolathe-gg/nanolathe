package gpurender

import (
	"fmt"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func newScorchShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(fmt.Sprintf(scorchShaderSource, float32(drawlist.ScorchFadeStartTicks), float32(drawlist.ScorchLifeTicks))))
}

// ScorchMarks draws authored cooling ground marks beneath objects and fog
// (GPU design §29). Age and seed are replayable values; the GPU has no history.
func (r *Renderer) ScorchMarks(batch drawlist.ScorchMarks) {
	if r == nil || r.sceneDest == nil || !r.scorchEnabled {
		return
	}
	st := &r.water
	if st.mask == nil || st.source != st.record.Terrain || r.scorchShader == nil {
		return
	}
	scale := float32(st.record.Scale.Float())
	if r.sched.worldOn {
		scale *= r.sched.worldScale
	}
	ox, oy := r.sched.inverseOrigin(float32(st.record.OriginX), float32(st.record.OriginY), scale)
	mapping := [4]float32{ox, oy, 1 / scale, float32(st.step)}
	for _, m := range batch.Marks {
		if r.modelStats.ScorchQuads >= drawlist.ScorchMarkLimit {
			break
		}
		if !(m.Radius > 0) || !(m.Age >= 0 && (m.Landing || m.Age < drawlist.ScorchLifeTicks)) {
			continue
		}
		rx, ry := m.Radius, m.Radius*0.7
		x0, y0, x1, y1 := m.X-rx, m.Y-ry, m.X+rx, m.Y+ry
		if !r.sched.beginBlended(schedDest, max(int(math.Floor(float64(x0))), 0), max(int(math.Floor(float64(y0))), 0), min(int(math.Ceil(float64(x1))), r.clipW()), min(int(math.Ceil(float64(y1))), r.clipH()), [4]*ebiten.Image{0: st.mask}, r.scorchShader, blendComposite, schedReadNone) {
			continue
		}
		variant := float32(m.Variant % 64)
		if m.Landing {
			variant += 64 // Dedicated appearance in the existing ground-mark shader.
		}
		r.sched.quadCorners(schedDest, [4]float32{x0, x1, x0, x1}, [4]float32{y0, y0, y1, y1}, mapping, [4][4]float32{{-1, -1, m.Age, variant}, {1, -1, m.Age, variant}, {-1, 1, m.Age, variant}, {1, 1, m.Age, variant}})
		r.modelStats.ScorchQuads++
	}
}

const scorchShaderSource = `//kage:unit pixels
package main

func scorchNoise(p vec2) float {
 a := floor(p)
 f := fract(p)
 f = f*f*(3.0-2.0*f)
 h := vec4(dot(a,vec2(43.17,97.53)),dot(a+vec2(1,0),vec2(43.17,97.53)),dot(a+vec2(0,1),vec2(43.17,97.53)),dot(a+vec2(1,1),vec2(43.17,97.53)))
 h = fract(sin(h)*17341.23)
 return mix(mix(h.x,h.y,f.x),mix(h.z,h.w,f.x),f.y)
}

func scorchMask(p vec2) vec3 {
 q := p-vec2(0.5)
 a := floor(q)+imageSrc0Origin()
 f := fract(q)
 return mix(mix(imageSrc0At(a).rgb,imageSrc0At(a+vec2(1,0)).rgb,f.x),mix(imageSrc0At(a+vec2(0,1)).rgb,imageSrc0At(a+vec2(1,1)).rgb,f.x),f.y)
}

func Fragment(dst vec4, src vec2, color vec4, custom vec4) vec4 {
 screen := dst.xy-imageDstOrigin()
 world := color.rg+screen*color.b
 mask := scorchMask(world/color.a)
 seed := custom.w
 landing := seed >= 64.0
 if landing { seed -= 64.0 }
 p := custom.xy
 age := custom.z
 n := scorchNoise(p*4.1+vec2(seed*2.7,seed*1.3))
 fine := scorchNoise(p*10.0+vec2(seed,31.0))
 // A smooth, seeded uneven boundary avoids a uniform circular stamp.
 radius := length(p)*(0.87+0.25*n)
 edge := 1.0-smoothstep(0.80,1.0,length(p))
 coverage := smoothstep(0.8,1.0,mask.b)*edge
 if coverage <= 0.0 { return vec4(0) }
 // Fade the whole premultiplied mark, preserving the approved warm center.
 fade := 1.0-smoothstep(%[1]f,%[2]f,age)
 scorch := (1.0-smoothstep(0.24,0.86,radius))*(0.6+0.4*fine)*0.34
 hot := (1.0-smoothstep(0.0,0.30,radius))*(1.0-smoothstep(15.0,90.0,age))*(0.45+0.55*fine)
 tint := mix(vec3(0.055,0.042,0.030),vec3(0.92,0.24,0.035),hot)
 alpha := max(scorch,hot*0.50)*fade
 if landing {
  // Compact char with a ragged central patch and a short trailing burn.
  // It changes only ground colour, with no terrain deformation or collision.
  core := (1.0-smoothstep(0.22,0.78,radius))*(0.65+0.35*fine)*0.62
  streak := (1.0-smoothstep(0.10,0.27,abs(p.x+0.07*sin(p.y*11.0))))
  streak *= smoothstep(-0.98,-0.65,p.y)*(1.0-smoothstep(-0.20,0.10,p.y))
  alpha = max(core,streak*(0.30+0.18*n))
  tint = mix(vec3(0.035,0.027,0.020),vec3(0.58,0.12,0.018),hot*0.4)
 }
 alpha = clamp(alpha*coverage,0.0,0.65)
 return vec4(tint*alpha,alpha)
}
`
