package gpurender

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Construction outlines on the device (docs/DESIGN_GPU_RENDERER.md §22).
//
// Retail overdraws a nanoframe's every primitive at its per-row extremes: on
// each row of the edge walk where the right chain's column is past the left's,
// exactly the two pixels at those columns, key-tested once against the pixel
// and stored with their key [03 R-COMP-01 §3][03 R-RAST-01 §1]. The lane used
// to walk those rows on the CPU (prepareOutlineRing) and append two one-pixel
// quads a row, which at a Survival base with twenty factories building was most
// of the lane's vertices, each converted twice by Ebitengine.
//
// A ring of three or four corners is now one device primitive instead: its box
// in native pixels, drawn at 2× about the native origin like the endpoint quads
// were, whose fragment evaluates the ring's edge walk from its key entry in the
// parameter image — the edge setup a mapped quad's key pass reads
// (model_quads.go) — and keeps only the row's two endpoint pixels. The box is
// clipped to the packet's box, which is the clip the CPU walk applied to each
// endpoint. Every texel under a native pixel evaluates that pixel, so all four
// texels of a block agree, and the pixel is key-tested once as before.
//
// The endpoint's key is the CPU walk's: a float32 mix along the edge,
// truncated. The fragment computes the truncated exact rational instead —
// (key_from·dy + (key_to − key_from)·m) / dy in integers, where dy is the
// edge's rows and m the row's offset down it — which no compiler rounding can
// move. The two agree wherever the rational is not a whole number, because the
// float's error is then smaller than the rational's distance from one
// (outlineEdgeExact bounds it); where it is a whole number the float can land
// either side of it, so those rows are checked against the walk's own float
// arithmetic. A ring that fails either test keeps the CPU walk, as does a ring
// of five or more corners, one the entry cannot hold, and every ring once the
// parameter image is full.
//
// A ring's entry holds no placement and no group delta, so a carried child's
// solo image reuses the entries its group composition made: the native origin
// and the delta ride the primitive's vertices.

// modelOutlineRing is one outline ring's share of a packet's append, in ring
// order: drawn on the device from the key entry at entry over the native box
// [x0,x1)×[y0,y1), or the CPU walk's endpoint quads, or — a ring with no
// endpoint to draw — neither.
type modelOutlineRing struct {
	entry          int32
	x0, y0, x1, y1 int32
	color          uint8
	faces          []modelGPUFace
	walked         bool
}

// modelOutlineMaxError bounds, for an edge of dy rows between keys a and b,
// dy·(2|b−a| + max(|a|,|b|)). The walk's float32 key differs from the exact
// rational by at most about (2|b−a| + max(|a|,|b|))·2⁻²⁴, with or without a
// fused multiply-add — one rounding of the quotient, at most one of the
// product and one of the sum — and a rational that is not a whole number is at
// least 1/dy from one; a bound of 2²³ leaves a factor of two between them.
const modelOutlineMaxError = 1 << 23

// planOutline records the append of every outline ring of g, in ring order,
// and returns their range in the lane's ring list.
func (r *Renderer) planOutline(g *drawlist.ModelGeometry) (start, end int32) {
	d := &r.modelDirect
	start = int32(len(d.outline))
	for i := range g.Outline {
		f := &g.Outline[i]
		// A ring of two corners has one edge, walked by both chains, so its
		// right column is never past its left: it draws nothing, as fewer
		// corners do.
		if len(f.Vertices) < 3 {
			continue
		}
		ring, ok := r.describeOutlineRing(g, f)
		if !ok {
			ring = modelOutlineRing{faces: r.prepareOutlineRing(g, f), walked: true}
		}
		d.outline = append(d.outline, ring)
	}
	return start, int32(len(d.outline))
}

// describeOutlineRing gives a ring of three or four corners its key entry and
// box, or reports false when the device cannot draw it exactly.
func (r *Renderer) describeOutlineRing(g *drawlist.ModelGeometry, f *drawlist.ModelFace) (modelOutlineRing, bool) {
	v := f.Vertices
	if len(v) > 4 || r.modelDirect.walkOutlines {
		return modelOutlineRing{}, false
	}
	ring := modelOutlineRing{color: f.Color}
	ring.x0, ring.y0, ring.x1, ring.y1 = v[0].X, v[0].Y, v[0].X, v[0].Y
	for _, c := range v[1:] {
		ring.x0, ring.y0 = min(ring.x0, c.X), min(ring.y0, c.Y)
		ring.x1, ring.y1 = max(ring.x1, c.X), max(ring.y1, c.Y)
	}
	// An endpoint's column is the ceiling of a point on its edge, so it lies
	// between the ring's extreme columns inclusive; its rows are the walk's,
	// the bottom row excluded [03 R-RAST-01 §1].
	ring.x1++
	if g.Width > 0 {
		ring.x0, ring.y0 = max(ring.x0, 0), max(ring.y0, 0)
		ring.x1, ring.y1 = min(ring.x1, g.Width), min(ring.y1, g.Height)
	}
	if ring.x0 >= ring.x1 || ring.y0 >= ring.y1 {
		// Every endpoint falls outside the packet's box.
		return modelOutlineRing{}, true
	}
	quad, ok := outlineQuadRing(v)
	if !ok || !quad.keysExact() {
		return modelOutlineRing{}, false
	}
	entry := r.modelDirect.params.addKeyEntry(&quad)
	if entry == 0 {
		return modelOutlineRing{}, false
	}
	ring.entry = int32(entry)
	return ring, true
}

