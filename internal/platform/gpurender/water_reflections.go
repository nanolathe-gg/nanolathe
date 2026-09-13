package gpurender

import (
	"fmt"
	"image"
	"math"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Reflections reuse visible model colour, projected corners and physical height.
// They add no scene camera or simulation state (GPU design §26.4).
const reflectionVertexLimit = 32768

type reflectionRun struct {
	page                                 int32
	frame                                *formats.GAFFrame
	first, count, firstIndex, indexCount int
}

type waterReflections struct {
	disabled                                       bool
	active                                         *drawlist.ModelGeometry
	region                                         modelDirectRegion
	verts, transformed                             []ebiten.Vertex
	indices                                        []uint32
	softTiles                                      []bool
	runs                                           []reflectionRun
	source, height                                 *ebiten.Image
	sourceShader, resolveShader, softResolveShader *ebiten.Shader
}

// SetWaterReflections isolates this treatment for capture comparisons.
func (r *Renderer) SetWaterReflections(on bool) { r.reflections.disabled = !on }

func (s *waterReflections) resetFrame() {
	s.active = nil
	s.verts, s.indices, s.runs = s.verts[:0], s.indices[:0], s.runs[:0]
}

func (s *waterReflections) run(page int32, f *formats.GAFFrame) *reflectionRun {
	if n := len(s.runs); n > 0 && s.runs[n-1].page == page && s.runs[n-1].frame == f {
		return &s.runs[n-1]
	}
	s.runs = append(s.runs, reflectionRun{page: page, frame: f, first: len(s.verts), firstIndex: len(s.indices)})
	return &s.runs[len(s.runs)-1]
}

func (s *waterReflections) fan(run *reflectionRun, n int) {
	base := uint32(len(s.verts) - n - run.first)
	for i := 1; i+1 < n; i++ {
		s.indices = append(s.indices, base, base+uint32(i), base+uint32(i+1))
	}
	run.count += n
	run.indexCount += 3 * (n - 2)
}

// Called after the ordinary face has passed front-face admission. The original
// key/quad mapping rejects source pixels hidden by another piece. Atlas colour
// already contains material shading, construction reveal and waterline verdicts.
func (r *Renderer) reflectModelFace(f *drawlist.ModelFace, ox, oy, scale, cx, cy, fat float32, quad int, page int32) {
	s := &r.reflections
	g := s.active
	n := len(f.Vertices)
	if g == nil || !g.ReflectWater || s.disabled || r.water.disabled || n < 3 || n > 256 || len(s.verts)+max(n, 3*(n-2)) > reflectionVertexLimit-4096 {
		return
	}
	high := float32(0)
	for _, v := range f.Vertices {
		high = max(high, g.WorldHeight+v.Height-g.ReflectionSea)
	}
	if high <= 0 {
		return
	}
	run := s.run(page, nil)
	for _, v := range f.Vertices {
		sx, sy := ox+fattenBy(float32(v.X), cx, scale, fat), oy+fattenBy(float32(v.Y), cy, scale, fat)
		height := g.WorldHeight + v.Height - g.ReflectionSea
		// The camera subtracts half physical height [03 §2.5]. Reflecting that
		// height about sea adds one full above-water height to screen Y.
		// Keep the ground footprint intact, including a diagonal hull's slope.
		x := float32(s.region.bounds.Min.X) + (sx-float32(s.region.x))*.5
		sourceY := float32(s.region.bounds.Min.Y) + (sy-float32(s.region.y))*.5
		y := sourceY + height
		s.verts = append(s.verts, ebiten.Vertex{DstX: x, DstY: y, SrcX: sx, SrcY: sy, ColorA: 1,
			Custom0: height, Custom1: r.modelDirect.laneKey(v.Key), Custom2: float32(quad), Custom3: 0})
	}
	if quad != 0 {
		s.fan(run, n)
		return
	}
	// Each unmapped triangle must compare its key at the sampled atlas texel
	// centre. Interpolation at a different subpixel point can reject its own
	// sloping face. Retain that triangle's source-space key gradient.
	var corners [256]ebiten.Vertex
	copy(corners[:], s.verts[len(s.verts)-n:])
	s.verts = s.verts[:len(s.verts)-n]
	for i := 1; i+1 < n; i++ {
		a, b, c := corners[0], corners[i], corners[i+1]
		dx1, dy1, dx2, dy2 := b.SrcX-a.SrcX, b.SrcY-a.SrcY, c.SrcX-a.SrcX, c.SrcY-a.SrcY
		det := dx1*dy2 - dx2*dy1
		if det == 0 {
			continue
		}
		k1, k2 := b.Custom1-a.Custom1, c.Custom1-a.Custom1
		gx, gy := (k1*dy2-k2*dy1)/det, (dx1*k2-dx2*k1)/det
		for _, v := range [3]ebiten.Vertex{a, b, c} {
			v.ColorR, v.ColorG = gx, gy
			s.verts = append(s.verts, v)
		}
		s.fan(run, 3)
	}
}

func (r *Renderer) prepareProjectileReflections(l *drawlist.List) {
	s := &r.reflections
	if s.disabled || r.water.disabled {
		return
	}
	l.VisitSprites(func(sp drawlist.Sprite) {
		if !sp.ReflectWater || sp.ReflectionHeight <= 0 || sp.Frame == nil || sp.Kind != drawlist.BlitKeyed || len(s.verts)+4 > reflectionVertexLimit {
			return
		}
		f := sp.Frame
		x, y := float32(sp.X), float32(sp.Y)
		if sp.Anchored {
			x -= float32(f.XOffset)
			y -= float32(f.YOffset)
		}
		w, h := float32(f.Width), float32(f.Height)
		if w <= 0 || h <= 0 {
			return
		}
		// Billboard art has no per-pixel physical depth; mirror around its anchor's
		// water-plane projection. Only model packets can clip individual pieces.
		pivot := float32(sp.Y) + sp.ReflectionHeight*.5
		y0, y1 := 2*pivot-y, 2*pivot-(y+h)
		run := s.run(-1, f)
		for _, v := range [4][4]float32{{x, y0, 0, 0}, {x + w, y0, w, 0}, {x + w, y1, w, h}, {x, y1, 0, h}} {
			s.verts = append(s.verts, ebiten.Vertex{DstX: v[0], DstY: v[1], SrcX: v[2], SrcY: v[3], ColorA: 1, Custom0: sp.ReflectionHeight, Custom3: 1})
		}
		s.fan(run, 4)
	})
	l.VisitLines(func(l drawlist.Line) {
		if !l.ReflectWater || max(l.ReflectionHeight0, l.ReflectionHeight1) <= 0 || len(s.verts)+4 > reflectionVertexLimit {
			return
		}
		x0, y0, x1, y1 := float32(l.X0), float32(l.Y0)+l.ReflectionHeight0, float32(l.X1), float32(l.Y1)+l.ReflectionHeight1
		dx, dy := x1-x0, y1-y0
		length := float32(math.Hypot(float64(dx), float64(dy)))
		if length < 1 {
			dx, dy, length = 1, 0, 1
		}
		nx, ny := -dy/length, dx/length
		run := s.run(-1, nil)
		for _, v := range [4][3]float32{{x0 + nx, y0 + ny, l.ReflectionHeight0}, {x1 + nx, y1 + ny, l.ReflectionHeight1}, {x1 - nx, y1 - ny, l.ReflectionHeight1}, {x0 - nx, y0 - ny, l.ReflectionHeight0}} {
			s.verts = append(s.verts, ebiten.Vertex{DstX: v[0], DstY: v[1], ColorA: 1, Custom0: v[2], Custom1: float32(l.Index), Custom3: 2})
		}
		s.fan(run, 4)
	})
}

func (r *Renderer) drawWaterReflections(c drawlist.Terrain) {
	s, st := &r.reflections, &r.water
	if s.disabled || st.disabled || !c.Water.Enabled || st.mask == nil || !st.visibleWater(c) || len(s.verts) == 0 || s.sourceShader == nil || s.resolveShader == nil || s.softResolveShader == nil {
		return
	}
	if s.source == nil || s.source.Bounds().Dx() != r.w || s.source.Bounds().Dy() != r.h {
		if s.source != nil {
			s.source.Deallocate()
		}
		s.source = ebiten.NewImageWithOptions(image.Rect(0, 0, r.w, r.h), &ebiten.NewImageOptions{Unmanaged: true})
	}
	s.source.Clear()
	scale := float32(c.Scale.Float())
	time := (float32(c.Water.Tick) + float32(c.Water.Fraction16)/65536) / 30
	s.transformed = append(s.transformed[:0], s.verts...)
	for i := range s.transformed {
		v := &s.transformed[i]
		// Displace only the reflected mesh, retaining its original atlas samples.
		// Shared corners move together; boats receive a small baseline wobble.
		// Height ramp and wave are Enhanced art choices (GPU design §26.6).
		if v.Custom3 == 0 {
			amount := max(0, min((v.Custom0/scale-64)/96, 1))
			amount = max(.35, amount*amount*(3-2*amount))
			x, y := float32(c.OriginX)+v.DstX/scale, float32(c.OriginY)+v.DstY/scale
			wave := math.Sin(float64(y*.65+time*1.7)) + .35*math.Sin(float64(x*.11-y*.31-time*1.1))
			v.DstX += float32(wave) * 3 * amount * scale
		}
		v.DstX = r.sched.txf(v.DstX)
		v.DstY = r.sched.txf(v.DstY)
	}
	effective := r.sched.txf(scale)
	w, h := min(int(c.DstW), r.clipW()), min(int(c.DstH), r.clipH())
	soften := s.markSofteningTiles(w, h, scale, effective, c.Water.Energy)
	// A second colour-sized buffer carries premultiplied blur strength/coverage.
	// It reuses the admitted mesh and visibility verdict, never a scene camera.
	if soften {
		if s.height == nil || s.height.Bounds() != s.source.Bounds() {
			if s.height != nil {
				s.height.Deallocate()
			}
			s.height = ebiten.NewImageWithOptions(s.source.Bounds(), &ebiten.NewImageOptions{Unmanaged: true})
		}
		s.height.Clear()
	}
	targets := [2]*ebiten.Image{s.source, nil}
	if soften {
		targets[1] = s.height
	}
	for metadata, target := range targets {
		if target == nil {
			continue
		}
		for _, run := range s.runs {
			op := ebiten.DrawTrianglesShaderOptions{Uniforms: map[string]any{"Metadata": float32(metadata), "RecordScale": scale, "Surface": []float32{float32(c.OriginX), float32(c.OriginY), 1 / effective, time}}}
			if run.page >= 0 {
				pg := &r.modelDirect.pages[run.page]
				op.Images = [4]*ebiten.Image{pg.colour, pg.key, nil, r.modelDirect.params.img}
			} else {
				op.Images[0] = r.placeholderImage()
				op.Images[1] = r.tables.atlas
				if run.frame != nil {
					op.Images[0] = r.gafImageFor(run.frame)
				}
			}
			if op.Images[0] == nil {
				continue
			}
			for i := range op.Images {
				if op.Images[i] == nil {
					op.Images[i] = r.placeholderImage()
				}
			}
			r.beginPass(target)
			target.DrawTrianglesShader32(s.transformed[run.first:run.first+run.count], s.indices[run.firstIndex:run.firstIndex+run.indexCount], s.sourceShader, &op)
			r.frameDraws++
			r.recordSubmission(run.count, run.indexCount)
		}
	}
	r.modelStats.ReflectionVertices = len(s.verts)
	images := [4]*ebiten.Image{s.source, st.mask}
	if soften {
		images[2] = s.height
	}
	emit := func(x0, y0, x1, y1 int, soft bool) {
		flag := float32(0)
		if soft {
			flag = 1
		}
		r.sched.quad(schedDest, float32(x0), float32(y0), float32(x1), float32(y1), 0, 0, 0, 0,
			[4]float32{float32(c.OriginX), float32(c.OriginY), 1 / effective, float32(st.step)},
			[4]float32{time, c.Water.Energy, effective, flag})
	}
	if !soften {
		if r.sched.beginBlended(schedDest, 0, 0, w, h, images, s.resolveShader, blendComposite, schedReadNone) {
			emit(0, 0, w, h, false)
		}
		return
	}
	// Adjacent cells of the same kind share a quad and scheduler submission.
	cols := (w + reflectionSoftTile - 1) / reflectionSoftTile
	// Separate shader variants keep the large filter's register/sample cost
	// out of ordinary water cells. The two sets of quads never overlap.
	for _, soft := range []bool{false, true} {
		shader := s.resolveShader
		if soft {
			shader = s.softResolveShader
		}
		if !r.sched.beginBlended(schedDest, 0, 0, w, h, images, shader, blendComposite, schedReadNone) {
			return
		}
		for y := 0; y < h; y += reflectionSoftTile {
			row := y / reflectionSoftTile * cols
			for x := 0; x < cols; {
				end := x + 1
				for end < cols && s.softTiles[row+end] == s.softTiles[row+x] {
					end++
				}
				if s.softTiles[row+x] == soft {
					emit(x*reflectionSoftTile, y, min(end*reflectionSoftTile, w), min(y+reflectionSoftTile, h), soft)
				}
				x = end
			}
		}
	}
}

const reflectionSoftTile = 64

// Bound the expensive filter by elevated triangles, including its full gather
// footprint and water displacement. Geometry bounds only cull shader work;
// per-source height still determines every sample's weight (GPU design §26.6).
func (s *waterReflections) markSofteningTiles(w, h int, scale, effective, energy float32) bool {
	cols, rows := (w+reflectionSoftTile-1)/reflectionSoftTile, (h+reflectionSoftTile-1)/reflectionSoftTile
	n := cols * rows
	if cap(s.softTiles) < n {
		s.softTiles = make([]bool, n)
	} else {
		s.softTiles = s.softTiles[:n]
		clear(s.softTiles)
	}
	toRecord := scale / effective
	margin := 2 + effective*(.65+energy*.65) + max(float32(4), effective)
	any := false
	for _, run := range s.runs {
		if run.page < 0 {
			continue
		}
		for i := run.firstIndex; i < run.firstIndex+run.indexCount; i += 3 {
			a, b, c := s.transformed[run.first+int(s.indices[i])], s.transformed[run.first+int(s.indices[i+1])], s.transformed[run.first+int(s.indices[i+2])]
			if max(a.Custom0, b.Custom0, c.Custom0) <= 64*scale || min(a.Custom0, b.Custom0, c.Custom0) >= 320*scale {
				continue
			}
			x0, y0 := max(0, int(math.Floor(float64((min(a.DstX, b.DstX, c.DstX)-margin)*toRecord)))), max(0, int(math.Floor(float64((min(a.DstY, b.DstY, c.DstY)-margin)*toRecord))))
			x1, y1 := min(w, int(math.Ceil(float64((max(a.DstX, b.DstX, c.DstX)+margin)*toRecord)))), min(h, int(math.Ceil(float64((max(a.DstY, b.DstY, c.DstY)+margin)*toRecord))))
			if x0 >= x1 || y0 >= y1 {
				continue
			}
			for y := y0 / reflectionSoftTile; y <= (y1-1)/reflectionSoftTile; y++ {
				for x := x0 / reflectionSoftTile; x <= (x1-1)/reflectionSoftTile; x++ {
					s.softTiles[y*cols+x] = true
				}
			}
			any = true
		}
	}
	return any
}

func newReflectionSourceShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(`//kage:unit pixels
package main
var Metadata float
var RecordScale float
var Surface vec4
` + modelQuadMapperSource + `
func Fragment(dst vec4, src vec2, color vec4, custom vec4) vec4 {
 if custom.x<=0 { return vec4(0) }
 var c vec4
 if custom.w<0.5 {
  p:=floor(src-imageSrc0Origin())
  key:=floor(custom.y+dot(color.rg,p+vec2(.5)-(src-imageSrc0Origin())))
  if custom.z>0.5 { key=floor(modelQuadLanes(custom.z,p).z) }
  key=key-floor(key/256)*256
  stored:=floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+p+vec2(.5)).r*255+.5)
  if key<stored { return vec4(0) }
  c=imageSrc0At(imageSrc0Origin()+p+vec2(.5))
 } else {
  idx:=custom.y
  if custom.w<1.5 {
   tex:=imageSrc0At(src)
   if tex.g<.5 { return vec4(0) }
   idx=floor(tex.r*255+.5)
  }
  c=vec4(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+.5,` + fmt.Sprint(tableRowPAL) + `.5)).rgb,1)
 }
 // Enhanced prototype: retain a faint model reflection through flight heights.
 // These are artistic fade distances, not retail constants (GPU design §26.6).
 end:=160.0
 if custom.w<0.5 {
  end=320.0
  distance:=smoothstep(64*RecordScale,160*RecordScale,custom.x)
  world:=Surface.xy+(dst.xy-imageDstOrigin())*Surface.z
  band:=.5+.5*sin(world.y*.9+sin(world.x*.075-Surface.w*.7)+Surface.w*1.7)
  c*=(1-.65*distance)*(1-.3*distance*band)
 }
 c*=1-smoothstep(64*RecordScale,end*RecordScale,custom.x)
 if Metadata>.5 {
  strength:=0.0
  if custom.w<.5 { strength=smoothstep(64*RecordScale,200*RecordScale,custom.x) }
  return vec4(strength*c.a,0,0,c.a)
 }
 return c
}
`))
}

func newReflectionResolveShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(strings.Replace(reflectionResolveShaderSource, "if custom.w<.5 {", "if true {", 1)))
}

func newSoftReflectionResolveShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(strings.Replace(reflectionResolveShaderSource, "if custom.w<.5 {", "if false {", 1)))
}

const reflectionResolveShaderSource = `//kage:unit pixels
package main
func sample(p vec2) vec4 {
 b:=floor(p-.5)+.5
 f:=fract(p-.5)
 o:=imageSrc0Origin()
 return mix(mix(imageSrc0At(o+b),imageSrc0At(o+b+vec2(1,0)),f.x),mix(imageSrc0At(o+b+vec2(0,1)),imageSrc0At(o+b+vec2(1,1)),f.x),f.y)
}
// Weight each source texel before interpolation. A nearby high reflection must
// not lend its blur strength to a boat, or change total filter energy.
func weightedTexel(p vec2, low, high float) vec4 {
 o:=imageSrc0Origin()
 c:=imageSrc0At(o+p)
 if c.a<=0 { return vec4(0) }
 h:=imageSrc2AtFromSrc0Pos(o+p)
 strength:=0.0
 if h.a>0 { strength=clamp(h.r/h.a,0,1) }
 return c*mix(low,high,strength)
}
func weightedSample(p vec2, low, high float) vec4 {
 b:=floor(p-.5)+.5
 f:=fract(p-.5)
 return mix(mix(weightedTexel(b,low,high),weightedTexel(b+vec2(1,0),low,high),f.x),mix(weightedTexel(b+vec2(0,1),low,high),weightedTexel(b+vec2(1,1),low,high),f.x),f.y)
}
// Shoreline coverage on the same bilinear/smoothstep terms the water surface
// and the wake shader use. A nearest mask rejection drew the reflection edge
// as a step of whole mask texels, which are several world pixels wide on a
// large map. Coordinates are mask texels in the source-0 convention.
func waterCoverage(p vec2) float {
 q:=p-vec2(0.5)
 a:=floor(q)
 f:=fract(q)
 o:=imageSrc0Origin()
 m:=mix(mix(imageSrc1AtFromSrc0Pos(o+a).r,imageSrc1AtFromSrc0Pos(o+a+vec2(1,0)).r,f.x),mix(imageSrc1AtFromSrc0Pos(o+a+vec2(0,1)).r,imageSrc1AtFromSrc0Pos(o+a+vec2(1,1)).r,f.x),f.y)
 return smoothstep(0.8,1.0,m)
}
func Fragment(dst vec4,src vec2,color vec4,custom vec4) vec4 {
 p:=dst.xy-imageDstOrigin()
 world:=color.xy+p*color.z
 coverage:=waterCoverage(world/color.w)
 if coverage<=0 { return vec4(0) }
 t:=custom.x
 ripple:=sin(world.y*.19+t*1.9+sin(world.x*.07-t*.6))*.65+sin(world.y*.37-t*1.3)*.35
 q:=p+vec2(ripple*(.65+custom.y*.65)*custom.z,0)
 var c vec4
 if custom.w<.5 {
  c=sample(q)*.5+(sample(q+vec2(custom.z,0))+sample(q-vec2(custom.z,0)))*.25
 } else {
  // Mix the original filter with a filled, bounded cross kernel per source.
  // A fixed screen-pixel kernel stays connected even for one-pixel details.
  step:=1.0
  c=weightedSample(q,.5,0)
  c+=weightedSample(q+vec2(custom.z,0),.25,0)+weightedSample(q-vec2(custom.z,0),.25,0)
  c+=weightedTexel(floor(q)+.5,0,.25)
  c+=weightedTexel(floor(q+vec2(step,0))+.5,0,.12)+weightedTexel(floor(q-vec2(step,0))+.5,0,.12)
  c+=weightedTexel(floor(q+vec2(2*step,0))+.5,0,.09)+weightedTexel(floor(q-vec2(2*step,0))+.5,0,.09)
  c+=weightedTexel(floor(q+vec2(3*step,0))+.5,0,.06)+weightedTexel(floor(q-vec2(3*step,0))+.5,0,.06)
  c+=weightedTexel(floor(q+vec2(4*step,0))+.5,0,.03)+weightedTexel(floor(q-vec2(4*step,0))+.5,0,.03)
  c+=weightedTexel(floor(q+vec2(0,step))+.5,0,.06)+weightedTexel(floor(q-vec2(0,step))+.5,0,.06)
  c+=weightedTexel(floor(q+vec2(0,2*step))+.5,0,.015)+weightedTexel(floor(q-vec2(0,2*step))+.5,0,.015)
 }
 c.rgb*=vec3(.76,.88,.94)
 // The result is premultiplied, so one factor fades colour and coverage alike.
 return c*(.25*coverage)
}
`
