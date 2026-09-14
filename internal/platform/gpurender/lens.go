package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

type lensPass struct {
	shader  *ebiten.Shader
	verts   []ebiten.Vertex
	indices []uint32
	opts    ebiten.DrawTrianglesShaderOptions
	// read is the union of the source rectangles the displaced samples come
	// from — the region this lens has to snapshot (readcopy.go).
	read readRect
}

var _ drawlist.LensSink = (*Renderer)(nil)

// Lens is an ordered refraction of the current composite [03 R-FX-01 §4].
// It submits earlier commands, snapshots the sampled region, then copies the
// displaced samples. Later lenses therefore see this lens's completed output.
func (r *Renderer) Lens(l drawlist.Lens) {
	if r == nil || r.lens.shader == nil || r.surfaces[0] == nil || r.surfaces[1] == nil {
		return
	}
	p := &r.lens
	p.verts, p.indices = p.verts[:0], p.indices[:0]
	p.read.reset()
	r.appendLens(l)
	r.drawOverComposite(p.read, p.verts, p.indices, p.shader, &p.opts, blendComposite)
}

// appendLens transforms record coordinates exactly once. Only nonidentity
// samples need geometry: key and sentinel tests cannot change identity pixels.
// The native map is shared with the classic executor; no radial arithmetic or
// animation is invented by the shader (GPU design C-G3, lens replay).
func (r *Renderer) appendLens(l drawlist.Lens) {
	p := &r.lens
	b := l.Bounds()
	key := r.displayPalette[l.Key]
	for y := b.Y; y < b.Y+b.H; y++ {
		for x := b.X; x < b.X+b.W; x++ {
			sx, sy, ok := l.Source(x, y)
			if !ok || (sx == x && sy == y) {
				continue
			}
			dx, dy := r.sched.txx(float32(x)), r.sched.txy(float32(y))
			ex, ey := r.sched.txx(float32(x+1)), r.sched.txy(float32(y+1))
			cx, cy := max(dx, float32(l.Clip.X), 0), max(dy, float32(l.Clip.Y), 0)
			ce, cf := min(ex, float32(l.Clip.X+l.Clip.W), float32(r.w)), min(ey, float32(l.Clip.Y+l.Clip.H), float32(r.h))
			if cx >= ce || cy >= cf {
				continue
			}
			// Crop source and destination together. The fixed map's changed samples
			// point inward, but explicit checks protect malformed authored commands.
			tx, ty := r.sched.txx(float32(sx))+(cx-dx), r.sched.txy(float32(sy))+(cy-dy)
			if tx < 0 {
				cx -= tx
				tx = 0
			}
			if ty < 0 {
				cy -= ty
				ty = 0
			}
			ce = min(ce, cx+float32(r.w)-tx)
			cf = min(cf, cy+float32(r.h)-ty)
			if cx >= ce || cy >= cf {
				continue
			}
			base := uint32(len(p.verts))
			for _, v := range [4][4]float32{{cx, cy, tx, ty}, {ce, cy, tx + ce - cx, ty}, {cx, cf, tx, ty + cf - cy}, {ce, cf, tx + ce - cx, ty + cf - cy}} {
				p.verts = append(p.verts, ebiten.Vertex{DstX: v[0], DstY: v[1], SrcX: v[2], SrcY: v[3], ColorR: float32(key[0]), ColorG: float32(key[1]), ColorB: float32(key[2])})
			}
			p.indices = append(p.indices, base, base+1, base+2, base+1, base+2, base+3)
			// The shader samples the interpolated source point and nothing else,
			// so the source rectangle is exactly what the snapshot must carry.
			p.read.add(tx, ty, tx+ce-cx, ty+cf-cy)
		}
	}
}

func newLensShader() (*ebiten.Shader, error) { return ebiten.NewShader([]byte(lensShaderSource)) }

// The modern composite has no palette identities. The documented lens policy
// compares RGB bytes to the display palette's key color; duplicate colors and
// enhanced nonpalette colors cannot reproduce a retail index-identity test
// (DESIGN_GPU_RENDERER §2.5).
const lensShaderSource = `//kage:unit pixels
package main
func Fragment(dst vec4, src vec2, key vec4) vec4 {
 sample := imageSrc0At(src)
 rgb := floor(sample.rgb*255.0+vec3(0.5))
 if rgb.r == key.r && rgb.g == key.g && rgb.b == key.b { discard() }
 return sample
}
`
