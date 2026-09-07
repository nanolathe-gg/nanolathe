package gpurender

// quadBatchVertexLimit is the largest vertex slice addressable by this
// renderer's uint16 index scratch: indices 0 through 65535 are valid.  Keep
// every quad batch at or below it; converting a larger vertex offset would
// silently wrap and draw earlier geometry instead.  This is an executor bound,
// not a retail rendering limit [docs/DESIGN_GPU_RENDERER.md C-G3].
const (
	quadVertices         = 4
	quadBatchVertexLimit = 1 << 16
)

// quadBatchHasRoom reports whether another four-vertex quad can be appended
// without exceeding the uint16 index domain. Callers flush their own pipeline
// state before appending when it returns false.
func (r *Renderer) quadBatchHasRoom() bool {
	return len(r.verts) <= quadBatchVertexLimit-quadVertices
}

func (r *Renderer) resetGeometry() {
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
}
