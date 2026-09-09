package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// frameArena is the per-type preparation store of one frame. Execute owns it
// until all preparation and replay have finished, and every slice it hands out
// stays valid and distinct for the whole frame, so prepared faces survive while
// later model jobs are assembled.
//
// take hands out a subslice of one retained backing array and reset only rewinds
// the offset: the earlier scheme kept one slice per request, so a frame both
// cleared thousands of small slices and converged on their sizes one request at
// a time [DESIGN_GPU_RENDERER.md §11.5 "CPU"].
//
// Growing allocates a fresh backing array and rewinds into it rather than
// copying. Slices handed out before the growth keep pointing at the previous
// array, which is exactly as valid for the rest of the frame; the old array
// becomes garbage once the last of them is dropped. Growth is geometric, so a
// steady-state frame never grows and never allocates.
//
// reset does not clear. Every caller either fills the whole subslice it took or
// takes it with a zero length and appends, so nothing reads a stale element.
// The only references a retained arena holds past a frame are to recorded
// geometry, GAF frames and atlas images that the recorder and the renderer's own
// caches keep alive regardless, so clearing would free nothing and cost the
// memclr the previous scheme paid every frame.
type frameArena[T any] struct {
	buf []T
	off int
}

func (s *frameArena[T]) take(n int) []T {
	if n < 0 {
		n = 0
	}
	if s.off+n > len(s.buf) {
		// The fresh array has to hold what this frame has already handed out as
		// well as this request, or the same frame grows again a few takes later
		// and the arena converges one request at a time instead of at once
		// (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy").
		size := 2 * (s.off + n)
		if size < 2*len(s.buf) {
			size = 2 * len(s.buf)
		}
		s.buf = make([]T, size)
		s.off = 0
	}
	v := s.buf[s.off : s.off+n : s.off+n]
	s.off += n
	return v
}

func (s *frameArena[T]) reset() { s.off = 0 }

// modelPrepScratch retains every frame's preparation buffers, so a steady-state
// frame prepares faces, strips and outline endpoints without allocating
// [DESIGN_GPU_RENDERER.md §11.2 "Allocation policy"].
type modelPrepScratch struct {
	strips   frameArena[modelGPUFace]
	crosses  frameArena[bool]
	vertices frameArena[modelGPUVertex]
	prepared frameArena[preparedModelFace]
	ears     frameArena[int]
	indices  frameArena[uint16]
}

func (s *modelPrepScratch) reset() {
	s.strips.reset()
	s.crosses.reset()
	s.vertices.reset()
	s.prepared.reset()
	s.ears.reset()
	s.indices.reset()
}

func (r *Renderer) prepareSpanStrips(f *drawlist.ModelFace) []modelGPUFace {
	rows := 0
	if len(f.Vertices) > 0 {
		lo, hi := f.Vertices[0].Y, f.Vertices[0].Y
		for _, v := range f.Vertices {
			lo = min(lo, v.Y)
			hi = max(hi, v.Y)
		}
		rows = int(hi - lo)
	}
	return modelSpanStripsInto(f, r.modelPrep.strips.take(rows), r.modelPrep.vertices.take(rows*4))
}

// Ebitengine keeps one temporary vertex buffer per destination image and grows
// it whenever a draw passes more vertices than it has ever held. A model stage
// whose vertex count drifts upward frame by frame therefore reallocates that
// buffer almost every frame [DESIGN_GPU_RENDERER.md §11.5 "CPU"]. Rounding the
// submitted length up to a coarse quantum turns that into a handful of growths
// over a run: padding vertices are never indexed, so they cost only their
// conversion, and the arenas keep the spare capacity the rounding addresses.
const (
	modelVertexPadMin   = 16
	modelVertexPadBlock = 4096
	// modelVertexPadSlack is the largest overshoot padModelVertices can ask for
	// past the end of a list, so an arena that reserves it can always serve the
	// padded slice.
	modelVertexPadSlack = modelVertexPadBlock
)

// modelPadCount rounds a run's vertex count up to the next power of two while it
// is small, then to whole blocks: at most twice the conversion work for a small
// draw, and at most one block of waste for a large one.
func modelPadCount(n int) int {
	if n >= modelVertexPadBlock {
		return ceilTo(n, modelVertexPadBlock)
	}
	p := modelVertexPadMin
	for p < n {
		p *= 2
	}
	return p
}

// reserveModelVertices keeps modelVertexPadSlack spare vertices past the end of
// a built list, so padModelVertices can extend any run's slice without
// reallocating and without disturbing the vertices already written.
func reserveModelVertices(v []ebiten.Vertex) []ebiten.Vertex {
	if cap(v)-len(v) >= modelVertexPadSlack {
		return v
	}
	grown := make([]ebiten.Vertex, len(v), 2*len(v)+modelVertexPadSlack)
	copy(grown, v)
	return grown
}

// padModelVertices returns the run's vertices extended to a padded length. The
// extra vertices hold whatever the arena already held; no index addresses them.
func padModelVertices(v []ebiten.Vertex, first, n int) []ebiten.Vertex {
	end := first + modelPadCount(n)
	if end > cap(v) {
		end = first + n
	}
	return v[first:end]
}
