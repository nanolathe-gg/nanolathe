package gpurender

import (
	"encoding/binary"
	"image"
	"slices"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// A textured quad draws as two device triangles whose fragment shader evaluates
// the span writer's mapping itself, instead of one device quad per source row
// (docs/DESIGN_GPU_RENDERER.md §11.2 "Textured quads without strips"). The
// mapping needs all four corners and all four corner lanes at every fragment,
// which is more than the twelve vertex lanes can carry, so the frame collects
// them in one parameter image and each vertex carries only the quad's index.
//
// The vertex lane that carries it is the flat colour byte: a textured face
// resolves its index from the texture, so that lane is dead on exactly the
// faces a quad index describes.
//
// A quad's entry is two slots of twelve texels, two 16-bit values a texel,
// high byte first. The first is the corner entry the colour pass and the water
// reflection walk (modelQuadLanes): the four corners' positions, texel
// coordinates and key/shade pairs, rotated top corner first, with the bottom
// corner's rotated index in the first pair's spare byte. The second is the key
// entry the key pass reads (modelQuadKey), the span writer's edge setup for
// the key made once here rather than once per fragment [03 R-RAST-01 §1]:
//
//   - texel 0: the top corner's row plus modelQuadRowLimit × the bottom
//     corner's rotated index, then corner 1's row;
//   - texel 1: corner 2's row, corner 3's row;
//   - texels 2 and 3: the four corners' keys;
//   - texels 4 and 5: the four corners' columns;
//   - texels 6 to 9: ring edge e's 16.16 slope, offset by 2³¹, for edge e
//     joining rotated corners e and e+1 as its chain walks it — the right
//     chain owns the edges before the bottom corner (from e to e+1), the left
//     chain the rest (from e+1 to e);
//   - texels 10 and 11: zero.
//
// Only the key pass reads the key entry, because only the key pass reads the
// mapping's key alone. The shader compiler rounds the mapping's lanes by the
// shape of the code around them, and where a shader reads every lane — the
// colour pass — it rounds some of them differently from where it reads one:
// the corner walk is kept there as it is, so every lane stays what it was
// (docs/DESIGN_GPU_RENDERER.md §11.2).
//
// Corner positions are SUBJECT-LOCAL: the raster's own coordinates at the
// atlas scale, plus modelQuadLocalBias, and never the atlas texel the region
// landed on this frame. The fragment shifts its own atlas position into that
// frame by the subject's origin, carried in the subject's verdict entry
// (model_direct.go, subjectVerdicts). That is what lets a retained subject's
// packed parameters be reused at whatever region a later frame gives it
// (docs/DESIGN_GPU_RENDERER.md §22 "Retained packed vertices").
const (
	// One row of the parameter image. The address arithmetic is a divide and a
	// remainder in the shader, so the width is a renderer payload choice, not a
	// power-of-two requirement.
	modelQuadParamWidth = 1024
	// One slot of the parameter image is twelve texels. A subject's verdict
	// entry takes one slot (model_direct.go, subjectVerdicts); a mapped quad
	// takes modelQuadSlots, and its index names the first.
	modelQuadTexels = 12
	modelQuadBytes  = modelQuadTexels * 4
	modelQuadSlots  = 2
	// The key entry's layout within its slot: the corners' keys, their
	// columns, then the edges' slopes.
	modelQuadKeyCornerTexel = 2
	modelQuadKeyColumnTexel = 4
	modelQuadKeyStepTexel   = 6
	// modelQuadRowLimit bounds a packed corner row in the key entry, whose top
	// row shares its sixteen bits with the bottom corner's index. A packed row
	// is modelQuadLocalBias plus a raster row inside a region of at most one
	// atlas page, so it never comes near the limit; a quad that would is not
	// described and interpolates linearly.
	modelQuadRowLimit = 1 << 14
	// The key lane is signed [03 R-REN-03A §2], so it is stored offset-binary.
	// A key outside the resulting window keeps the strip path rather than
	// wrapping into a different subject-local depth.
	modelQuadKeyBias = 32768
	// The image grows in row blocks and is reused, so a steady-state frame
	// neither reallocates it nor allocates its byte buffer.
	modelQuadGrowRows = 64
	// modelQuadLocalBias is added to every packed subject-local corner and to
	// the fragment's shifted position alike, so both stay non-negative: the
	// edge walk's integer division truncates toward zero, which is the floor
	// the span writer's shift takes only for a non-negative operand
	// [03 R-RAST-01 §1]. A corner can sit a little outside its packet's box
	// (the doubled lane's half-pixel offset, an outline ring the box clip
	// dropped), so the bias is wider than any such reach and narrower than
	// the shader's integer headroom.
	modelQuadLocalBias = 8192
)

// modelQuadParams is the frame's quad parameter store: the packed bytes, the
// image they upload into, and how many slots the frame has filled so far.
// Indices are one-based, so zero on a vertex means "not a mapped quad".
type modelQuadParams struct {
	img  *ebiten.Image
	buf  []byte
	rows int
	// count is the number of slots filled this frame.
	count int
}

// reset starts a new frame. The byte buffer and the image are retained.
func (q *modelQuadParams) reset() {
	q.count = 0
	q.buf = q.buf[:0]
}

// add describes one four-corner face and returns the one-based index of its
// first slot, or zero when the face cannot be described and must keep the
// strip path.
//
// dx and dy shift the authored corners into the packed frame — the local bias
// for the model lane, which keeps its corners subject-local (see the file
// comment) — so every packed coordinate is what the fragment shader compares
// its own shifted position against; keyDelta is a group child's delta, applied
// to the corner keys as the vertex lane applies it (modelDirectShiftKey). The
// corners are rotated so that index 0 is the corner holding the minimum Y —
// the first one attaining it, as the extrema pass records — and the bottom
// corner is the first attaining the maximum, which splits the ring into the
// two chains of [03 R-RAST-01 §1]: two index ranges the shader walks without
// searching for the top corner itself.
//
// Each edge's slope in the key entry is the span writer's: the column
// difference shifted up sixteen bits and divided by the row difference,
// truncating toward zero — the integer division modelQuadEdgeX makes at every
// texel. The column span is bounded so that the slope and the fragment's
// running term stay inside a signed 32-bit integer, as they must in the
// walk.
func (q *modelQuadParams) add(v []drawlist.ModelVertex, dx, dy int, keyDelta int32) int {
	if len(v) != 4 || q.count+modelQuadSlots > modelDirectParamCap {
		return 0
	}
	var ring modelQuadRing
	top, ok := ring.rotate(v, int32(dx), int32(dy), keyDelta)
	if !ok {
		return 0
	}
	for j := 0; j < 4; j++ {
		if c := &v[(top+j)&3]; !fitsQuadLane(c.U) || !fitsQuadLane(c.V) {
			return 0
		}
	}
	// The entry is packed straight into its place in the buffer. Every byte of
	// it is written, so a retained copy of the block is the cold append's
	// bytes.
	packed := q.grow(modelQuadSlots)
	for j := 0; j < 4; j++ {
		c := &v[(top+j)&3]
		putQuadLane(packed[4*j:], ring.x[j], ring.y[j])
		putQuadLane(packed[16+4*j:], c.U, c.V)
		binary.BigEndian.PutUint32(packed[32+4*j:], uint32(ring.key[j])<<16|uint32(c.Shade)<<8)
	}
	// The bottom corner's rotated index rides the first corner's spare byte;
	// the shader needs it to split the ring into its left and right chains.
	packed[35] = byte(ring.mm)
	ring.putKeyEntry(packed[modelQuadBytes:])
	return q.commit(modelQuadSlots)
}

// addKeyEntry describes a ring by its key entry alone, one slot, and returns
// the slot's one-based index, or zero when the image is full. Outline rings
// use it (model_outline.go).
func (q *modelQuadParams) addKeyEntry(r *modelQuadRing) int {
	if q.count+1 > modelDirectParamCap {
		return 0
	}
	r.putKeyEntry(q.grow(1))
	return q.commit(1)
}

// modelQuadRing is a four-corner ring rotated so its top corner comes first,
// in the packed frame: columns, rows, and keys offset by modelQuadKeyBias,
// with mm the bottom corner's rotated index.
type modelQuadRing struct {
	x, y, key [4]int32
	mm        int32
}

// rotate fills r with v rotated top corner first and shifted into the packed
// frame, and returns the top corner's index in v; it reports false for a ring
// with no rows or one whose columns, rows or keys the entries cannot hold.
func (r *modelQuadRing) rotate(v []drawlist.ModelVertex, dx, dy, keyDelta int32) (int, bool) {
	top, bot := 0, 0
	for i := 1; i < 4; i++ {
		if v[i].Y < v[top].Y {
			top = i
		}
		if v[i].Y > v[bot].Y {
			bot = i
		}
	}
	// A ring whose corners all share one Y has no row to own; the two-chain
	// walk paints nothing there and neither does this path.
	r.mm = int32((bot - top) & 3)
	if r.mm == 0 {
		return 0, false
	}
	x0, x1 := int32(0xFFFF), int32(0)
	for j := 0; j < 4; j++ {
		c := &v[(top+j)&3]
		r.x[j], r.y[j] = c.X+dx, c.Y+dy
		r.key[j] = modelDirectShiftKey(c.Key, keyDelta) + modelQuadKeyBias
		if !fitsQuadLane(r.x[j]) || !fitsQuadLane(r.y[j]) || !fitsQuadLane(r.key[j]) {
			return 0, false
		}
		x0, x1 = min(x0, r.x[j]), max(x1, r.x[j])
	}
	if r.y[0] >= modelQuadRowLimit || x1-x0 >= 1<<15 {
		return 0, false
	}
	return top, true
}

// edge is ring edge e (joining rotated corners e and e+1) as its chain walks
// it: the right chain owns the edges before the bottom corner, from e to e+1,
// the left chain the rest, from e+1 to e.
func (r *modelQuadRing) edge(e int32) (from, to int32) {
	from, to = e, (e+1)&3
	if e >= r.mm {
		from, to = to, from
	}
	return from, to
}

// putKeyEntry writes the ring's key entry into one slot (the file comment's
// layout). A slope's column difference is under 2¹⁵ (rotate), so the shifted
// difference and the quotient fit a signed 32-bit integer, as they must in
// the walk this replaces.
func (r *modelQuadRing) putKeyEntry(k []byte) {
	putQuadLane(k[0:], r.y[0]+r.mm*modelQuadRowLimit, r.y[1])
	putQuadLane(k[4:], r.y[2], r.y[3])
	putQuadLane(k[4*modelQuadKeyCornerTexel:], r.key[0], r.key[1])
	putQuadLane(k[4*modelQuadKeyCornerTexel+4:], r.key[2], r.key[3])
	putQuadLane(k[4*modelQuadKeyColumnTexel:], r.x[0], r.x[1])
	putQuadLane(k[4*modelQuadKeyColumnTexel+4:], r.x[2], r.x[3])
	for e := int32(0); e < 4; e++ {
		from, to := r.edge(e)
		step := int32(0)
		if d := r.y[to] - r.y[from]; d > 0 {
			step = ((r.x[to] - r.x[from]) << 16) / d
		}
		binary.BigEndian.PutUint32(k[4*(modelQuadKeyStepTexel+e):], uint32(step)^1<<31)
	}
	clear(k[4*(modelQuadKeyStepTexel+4) : modelQuadBytes])
}

// grow makes room for n slots after the filled ones and returns them. A face
// that turns out not to fit leaves count where it was, so the bytes are never
// read.
func (q *modelQuadParams) grow(n int) []byte {
	need := (q.count + n) * modelQuadBytes
	if cap(q.buf) < need {
		q.buf = slices.Grow(q.buf, need-len(q.buf))
	}
	return q.buf[q.count*modelQuadBytes : need]
}

// commit keeps the n slots grow handed out and returns the first one's
// one-based index.
func (q *modelQuadParams) commit(n int) int {
	q.buf = q.buf[:(q.count+n)*modelQuadBytes]
	first := q.count + 1
	q.count += n
	return first
}

func fitsQuadLane[T int | int32](v T) bool { return v >= 0 && v <= 0xFFFF }

// putQuadLane stores two 16-bit values in one RGBA8 texel, high byte first.
// The image is only ever sampled by the shader, so the alpha byte is data:
// WritePixels keeps a value that is invalid as a premultiplied colour.
func putQuadLane[T int | int32](b []byte, hi, lo T) {
	binary.BigEndian.PutUint32(b, uint32(uint16(hi))<<16|uint32(uint16(lo)))
}

// appendBlock appends a retained run of n packed slots — one subject's
// mapped faces, captured from an earlier frame in the subject-local frame —
// and returns the one-based index of its first slot, or zero when the image
// cannot hold the whole block. A block is all or nothing so the retained
// vertices' local indices stay valid.
func (q *modelQuadParams) appendBlock(block []byte, n int) int {
	if n == 0 || q.count+n > modelDirectParamCap {
		return 0
	}
	need := (q.count + n) * modelQuadBytes
	if cap(q.buf) < need {
		q.buf = slices.Grow(q.buf, need-len(q.buf))
	}
	q.buf = q.buf[:need]
	copy(q.buf[q.count*modelQuadBytes:], block[:n*modelQuadBytes])
	first := q.count + 1
	q.count += n
	return first
}

// upload publishes the frame's entries. It is called once a frame, before the
// page draws.
func (q *modelQuadParams) upload() {
	if q.count == 0 {
		return
	}
	texels := q.count * modelQuadTexels
	rows := (texels + modelQuadParamWidth - 1) / modelQuadParamWidth
	need := rows * modelQuadParamWidth * 4
	if cap(q.buf) < need {
		q.buf = slices.Grow(q.buf, need-len(q.buf))
	}
	// The pad to a whole row keeps stale bytes from the previous frame; no
	// quad addresses them, and zeroing them would cost a frame-sized clear.
	q.buf = q.buf[:need]
	if q.img == nil || q.rows < rows {
		if q.img != nil {
			q.img.Deallocate()
		}
		q.rows = ceilTo(rows, modelQuadGrowRows)
		q.img = ebiten.NewImage(modelQuadParamWidth, q.rows)
	}
	// The sub-image lives only for this upload, so it comes from the recyclable
	// pool instead of the image's own sub-image cache, which would otherwise
	// retain one entry per distinct row count the frames ask for
	// (docs/DESIGN_GPU_RENDERER.md §11.5 "CPU").
	sub := q.img.RecyclableSubImage(image.Rect(0, 0, modelQuadParamWidth, rows))
	sub.WritePixels(q.buf[:need])
	sub.Recycle()
}

// ceilTo rounds v up to a multiple of a.
func ceilTo(v, a int) int { return (v + a - 1) / a * a }
