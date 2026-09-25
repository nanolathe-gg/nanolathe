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

// The merges run in waves (GPU design §22.4 "Construction-child composition").
// A render pass, not a draw, is the device's unit of cost (§11.5), and merging
// one child at a time opened up to four passes per child, two of them over a
// whole atlas page. A wave instead evaluates the merge of every one of its
// children into a slot of its own in one scratch pair, then copies every slot
// back onto its carrier's page: one pass per scratch plane and one per page
// plane it writes, however many children it holds.
//
// Inside a wave every read sees the pages as the wave found them. For a merge
// that reads nothing an earlier merge of the wave writes, that is what merging
// in order gives, so a merge that does read such a rectangle — a carrier's next
// child, above all — starts the next wave. A merge that only writes where an
// earlier one read stays: in order, that read came first too. Two merges of one
// wave therefore never write the same texel.
const (
	// modelGroupScratchW is the width a wave's slots are shelf-packed into, and
	// modelGroupScratchH the height its shelves may reach before the next slot
	// starts a new wave. A slot larger than either takes a wave of its own and
	// the scratch grows to hold it.
	modelGroupScratchW = 2048
	modelGroupScratchH = 2048
)

// modelGroupSlot is one merge laid out for its wave: its box's 2× rectangles
// on the parent's and the child's pages, and the scratch rectangle its result
// is evaluated into.
type modelGroupSlot struct {
	sx, sy, w, h          int
	px, py, cx, cy        int
	parentPage, childPage int32
	key                   bool
}

type modelGroupMergeLane struct {
	merges []modelGroupMerge
	// slots is the frame's merges whose box is not empty, in merge order, and
	// waves the index in slots of each wave's first merge.
	slots  []modelGroupSlot
	waves  []int
	shader *ebiten.Shader
	// key and colour are the scratch pair every wave evaluates into, retained
	// across frames and grown to the largest frame's waves.
	key, colour *ebiten.Image
	opts        ebiten.DrawTrianglesShaderOptions
	copy        ebiten.DrawTrianglesOptions
	// keyOutput is the shader's plane selector, one element written in place
	// so the uniform is never reboxed.
	keyOutput []float32
	verts     []ebiten.Vertex
	idx       []uint32
}

// mergeModelGroups runs after every atlas page is ready, so the two members
// may occupy different pages. Children join in recorded order; equality admits
// the later child. Their isolated rasters already carry the shifted key and
// both their own verdicts and the carrier's clip, as in the ordinary lane.
func (r *Renderer) mergeModelGroups() {
	m := &r.modelDirect.groups
	m.planWaves()
	if len(m.slots) != 0 {
		m.ensureScratch()
		passes := r.modelStats.Passes
		for i, first := range m.waves {
			end := len(m.slots)
			if i+1 < len(m.waves) {
				end = m.waves[i+1]
			}
			r.drawModelGroupWave(m.slots[first:end])
		}
		r.modelStats.DirectPasses += r.modelStats.Passes - passes
	}
	for i := range m.slots {
		r.modelStats.DirectGroupMerges++
		r.modelStats.DirectGroupPixels += m.slots[i].w * m.slots[i].h
	}
	if m.colour != nil {
		b := m.colour.Bounds()
		r.modelStats.DirectGroupScratchBytes = b.Dx() * b.Dy() * 8
	}
}

// planWaves lays the frame's merges out as slots and divides them into waves.
// A merge touches only the child's box, the atlas margin included, where it
// meets the parent's; that is also the guard against reading a neighbouring
// atlas slot. A merge whose two boxes do not meet has nothing to do.
func (m *modelGroupMergeLane) planWaves() {
	m.slots, m.waves = m.slots[:0], m.waves[:0]
	x, y, shelf := 0, 0, 0
	closed := false // the wave holds an oversized slot and takes no other
	for _, merge := range m.merges {
		parent, child := merge.parent, merge.child
		box := child.bounds.Inset(-modelDirectMargin / 2).Intersect(parent.bounds.Inset(-modelDirectMargin / 2))
		if box.Empty() {
			continue
		}
		s := modelGroupSlot{
			w: box.Dx() * 2, h: box.Dy() * 2,
			px: int(parent.x) + 2*(box.Min.X-parent.bounds.Min.X), py: int(parent.y) + 2*(box.Min.Y-parent.bounds.Min.Y),
			cx: int(child.x) + 2*(box.Min.X-child.bounds.Min.X), cy: int(child.y) + 2*(box.Min.Y-child.bounds.Min.Y),
			parentPage: parent.page, childPage: child.page, key: merge.key,
		}
		oversized := s.w > modelGroupScratchW || s.h > modelGroupScratchH
		if x+s.w > modelGroupScratchW {
			x, y, shelf = 0, y+shelf, 0
		}
		if len(m.waves) == 0 || closed || oversized || y+s.h > modelGroupScratchH || s.reads(m.slots[m.waves[len(m.waves)-1]:]) {
			m.waves = append(m.waves, len(m.slots))
			x, y, shelf = 0, 0, 0
		}
		s.sx, s.sy = x, y
		m.slots = append(m.slots, s)
		x += s.w
		shelf = max(shelf, s.h)
		closed = oversized
	}
}

// reads reports whether s reads a page rectangle one of wave's merges writes.
// A merge reads both planes of its parent's rectangle and its child's, and
// writes its parent's.
func (s *modelGroupSlot) reads(wave []modelGroupSlot) bool {
	parent := image.Rect(s.px, s.py, s.px+s.w, s.py+s.h)
	child := image.Rect(s.cx, s.cy, s.cx+s.w, s.cy+s.h)
	for i := range wave {
		e := &wave[i]
		written := image.Rect(e.px, e.py, e.px+e.w, e.py+e.h)
		if (e.parentPage == s.parentPage && written.Overlaps(parent)) || (e.parentPage == s.childPage && written.Overlaps(child)) {
			return true
		}
	}
	return false
}

