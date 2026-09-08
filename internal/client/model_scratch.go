package client

import (
	"github.com/nanolathe/nanolathe/internal/drawlist"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"slices"
	"strings"
)

// Model scratch is owned by a client and borrowed for one recorded frame.
// Slots never overlap within that frame, including nested carrier composition.
// Diagnostic helpers outside recording retain their independently owned results.
type modelScratch struct {
	states      [][]compiledmodel.PieceState
	stateNext   int
	names       map[string]string
	draws       []*presentationrender.DrawScratch
	drawNext    int
	packets     []*packetScratch
	packetNext  int
	active      bool
	polys       []*polyScratch
	polyNext    int
	images      []*imageScratch
	imageNext   int
	outlines    []*outlineScratch
	outlineNext int
}

// outlineScratch is one subject's nanoframe outline: the ring list, the corner
// arena its rings address, and the half-open corner spans used to point the
// rings at the arena once it has stopped moving.
type outlineScratch struct {
	faces []drawlist.ModelFace
	verts []drawlist.ModelVertex
	spans []int32
}
type imageScratch struct {
	target modelTarget
	keys   []uint8
}

type polyScratch struct {
	polys  []screenPoly
	lanes  []int32
	odd    []bool
	corner int
}

// resizeScratch rewinds a borrowed slot's storage to exactly n elements,
// overshooting the request when it has to grow. A slot is reused by whichever
// subject lands in it, so its size drifts from frame to frame; growing to
// exactly the current need makes the next slightly larger subject reallocate
// again, and the composition planes and vertex arenas are the largest per-frame
// buffers the recorder keeps. The returned length is still exactly n, so no
// caller can observe the extra capacity (docs/DESIGN_GPU_RENDERER.md §11.5
// "CPU").
func resizeScratch[T any](v []T, n int) []T {
	if cap(v) < n {
		return slices.Grow(v[:0], n+n/2)[:n]
	}
	return v[:n]
}
func (s *modelScratch) reset() {
	s.polyNext, s.imageNext, s.packetNext, s.drawNext, s.stateNext, s.outlineNext = 0, 0, 0, 0, 0, 0
	// Rewind, do not erase. Both face and polygon storage is fully rewritten
	// before it is read again — a borrowed slot resizes and then assigns every
	// element it hands out — so the only thing a blanket clear achieved was
	// dropping the one pointer these records carry, a texture frame the loaded
	// GAF owns for the life of the process. Erasing every used face and polygon
	// of every subject each frame is real memory traffic bought for nothing
	// (docs/DESIGN_GPU_RENDERER.md §11.5 "CPU"). The lane and corner arrays that
	// a partially written packet WOULD expose are still cleared at borrow time.
	for _, p := range s.packets {
		p.g = drawlist.ModelGeometry{Faces: p.g.Faces[:0]}
	}
	for _, p := range s.polys {
		p.polys = p.polys[:0]
		p.corner = 0
	}
	for _, o := range s.outlines {
		o.faces, o.verts, o.spans = o.faces[:0], o.verts[:0], o.spans[:0]
	}
}

