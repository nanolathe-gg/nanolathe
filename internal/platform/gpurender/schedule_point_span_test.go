package gpurender

import (
	"math/rand"
	"testing"
)

// Span placement (placePointSpan) has to agree with the per-pixel definition it
// replaced (placePoint): the phase a run takes is the maximum of the phases its
// pixels would take one at a time, and the tag state it leaves is the state
// placing them one at a time at that phase leaves. This holds because a run's x
// values are consecutive and distinct, so no pixel of a run can depend on another
// pixel of the same run (docs/DESIGN_GPU_RENDERER.md §11.2 "The scheduler").
//
// The check runs two schedulers over the same command stream — one placing pixel
// by pixel, one run by run — with repeated pixels, overlapping runs and rectangle
// owners in the mix, and compares run by run.
func TestPlacePointSpanMatchesPerPixelMaximum(t *testing.T) {
	const w, h = 320, 200
	var span, pixel scheduler
	span.resetFrame(w, h)
	pixel.resetFrame(w, h)

	rng := rand.New(rand.NewSource(5))
	for step := 0; step < 4000; step++ {
		switch {
		case step%37 == 0:
			// A rectangle command of either class, so the runs meet owners,
			// saturated cells and the forgotten-owner floor.
			x0, y0 := rng.Intn(w), rng.Intn(h)
			x1, y1 := x0+1+rng.Intn(48), y0+1+rng.Intn(32)
			dest := rng.Intn(2) == 0
			for _, s := range []*scheduler{&span, &pixel} {
				p := s.place(x0, y0, x1, y1)
				s.tag(x0, y0, x1, y1, p, dest)
			}
		case step%211 == 0:
			span.resetSegment()
			pixel.resetSegment()
		default:
			y := rng.Intn(h)
			x0 := rng.Intn(w)
			n := 1 + rng.Intn(24)
			if x0+n > w {
				n = w - x0
			}
			got := span.placePointSpan(x0, x0+n, y)
			want := int32(0)
			for x := x0; x < x0+n; x++ {
				if p := pixel.placePoint(x, y); p > want {
					want = p
				}
			}
			// The per-pixel run stamped each pixel with its own answer; re-stamp
			// it with the maximum so the two schedulers stay comparable, which is
			// exactly the state span placement leaves.
			for x := x0; x < x0+n; x++ {
				pixel.restampPointForTest(x, y, want)
			}
			if got != want {
				t.Fatalf("step %d: run (%d..%d, %d) placed at phase %d, per-pixel maximum %d",
					step, x0, x0+n, y, got, want)
			}
			for x := x0; x < x0+n; x++ {
				if a, b := span.pointPhaseForTest(x, y), pixel.pointPhaseForTest(x, y); a != b {
					t.Fatalf("step %d: pixel (%d,%d) stamped %d by the span, %d per pixel", step, x, y, a, b)
				}
			}
			if a, b := span.cellPointForTest(x0, y), pixel.cellPointForTest(x0, y); a != b {
				t.Fatalf("step %d: cell of (%d,%d) holds point phase %d, per pixel %d", step, x0, y, a, b)
			}
		}
	}
}

// A run may never be placed before a command it must observe: the span answer is
// at least every per-pixel answer. The maximum test above proves equality, so this
// only guards the direction that would corrupt a frame if the maximum were ever
// replaced by a cheaper approximation.
func TestPlacePointSpanNeverPlacesEarlyOverAnOwner(t *testing.T) {
	const w, h = 128, 64
	var s scheduler
	s.resetFrame(w, h)
	// A destination-reading owner over the right half: any run touching it must
	// land one phase after it, whichever of its pixels does the touching.
	s.tag(64, 0, 128, 64, 3, true)
	if got := s.placePointSpan(60, 70, 10); got != 4 {
		t.Fatalf("run crossing a phase-3 destination owner placed at %d, want 4", got)
	}
	if got := s.placePointSpan(0, 8, 10); got != 0 {
		t.Fatalf("run clear of every owner placed at %d, want 0", got)
	}
	if got := s.placePointSpan(4, 12, 10); got != 1 {
		t.Fatalf("run repeating pixels of a phase-0 run placed at %d, want 1", got)
	}
}

// The three helpers below read the scheduler's tag state the way placePointSpan
// writes it, so a test can compare two schedulers without exporting the layout.
func (s *scheduler) pointPhaseForTest(x, y int) int32 {
	cx, cy, _, _, ok := s.cellRange(x, y, x+1, y+1)
	if !ok {
		return -1
	}
	at := cy*s.cols + cx
	v := s.pointPhase[at<<schedPointCellShift|(y&schedCellMask)<<schedGridShift|x&schedCellMask]
	if uint32(v>>32) != s.serial {
		return -1
	}
	return int32(uint32(v))
}

func (s *scheduler) cellPointForTest(x, y int) int32 {
	cx, cy, _, _, ok := s.cellRange(x, y, x+1, y+1)
	if !ok {
		return -1
	}
	at := cy*s.cols + cx
	if s.tags[at] != s.serial {
		return -1
	}
	return s.cellPoint[at]
}

func (s *scheduler) restampPointForTest(x, y int, phase int32) {
	cx, cy, _, _, ok := s.cellRange(x, y, x+1, y+1)
	if !ok {
		return
	}
	at := cy*s.cols + cx
	s.pointPhase[at<<schedPointCellShift|(y&schedCellMask)<<schedGridShift|x&schedCellMask] =
		uint64(s.serial)<<32 | uint64(uint32(phase))
	if phase > s.cellPoint[at] {
		s.cellPoint[at] = phase
	}
}