// ensureScratch grows the scratch pair to hold every wave of the frame. Its
// two planes stay separate from the pages so no pass samples a page it writes.
func (m *modelGroupMergeLane) ensureScratch() {
	w, h := 0, 0
	for i := range m.slots {
		w, h = max(w, m.slots[i].sx+m.slots[i].w), max(h, m.slots[i].sy+m.slots[i].h)
	}
	if m.colour != nil {
		b := m.colour.Bounds()
		if b.Dx() >= w && b.Dy() >= h {
			return
		}
		w, h = max(w, b.Dx()), max(h, b.Dy())
		m.colour.Deallocate()
		m.key.Deallocate()
	}
	opts := &ebiten.NewImageOptions{Unmanaged: true}
	m.colour = ebiten.NewImageWithOptions(image.Rect(0, 0, w, h), opts)
	m.key = ebiten.NewImageWithOptions(image.Rect(0, 0, w, h), opts)
}

// drawModelGroupWave evaluates one wave into the scratch pair, one draw per
// run of consecutive merges reading the same two pages, then copies the slots
// back in merge order, one draw per page and plane. Only a merge whose key has
// a later reader writes the key plane.
func (r *Renderer) drawModelGroupWave(wave []modelGroupSlot) {
	d := &r.modelDirect
	m := &d.groups
	if m.opts.Uniforms == nil {
		m.keyOutput = make([]float32, 1)
		m.opts.Uniforms = map[string]any{"KeyOutput": m.keyOutput}
	}
	m.opts.Blend = ebiten.BlendCopy
	m.copy.Blend = ebiten.BlendCopy
	for plane, dst := range [2]*ebiten.Image{m.colour, m.key} {
		m.keyOutput[0] = float32(plane)
		m.verts, m.idx = m.verts[:0], m.idx[:0]
		var parentPage, childPage int32
		for i := range wave {
			s := &wave[i]
			if plane == 1 && !s.key {
				continue
			}
			if len(m.idx) != 0 && (s.parentPage != parentPage || s.childPage != childPage) {
				r.evaluateModelGroupRun(dst, parentPage, childPage)
			}
			parentPage, childPage = s.parentPage, s.childPage
			// The fragment reads the parent's and the child's texels at its
			// own offset within the slot.
			base := uint32(len(m.verts))
			for _, c := range [4][2]int{{0, 0}, {s.w, 0}, {s.w, s.h}, {0, s.h}} {
				m.verts = append(m.verts, ebiten.Vertex{
					DstX: float32(s.sx + c[0]), DstY: float32(s.sy + c[1]),
					Custom0: float32(s.px - s.sx), Custom1: float32(s.py - s.sy),
					Custom2: float32(s.cx - s.sx), Custom3: float32(s.cy - s.sy),
				})
			}
			m.idx = append(m.idx, base, base+1, base+2, base, base+2, base+3)
		}
		if len(m.idx) != 0 {
			r.evaluateModelGroupRun(dst, parentPage, childPage)
		}
	}
	for p := range d.pages {
		pg := &d.pages[p]
		for plane, dst := range [2]*ebiten.Image{pg.colour, pg.key} {
			src := m.colour
			if plane == 1 {
				src = m.key
			}
			m.verts, m.idx = m.verts[:0], m.idx[:0]
			for i := range wave {
				s := &wave[i]
				if int(s.parentPage) != p || (plane == 1 && !s.key) {
					continue
				}
				base := uint32(len(m.verts))
				for _, c := range [4][2]int{{0, 0}, {s.w, 0}, {s.w, s.h}, {0, s.h}} {
					m.verts = append(m.verts, ebiten.Vertex{
						DstX: float32(s.px + c[0]), DstY: float32(s.py + c[1]),
						SrcX: float32(s.sx + c[0]), SrcY: float32(s.sy + c[1]),
						ColorR: 1, ColorG: 1, ColorB: 1, ColorA: 1,
					})
				}
				m.idx = append(m.idx, base, base+1, base+2, base, base+2, base+3)
			}
			if len(m.idx) == 0 {
				continue
			}
			r.beginPass(dst)
			dst.DrawTriangles32(m.verts, m.idx, src, &m.copy)
			r.recordSubmission(len(m.verts), len(m.idx))
			r.frameDraws++
		}
	}
}

// evaluateModelGroupRun draws the pending slots of one pair of pages into dst.
func (r *Renderer) evaluateModelGroupRun(dst *ebiten.Image, parentPage, childPage int32) {
	d := &r.modelDirect
	m := &d.groups
	pg, cg := &d.pages[parentPage], &d.pages[childPage]
	m.opts.Images = [4]*ebiten.Image{pg.colour, pg.key, cg.colour, cg.key}
	r.beginPass(dst)
	dst.DrawTrianglesShader32(m.verts, m.idx, m.shader, &m.opts)
	r.recordSubmission(len(m.verts), len(m.idx))
	r.frameDraws++
	m.verts, m.idx = m.verts[:0], m.idx[:0]
}

// The merge function, one texel of a slot per fragment: custom.xy is the
// parent's texel less the slot's, custom.zw the child's.
const modelGroupMergeShaderSource = `//kage:unit pixels
package main

var KeyOutput float

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
 p := floor(dstPos.xy-imageDstOrigin()) + vec2(0.5)
 parent := imageSrc0Origin()+custom.xy+p
 child := imageSrc0Origin()+custom.zw+p
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
