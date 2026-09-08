package gpurender

import (
	"slices"

	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// Execute owns these buffers until all preparation and replay have finished.
// Distinct slots preserve prepared faces while other model jobs are assembled.
type frameScratch[T any] struct {
	slots [][]T
	next  int
}

func (s *frameScratch[T]) take(n int) []T {
	if s.next == len(s.slots) {
		s.slots = append(s.slots, nil)
	}
	v := s.slots[s.next]
	if cap(v) < n {
		v = slices.Grow(v[:0], n)[:n]
	} else {
		v = v[:n]
	}
	s.slots[s.next] = v
	s.next++
	return v
}
func (s *frameScratch[T]) reset() {
	for _, v := range s.slots {
		clear(v)
	}
	s.next = 0
}

// modelPrepScratch retains every frame's preparation buffers, so a steady-state
// frame prepares faces, strips and outline endpoints without allocating
// [DESIGN_GPU_RENDERER.md §11.2 "Allocation policy"].
type modelPrepScratch struct {
	strips   frameScratch[modelGPUFace]
	vertices frameScratch[modelGPUVertex]
	prepared frameScratch[preparedModelFace]
	ears     frameScratch[int]
	indices  frameScratch[uint16]
}

func (s *modelPrepScratch) reset() {
	s.strips.reset()
	s.vertices.reset()
	s.prepared.reset()
	s.ears.reset()
	s.indices.reset()
}

func (r *Renderer) prepareSpanStrips(f drawlist.ModelFace) []modelGPUFace {
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