// borrowOutline hands out one subject's outline slot for the frame. Slots never
// overlap within a frame, so a subject's rings stay valid until the next reset.
func (c *Client) borrowOutline() *outlineScratch {
	if c == nil || !c.modelScratch.active {
		return &outlineScratch{}
	}
	s := &c.modelScratch
	if s.outlineNext == len(s.outlines) {
		s.outlines = append(s.outlines, &outlineScratch{})
	}
	o := s.outlines[s.outlineNext]
	s.outlineNext++
	o.faces, o.verts, o.spans = o.faces[:0], o.verts[:0], o.spans[:0]
	return o
}
func (c *Client) borrowPolys(faces, corners int) *polyScratch {
	var p *polyScratch
	if c.modelScratch.active {
		s := &c.modelScratch
		if s.polyNext == len(s.polys) {
			s.polys = append(s.polys, &polyScratch{})
		}
		p = s.polys[s.polyNext]
		s.polyNext++
	} else {
		p = &polyScratch{}
	}
	p.polys = resizeScratch(p.polys, faces)[:0]
	p.lanes = resizeScratch(p.lanes, corners*(2+spanAttrs))
	p.odd = resizeScratch(p.odd, corners)
	clear(p.lanes)
	clear(p.odd)
	p.corner = 0
	return p
}
func (s *polyScratch) face(n int) screenPoly {
	start := s.corner
	s.corner += n
	buf := s.lanes[start*(2+spanAttrs) : s.corner*(2+spanAttrs)]
	p := screenPoly{x: buf[:n:n], y: buf[n : 2*n : 2*n], oddHeight: s.odd[start:s.corner:s.corner]}
	for k := 0; k < spanAttrs; k++ {
		lo := (2 + k) * n
		p.attr[k] = buf[lo : lo+n : lo+n]
	}
	return p
}
func (c *Client) cloneModelPolys(in []screenPoly) []screenPoly {
	corners := 0
	for _, p := range in {
		corners += len(p.x)
	}
	s := c.borrowPolys(len(in), corners)
	for _, p := range in {
		q := s.face(len(p.x))
		qcopy := p
		qcopy.x, qcopy.y, qcopy.attr, qcopy.oddHeight = q.x, q.y, q.attr, q.oddHeight
		copy(qcopy.x, p.x)
		copy(qcopy.y, p.y)
		copy(qcopy.oddHeight, p.oddHeight)
		for k := range p.attr {
			copy(qcopy.attr[k], p.attr[k])
		}
		s.polys = append(s.polys, qcopy)
	}
	return s.polys
}
func (c *Client) borrowModelImage(width, height int, ox, oy, ax, ay int32, key bool, scale int32) *modelTarget {
	if !c.modelScratch.active {
		return newModelImage(width, height, ox, oy, ax, ay, key, scale)
	}
	s := &c.modelScratch
	if s.imageNext == len(s.images) {
		s.images = append(s.images, &imageScratch{})
	}
	slot := s.images[s.imageNext]
	t := &slot.target
	s.imageNext++
	width = max(width, 0)
	height = max(height, 0)
	n := width * height
	colors := resizeScratch(t.color, n)
	covered := resizeScratch(t.covered, n)
	heights := slot.keys
	if key {
		heights = resizeScratch(heights, n)
		clear(heights)
		slot.keys = heights
	} else {
		heights = heights[:0]
	}
	left, right := t.scanLeft, t.scanRight
	*t = modelTarget{color: colors, covered: covered, height: heights, width: width, heightPx: height, originX: ox, originY: oy, anchorX: ax, anchorY: ay, transparent: transparentModelIndex, scale: scale, scanLeft: left, scanRight: right}
	// A missing key plane must remain nil: writers use nil to select painter order.
	if !key {
		t.height = nil
	}
	clear(covered)
	for i := range colors {
		colors[i] = transparentModelIndex
	}
	return t
}

type packetScratch struct {
	g        drawlist.ModelGeometry
	vertices []drawlist.ModelVertex
}

func (c *Client) borrowModelPacket(polys []screenPoly, width, height, ox, oy, ax, ay, scale int32, key bool, fallback drawlist.ModelFallbackReason) *drawlist.ModelGeometry {
	if !c.modelScratch.active {
		return modelGeometryPacketAt(polys, width, height, ox, oy, ax, ay, scale, key, fallback)
	}
	s := &c.modelScratch
	if s.packetNext == len(s.packets) {
		s.packets = append(s.packets, &packetScratch{})
	}
	p := s.packets[s.packetNext]
	s.packetNext++
	count := 0
	for _, f := range polys {
		count += len(f.x)
	}
	p.vertices = resizeScratch(p.vertices, count)
	return fillModelPacket(&p.g, p.vertices, polys, width, height, ox, oy, ax, ay, scale, key, fallback)
}

func (c *Client) borrowDrawScratch() *presentationrender.DrawScratch {
	if !c.modelScratch.active {
		return &presentationrender.DrawScratch{}
	}
	s := &c.modelScratch
	if s.drawNext == len(s.draws) {
		s.draws = append(s.draws, &presentationrender.DrawScratch{})
	}
	d := s.draws[s.drawNext]
	s.drawNext++
	return d
}

func (c *Client) borrowModelStates(n int) []compiledmodel.PieceState {
	if c == nil || !c.modelScratch.active {
		return make([]compiledmodel.PieceState, n)
	}
	s := &c.modelScratch
	if s.stateNext == len(s.states) {
		s.states = append(s.states, nil)
	}
	v := resizeScratch(s.states[s.stateNext], n)
	clear(v)
	s.states[s.stateNext] = v
	s.stateNext++
	return v
}

// Only memoize canonical names, not resolved texture frames: live texture binding
// and animated cursor selection still execute on every presentation.
func (c *Client) modelNameKey(name string) string {
	if name == "" {
		return ""
	}
	if c == nil {
		return strings.ToLower(name)
	}
	s := &c.modelScratch
	if key, ok := s.names[name]; ok {
		return key
	}
	if s.names == nil {
		s.names = make(map[string]string)
	}
	key := strings.ToLower(name)
	s.names[name] = key
	return key
}
