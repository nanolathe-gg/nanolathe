package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
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
	// projectiles are the standalone model calls' slots. A projectile's parent
	// and child calls are live together, so each call takes its own slot.
	projectiles    []*presentationrender.ProjectileScratch
	projectileNext int
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
	polys   []screenPoly
	lanes   []int32
	odd     []bool
	heights []float32
	corner  int
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
	s.projectileNext = 0
	// Preserve warm storage. Face records also address corner arrays: their
	// obsolete tails are cleared when a slot is refilled, while polygons drop
	// those references when their lane storage changes. A blanket per-frame
	// polygon clear would erase data whose backing arrays the slot still owns
	// [DESIGN_GPU_RENDERER.md §11.5 "CPU"].
	for _, p := range s.packets {
		// Preserve the previous face length so refill can clear only the removed
		// tail. The rest of each packet is frame-local, including the doubled
		// packet's outline, reveal and shadow references.
		// The face arena is the slot's own fill arena, never the faces the
		// packet last addressed: a rebased packet addresses its copy arena or
		// a retained lane's own faces (rebaseRetained), and a refill writes
		// through the slice it is given.
		p.g = drawlist.ModelGeometry{Faces: p.faces}
		p.supersample = drawlist.ModelGeometry{}
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
	if cap(p.lanes) < corners*(polyLanes+spanAttrs) || cap(p.odd) < corners {
		// This slot has not been borrowed yet this frame. Dropping its old lane
		// references is safe now; a later slot's growth cannot touch these records.
		clear(p.polys[:cap(p.polys)])
	}
	p.polys = resizeScratch(p.polys, faces)[:0]
	p.lanes = resizeScratch(p.lanes, corners*(polyLanes+spanAttrs))
	p.odd = resizeScratch(p.odd, corners)
	p.heights = resizeScratch(p.heights, corners)
	// Corner lanes are erased as next hands them out: a slot is sized for every
	// face the subject has, and a lane walk (the live pieces, the front faces)
	// usually takes a small part of it.
	p.corner = 0
	return p
}

// next extends the borrowed slot by one face, points its corner lanes at the
// slot's arena and returns a pointer so the caller fills the face in place.
// borrowPolys sized the slot for every face the subject can emit, so the slot
// never moves and the pointer stays valid for the frame. The record is erased
// rather than copied in: the slot carries the previous subject's face, and a
// polygon is large enough that constructing one as a value and copying it into
// the slot costs more than the erase.
func (s *polyScratch) next(n int) *screenPoly {
	start := s.corner
	s.corner += n
	buf := s.lanes[start*(polyLanes+spanAttrs) : s.corner*(polyLanes+spanAttrs)]
	s.polys = s.polys[:len(s.polys)+1]
	p := &s.polys[len(s.polys)-1]
	*p = screenPoly{}
	p.x, p.y = buf[:n:n], buf[n:2*n:2*n]
	p.x2, p.y2 = buf[2*n:3*n:3*n], buf[3*n:4*n:4*n]
	p.oddHeight = s.odd[start:s.corner:s.corner]
	p.heights = s.heights[start:s.corner:s.corner]
	clear(buf)
	clear(p.oddHeight)
	clear(p.heights)
	for k := 0; k < spanAttrs; k++ {
		lo := (polyLanes + k) * n
		p.attr[k] = buf[lo : lo+n : lo+n]
	}
	return p
}
func (c *Client) cloneModelPolys(in []screenPoly) []screenPoly {
	corners := 0
	for i := range in {
		corners += len(in[i].x)
	}
	s := c.borrowPolys(len(in), corners)
	for i := range in {
		p := &in[i]
		q := s.next(len(p.x))
		x, y, x2, y2, odd, attr := q.x, q.y, q.x2, q.y2, q.oddHeight, q.attr
		heights := q.heights
		*q = *p
		q.x, q.y, q.x2, q.y2, q.oddHeight, q.attr = x, y, x2, y2, odd, attr
		q.heights = heights
		copy(q.heights, p.heights)
		copy(q.x, p.x)
		copy(q.y, p.y)
		copy(q.x2, p.x2)
		copy(q.y2, p.y2)
		copy(q.oddHeight, p.oddHeight)
		for k := range p.attr {
			copy(q.attr[k], p.attr[k])
		}
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
	g                drawlist.ModelGeometry
	vertices         []drawlist.ModelVertex
	cachedFaces      []drawlist.ModelFace
	cachedVertices   []drawlist.ModelVertex
	supersample      drawlist.ModelGeometry
	supersampleFaces []drawlist.ModelFace
	supersampleVerts []drawlist.ModelVertex
	// faces is the arena fillModelPacket writes this slot's faces into.
	faces []drawlist.ModelFace
}

func (c *Client) borrowPacketScratch() *packetScratch {
	s := &c.modelScratch
	if s.packetNext == len(s.packets) {
		s.packets = append(s.packets, &packetScratch{})
	}
	p := s.packets[s.packetNext]
	s.packetNext++
	return p
}

func (c *Client) borrowModelPacket(polys []screenPoly, width, height, ox, oy, ax, ay, scale int32, key bool, fallback drawlist.ModelFallbackReason) *drawlist.ModelGeometry {
	if !c.modelScratch.active {
		return modelGeometryPacketAt(polys, width, height, ox, oy, ax, ay, scale, key, fallback)
	}
	p := c.borrowPacketScratch()
	count := 0
	for _, f := range polys {
		count += len(f.x)
	}
	p.vertices = resizeScratch(p.vertices, count)
	g := fillModelPacket(&p.g, p.vertices, polys, width, height, ox, oy, ax, ay, scale, key, fallback, nil)
	p.faces = g.Faces
	return g
}

// borrowModelPacketDoubled fills a scale-2 packet from unplaced polygons with
// the supersample placement (doubledPlacement), the doubled counterpart of
// borrowModelPacket with a native box of width×height at the placement's
// origin.
func (c *Client) borrowModelPacketDoubled(polys []screenPoly, width, height int, place doubledPlacement, key bool) *drawlist.ModelGeometry {
	ox, oy := place.originX, place.originY
	w, h := int32(2*width), int32(2*height)
	if !c.modelScratch.active {
		return fillModelPacket(&drawlist.ModelGeometry{}, nil, polys, w, h, 2*ox, 2*oy, 2*ox, 2*oy, 2, key, drawlist.ModelFallbackNone, &place)
	}
	p := c.borrowPacketScratch()
	count := 0
	for _, f := range polys {
		count += len(f.x)
	}
	p.vertices = resizeScratch(p.vertices, count)
	g := fillModelPacket(&p.g, p.vertices, polys, w, h, 2*ox, 2*oy, 2*ox, 2*oy, 2, key, drawlist.ModelFallbackNone, &place)
	p.faces = g.Faces
	return g
}

// borrowProjectileScratch hands out one standalone model call's slot for the
// frame. Slots never overlap within a frame, so a call's draw record stays
// valid until the next reset.
func (c *Client) borrowProjectileScratch() *presentationrender.ProjectileScratch {
	if !c.modelScratch.active {
		return &presentationrender.ProjectileScratch{}
	}
	s := &c.modelScratch
	if s.projectileNext == len(s.projectiles) {
		s.projectiles = append(s.projectiles, &presentationrender.ProjectileScratch{})
	}
	p := s.projectiles[s.projectileNext]
	s.projectileNext++
	return p
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