// outlineQuadRing is a ring of three or four corners as a four-corner ring in
// the native frame plus the local bias, its keys raw. A three-corner ring
// repeats its last corner: the repeated edge has no rows, so both chains walk
// exactly the triangle's edges in the triangle's order, and the extrema are
// the triangle's because the repeat comes after the corner it copies.
func outlineQuadRing(v []drawlist.ModelVertex) (modelQuadRing, bool) {
	var four [4]drawlist.ModelVertex
	copy(four[:], v)
	if len(v) == 3 {
		four[3] = v[2]
	}
	var quad modelQuadRing
	_, ok := quad.rotate(four[:], modelQuadLocalBias, modelQuadLocalBias, 0)
	return quad, ok
}

// keysExact reports whether the device's exact key equals the CPU walk's
// truncated float32 key on every row of every edge the chains walk.
func (q *modelQuadRing) keysExact() bool {
	for e := int32(0); e < 4; e++ {
		from, to := q.edge(e)
		if !outlineEdgeExact(q.key[from]-modelQuadKeyBias, q.key[to]-modelQuadKeyBias, q.y[to]-q.y[from]) {
			return false
		}
	}
	return true
}

// outlineEdgeExact reports whether, on every row m of an edge dy rows tall
// from key a to key b, the walk's key truncated toward zero equals
// truncate(a + (b−a)·m/dy). Outside the rows where that rational is a whole
// number the error bound settles it; on those rows the walk's arithmetic is
// run and compared.
func outlineEdgeExact(a, b, dy int32) bool {
	if dy <= 0 {
		return true
	}
	dk := int64(b) - int64(a)
	span := max(abs64(int64(a)), abs64(int64(b)))
	if int64(dy)*(2*abs64(dk)+span) > modelOutlineMaxError {
		return false
	}
	if dk == 0 {
		return true
	}
	stride := int64(dy) / gcd64(abs64(dk), int64(dy))
	for m := stride; m < int64(dy); m += stride {
		exact := int64(a) + dk*m/int64(dy)
		if exact != 0 && int64(int32(outlineEdgeKey(a, b, int32(m), dy))) != exact {
			return false
		}
	}
	return true
}

// outlineEdgeKey is the CPU walk's key m rows down an edge dy rows tall from
// key a to key b: chainAt's mix, the same expression in the same float32
// operations, so a compiler that fuses one fuses the other.
func outlineEdgeKey(a, b, m, dy int32) float32 {
	q := float32(m) / float32(dy)
	return float32(a) + (float32(b)-float32(a))*q
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func gcd64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// appendOutline appends g's outline rings in ring order, between the cached
// and the live lanes [03 R-REN-03A §4]: one primitive for a ring the device
// draws, the endpoint quads for a ring the CPU walked. (ox, oy) is the atlas
// texel of the native raster's local origin: outline endpoints are native
// pixels drawn at 2× about it, whichever raster the faces used, because retail
// walks the rows of the 1× image after the anti-alias resolve
// [03 R-COMP-01 §3]. A carried child's solo image reuses the rings its group
// composition planned an instant earlier, entries included.
func (r *Renderer) appendOutline(g *drawlist.ModelGeometry, ox, oy float32, entry int) {
	d := &r.modelDirect
	if !d.soloPass || d.soloOutlineFor != g {
		d.soloOutline[0], d.soloOutline[1] = r.planOutline(g)
		d.soloOutlineFor = g
	}
	for i := d.soloOutline[0]; i < d.soloOutline[1]; i++ {
		ring := &d.outline[i]
		switch {
		case ring.entry != 0:
			r.appendOutlineRing(ring, ox, oy, entry)
		case ring.walked:
			r.modelStats.DirectOutlineWalked++
			for _, f := range ring.faces {
				r.appendDirectGPUFace(f, ox, oy, 2, entry)
			}
		}
	}
}

// appendOutlineRing appends one ring's primitive: its box at 2× about (ox, oy),
// unbiased, so it covers exactly the texels of the box's native pixels. The
// colour lanes carry what the entry does not: the key entry's index, the flat
// colour, the group delta and the native origin.
func (r *Renderer) appendOutlineRing(ring *modelOutlineRing, ox, oy float32, entry int) {
	d := &r.modelDirect
	run := d.colourRun([2]*ebiten.Image{r.texturePage(), r.tables.atlas}, 4)
	base := uint32(len(d.verts)) - uint32(run.vOff)
	x0, y0 := ox+2*float32(ring.x0), oy+2*float32(ring.y0)
	x1, y1 := ox+2*float32(ring.x1), oy+2*float32(ring.y1)
	for _, c := range [4][2]float32{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}} {
		d.verts = append(d.verts, ebiten.Vertex{
			DstX: c[0], DstY: c[1],
			ColorR: float32(d.keyDelta), ColorG: float32(ring.color), ColorB: float32(ring.entry), ColorA: ox,
			Custom0: modelDirectOutline | modelDirectLive, Custom1: oy, Custom2: float32(entry),
		})
	}
	d.idx = append(d.idx, base, base+1, base+2, base, base+2, base+3)
	run.vLen += 4
	run.iLen += 6
	r.modelStats.DirectFaces++
	r.modelStats.DirectOutlineRings++
	r.modelStats.DirectOutlineTexels += 4 * int(ring.x1-ring.x0) * int(ring.y1-ring.y0)
}

