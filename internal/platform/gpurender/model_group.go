package gpurender

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// A construction child must finish its reveal before joining its carrier:
// transparent child pixels never contribute a staging key [03 R-REN-03A §4].
// Only these groups take the extra composition passes (GPU design §22.4).
func modelGroupNeedsMerge(g *drawlist.ModelGeometry) bool {
	for _, child := range g.Children {
		c := child.Geometry
		if mergeableChild(c) && (c.Reveal != nil || (c.Supersample != nil && c.Supersample.Reveal != nil)) {
			return true
		}
	}
	return false
}

type modelGroupMerge struct {
	parent, child modelDirectRegion
	key           bool
}

type modelGroupMergeLane struct {
	merges                    []modelGroupMerge
	shader                    *ebiten.Shader
	key, colour               *ebiten.Image
	opts                      ebiten.DrawTrianglesShaderOptions
	copy                      ebiten.DrawTrianglesOptions
	vertices                  [4]ebiten.Vertex
	indices                   [6]uint16
	parentOrigin, childOrigin [2]float32
}

// mergeModelGroups runs after every atlas page is ready, so the two members
// may occupy different pages. Children join in recorded order; equality admits
// the later child. Their isolated rasters already carry the shifted key and
// both their own verdicts and the carrier's clip, as in the ordinary lane.
func (r *Renderer) mergeModelGroups() {
	d := &r.modelDirect
	m := &d.groups
	for _, merge := range m.merges {
		parent, child := merge.parent, merge.child
		// Touch only the child's box (including the fattened raster margin).
		// This is also the source guard against reading a neighbouring atlas slot.
		box := child.bounds.Inset(-modelDirectMargin / 2).Intersect(parent.bounds.Inset(-modelDirectMargin / 2))
		if box.Empty() {
			continue
		}
		w, h := box.Dx()*2, box.Dy()*2
		if m.key == nil || m.key.Bounds().Dx() < w || m.key.Bounds().Dy() < h {
			if m.key != nil {
				w, h = max(w, m.key.Bounds().Dx()), max(h, m.key.Bounds().Dy())
				m.key.Deallocate()
				m.colour.Deallocate()
			}
			opts := &ebiten.NewImageOptions{Unmanaged: true}
			m.key = ebiten.NewImageWithOptions(image.Rect(0, 0, w, h), opts)
			m.colour = ebiten.NewImageWithOptions(image.Rect(0, 0, w, h), opts)
		}
		w, h = box.Dx()*2, box.Dy()*2
		px := int(parent.x) + 2*(box.Min.X-parent.bounds.Min.X)
		py := int(parent.y) + 2*(box.Min.Y-parent.bounds.Min.Y)
		cx := int(child.x) + 2*(box.Min.X-child.bounds.Min.X)
		cy := int(child.y) + 2*(box.Min.Y-child.bounds.Min.Y)
		pg, cg := &d.pages[parent.page], &d.pages[child.page]
		if m.opts.Uniforms == nil {
			m.opts.Uniforms = map[string]any{
				"ParentOrigin": m.parentOrigin[:], "ChildOrigin": m.childOrigin[:],
			}
			m.indices = [6]uint16{0, 1, 2, 0, 2, 3}
		}
		m.parentOrigin = [2]float32{float32(px), float32(py)}
		m.childOrigin = [2]float32{float32(cx), float32(cy)}
		m.opts.Images = [4]*ebiten.Image{pg.colour, pg.key, cg.colour, cg.key}
		m.opts.Blend = ebiten.BlendCopy
		for i, xy := range [4][2]int{{0, 0}, {w, 0}, {w, h}, {0, h}} {
			m.vertices[i] = ebiten.Vertex{DstX: float32(xy[0]), DstY: float32(xy[1])}
		}
		// Separate scratch planes avoid sampling an atlas while writing to it.
		for i, dst := range []*ebiten.Image{m.colour, m.key} {
			if i == 1 && !merge.key {
				break
			}
			m.opts.Uniforms["KeyOutput"] = float32(i)
			r.beginPass(dst)
			dst.DrawTrianglesShader(m.vertices[:], m.indices[:], m.shader, &m.opts)
			r.recordSubmission(4, 6)
			r.frameDraws++
		}
		for i, xy := range [4][2]int{{0, 0}, {w, 0}, {w, h}, {0, h}} {
			m.vertices[i] = ebiten.Vertex{DstX: float32(px + xy[0]), DstY: float32(py + xy[1]), SrcX: float32(xy[0]), SrcY: float32(xy[1]), ColorR: 1, ColorG: 1, ColorB: 1, ColorA: 1}
		}
		m.copy.Blend = ebiten.BlendCopy
		for i, dst := range []*ebiten.Image{pg.colour, pg.key} {
			if i == 1 && !merge.key {
				break
			}
			src := m.colour
			if i == 1 {
				src = m.key
			}
			r.beginPass(dst)
			dst.DrawTriangles(m.vertices[:], m.indices[:], src, &m.copy)
			r.recordSubmission(4, 6)
			r.frameDraws++
		}
		r.modelStats.DirectPasses += 2
		if merge.key {
			r.modelStats.DirectPasses += 2
		}
		r.modelStats.DirectGroupMerges++
		r.modelStats.DirectGroupPixels += w * h
	}
	if m.key != nil {
		r.modelStats.DirectGroupScratchBytes = m.key.Bounds().Dx() * m.key.Bounds().Dy() * 8
	}
}

const modelGroupMergeShaderSource = `//kage:unit pixels
package main

var ParentOrigin vec2
var ChildOrigin vec2
var KeyOutput float

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
 p := floor(dstPos.xy-imageDstOrigin()) + vec2(0.5)
 parent := imageSrc0Origin()+ParentOrigin+p
 child := imageSrc0Origin()+ChildOrigin+p
 priorColour := imageSrc0At(parent)
 priorKey := imageSrc1AtFromSrc0Pos(parent)
 childColour := imageSrc2AtFromSrc0Pos(child)
 childKey := imageSrc3AtFromSrc0Pos(child)
 // The finished child, rather than its unconditionally rasterized key,
 // controls staging admission [03 R-REN-03A §4].
 if childColour.a > 0.0 && priorKey.r <= childKey.r {
  if KeyOutput > 0.5 { return childKey }
  return childColour
 }
 if KeyOutput > 0.5 { return priorKey }
 return priorColour
}
`
