package gpurender

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
// reset only rewinds. The preparation owner clears its pointer-bearing records
// after Execute has consumed them; numeric backing arrays stay warm. Growth must
// not clear the previous backing array: earlier subjects still read it this frame.
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
// modelPrepScratch retains the outline walk's buffers, so a steady-state frame
// prepares its endpoints without allocating [DESIGN_GPU_RENDERER.md §11.2
// "Allocation policy"].
type modelPrepScratch struct {
	strips   frameArena[modelGPUFace]
	vertices frameArena[modelGPUVertex]
}

func (s *modelPrepScratch) reset() {
	clear(s.strips.buf[:s.strips.off])
	s.strips.reset()
	s.vertices.reset()
}
