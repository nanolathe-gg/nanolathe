package gpurender

import (
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
const (
	// One row of the parameter image. The address arithmetic is a divide and a
	// remainder in the shader, so the width is a renderer payload choice, not a
	// power-of-two requirement.
	modelQuadParamWidth = 1024
	// Twelve texels per quad: four corner positions, four corner texel
	// coordinates, then four corner key/shade pairs.
	modelQuadTexels = 12
	modelQuadBytes  = modelQuadTexels * 4
	// The key lane is signed [03 R-REN-03A §2], so it is stored offset-binary.
	// A key outside the resulting window keeps the strip path rather than
	// wrapping into a different subject-local depth.
	modelQuadKeyBias = 32768
	// The image grows in row blocks and is reused, so a steady-state frame
	// neither reallocates it nor allocates its byte buffer.
	modelQuadGrowRows = 64
)

// modelQuadParams is the frame's quad parameter store: the packed bytes, the
// image they upload into, and how many quads the frame has described so far.
// Indices are one-based, so zero on a vertex means "not a mapped quad".
type modelQuadParams struct {
	img  *ebiten.Image
	buf  []byte
	rows int
	// count is the number of quads described this frame; uploaded is how many
	// of them the parameter image already holds, so the per-subject fallback
	// route can add more quads after the shared pages have been drawn.
	count    int
	uploaded int
}

// reset starts a new frame. The byte buffer and the image are retained.
func (q *modelQuadParams) reset() {
	q.count, q.uploaded = 0, 0
	q.buf = q.buf[:0]
}

// add describes one four-corner face and returns its one-based index, or zero
// when the face cannot be described and must keep the strip path.
//
// dx and dy shift the authored corners onto the subject's slot, so every packed
// coordinate is the page pixel the fragment shader compares against. The
// corners are rotated so that index 0 is the corner holding the minimum Y — the
// first one attaining it, as the extrema pass records — which turns the two
// chains of [03 R-RAST-01 §1] into two index ranges the shader can walk without
// searching for the top corner itself.
func (q *modelQuadParams) add(v []drawlist.ModelVertex, dx, dy int) int {
	if len(v) != 4 {
		return 0
	}
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
	mm := (bot - top + 4) % 4
	if mm == 0 {
		return 0
	}
	var packed [modelQuadBytes]byte
	for j := 0; j < 4; j++ {
		c := v[(top+j)%4]
		x, y := int(c.X)+dx, int(c.Y)+dy
		key := int(c.Key) + modelQuadKeyBias
		if !fitsQuadLane(x) || !fitsQuadLane(y) || !fitsQuadLane(int(c.U)) || !fitsQuadLane(int(c.V)) || !fitsQuadLane(key) {
			return 0
		}
		putQuadLane(packed[4*j:], x, y)
		putQuadLane(packed[16+4*j:], int(c.U), int(c.V))
		putQuadLane(packed[32+4*j:], key, 0)
		packed[32+4*j+2] = c.Shade
	}
	// The bottom corner's rotated index rides the first corner's spare byte;
	// the shader needs it to split the ring into its left and right chains.
	packed[35] = byte(mm)
	need := (q.count + 1) * modelQuadBytes
	if cap(q.buf) < need {
		q.buf = slices.Grow(q.buf, need-len(q.buf))
	}
	q.buf = q.buf[:need]
	copy(q.buf[q.count*modelQuadBytes:], packed[:])
	q.count++
	return q.count
}

func fitsQuadLane(v int) bool { return v >= 0 && v <= 0xFFFF }

// putQuadLane stores two 16-bit values in one RGBA8 texel, high byte first.
// The image is only ever sampled by the shader, so the alpha byte is data:
// WritePixels keeps a value that is invalid as a premultiplied colour.
func putQuadLane(b []byte, hi, lo int) {
	b[0], b[1] = byte(hi>>8), byte(hi)
	b[2], b[3] = byte(lo>>8), byte(lo)
}

// upload publishes every quad added since the last upload. It is called before
// each set of page draws, so the per-subject fallback route's late additions
// reach the device too.
func (q *modelQuadParams) upload() {
	if q.count == q.uploaded {
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
	q.uploaded = q.count
}