// modelOutlineSource is the fragment side of an outline ring, shared by the key
// and colour passes after modelQuadMapperSource. modelOutlineKey takes the
// ring's key entry, the fragment's atlas texel d, the native origin g and the
// group delta, and returns whether the texel's native pixel is one of its
// row's two endpoints and, if so, the endpoint's key with the delta applied as
// the vertex lane applies it (modelDirectShiftKey). Every step is integer
// arithmetic: the column is the span writer's fixed-point walk and the key the
// truncated exact rational (see the file comment).
var modelOutlineSource = `
func modelOutlineKey(q float, d vec2, g vec2, delta float) (bool, float) {
	base := (q-1.0)*` + fmt.Sprint(modelQuadTexels) + `.0
	p := floor((d-g)*0.5) + vec2(` + fmt.Sprint(modelQuadLocalBias) + `.0)
	y, mm := modelQuadRows(base)
	row := p.y
	if row < y.x || row >= modelQuadBottom(y, mm) {
		return false, 0.0
	}
	f := modelQuadTexel(base + ` + fmt.Sprint(modelQuadKeyColumnTexel) + `.0)
	h := modelQuadTexel(base + ` + fmt.Sprint(modelQuadKeyColumnTexel+1) + `.0)
	xs := vec4(modelQuadU16(f.r, f.g), modelQuadU16(f.b, f.a), modelQuadU16(h.r, h.g), modelQuadU16(h.b, h.a))
	le, lyf, lyt := modelQuadLeft(y, mm, row)
	re, ryf, ryt := modelQuadRight(y, mm, row)
	lf := le + 1
	if lf == 4 {
		lf = 0
	}
	lx := modelQuadStepX(modelQuadPick(xs, lf), modelQuadStep(modelQuadTexel(base+` + fmt.Sprint(modelQuadKeyStepTexel) + `.0+float(le))), lyf, row)
	rx := modelQuadStepX(modelQuadPick(xs, re), modelQuadStep(modelQuadTexel(base+` + fmt.Sprint(modelQuadKeyStepTexel) + `.0+float(re))), ryf, row)
	if rx <= lx {
		return false, 0.0
	}
	from := 0
	to := 0
	yf := 0.0
	yt := 0.0
	if p.x == lx {
		from = lf
		to = le
		yf = lyf
		yt = lyt
	} else if p.x == rx {
		from = re
		to = re + 1
		yf = ryf
		yt = ryt
	} else {
		return false, 0.0
	}
	a := modelQuadTexel(base + ` + fmt.Sprint(modelQuadKeyCornerTexel) + `.0)
	b := modelQuadTexel(base + ` + fmt.Sprint(modelQuadKeyCornerTexel+1) + `.0)
	k := vec4(modelQuadU16(a.r, a.g), modelQuadU16(a.b, a.a), modelQuadU16(b.r, b.g), modelQuadU16(b.b, b.a)) - vec4(32768.0)
	x := int(modelQuadPick(k, from))
	dk := int(modelQuadPick(k, to)) - x
	dy := int(yt - yf)
	key := (x*dy + dk*int(row-yf)) / dy
	if delta != 0.0 {
		key += int(delta)
		if key < 0 {
			key = 0
		}
		if key > 255 {
			key = 255
		}
	}
	return true, float(key)
}
`
