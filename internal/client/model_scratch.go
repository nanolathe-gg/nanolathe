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
	states     [][]compiledmodel.PieceState
	stateNext  int
	names      map[string]string
	draws      []*presentationrender.DrawScratch
	drawNext   int
	packets    []*packetScratch
	packetNext int
	active     bool
	polys      []*polyScratch
	polyNext   int
	images     []*imageScratch
	imageNext  int
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

func resizeScratch[T any](v []T, n int) []T {
	if cap(v) < n {
		return slices.Grow(v[:0], n)[:n]
	}
	return v[:n]
}
func (s *modelScratch) reset() {
	s.polyNext, s.imageNext, s.packetNext, s.drawNext, s.stateNext = 0, 0, 0, 0, 0
	for _, p := range s.packets {
		clear(p.g.Faces)
		p.g = drawlist.ModelGeometry{Faces: p.g.Faces[:0]}
	}
	// Drop texture references in unused slots while retaining owned lane storage.
	for _, p := range s.polys {
		clear(p.polys)
		p.polys = p.polys[:0]
		p.corner = 0
	}
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
