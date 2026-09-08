package gpurender

import (
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"slices"
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

type modelPrepScratch struct {
	active   bool
	strips   frameScratch[modelGPUFace]
	vertices frameScratch[modelGPUVertex]
	prepared frameScratch[preparedModelFace]
	keys     map[*drawlist.ModelGeometry]string
}

func (s *modelPrepScratch) reset() {
	s.strips.reset()
	s.vertices.reset()
	s.prepared.reset()
	clear(s.keys)
}
func (r *Renderer) frameModelKey(g *drawlist.ModelGeometry) string {
	s := &r.modelPrep
	if !s.active {
		return r.modelCache.identity(g)
	}
	if key, ok := s.keys[g]; ok {
		return key
	}
	c := &r.modelCache
	c.keyScratch = c.keyScratch[:0]
	c.appendGeometry(g)
	var key string
	// A temporary byte-to-string map lookup avoids allocating a duplicate key on hits.
	if el := c.entries[string(c.keyScratch)]; el != nil {
		key = el.Value.(*modelCacheEntry).key
	} else {
		key = string(c.keyScratch)
	}
	if s.keys == nil {
		s.keys = make(map[*drawlist.ModelGeometry]string)
	}
	s.keys[g] = key
	return key
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
