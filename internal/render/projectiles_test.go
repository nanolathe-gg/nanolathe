package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

type fixedCRTRand struct {
	value int32
	draws int
}

func (r *fixedCRTRand) Rand() int32 {
	r.draws++
	return r.value
}

// [06 R-WFX-01 §4] Secondary geometry is sorted by the strict major-axis
// choice; reversing the inputs preserves both strokes when color2 is present.
func TestBeamStrokes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		a, b      [2]int32
		secondary BeamStroke
	}{
		{"horizontal", [2]int32{10, 20}, [2]int32{30, 20}, BeamStroke{10, 19, 30, 19, 9}},
		{"vertical", [2]int32{10, 20}, [2]int32{10, 40}, BeamStroke{9, 20, 11, 40, 9}},
		{"shallow", [2]int32{10, 20}, [2]int32{30, 30}, BeamStroke{10, 19, 30, 29, 9}},
		{"steep", [2]int32{10, 20}, [2]int32{20, 40}, BeamStroke{9, 20, 21, 40, 9}},
		{"equal spans", [2]int32{10, 20}, [2]int32{30, 40}, BeamStroke{9, 20, 31, 40, 9}},
		{"descending", [2]int32{30, 20}, [2]int32{20, 40}, BeamStroke{29, 20, 21, 40, 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := BeamStroke{tc.a[0], tc.a[1], tc.b[0], tc.b[1], 5}
			for _, pair := range [][2][2]int32{{tc.a, tc.b}, {tc.b, tc.a}} {
				got := BeamStrokes(pair[0], pair[1], 5, 9)
				if len(got) != 2 || got[0] != tc.secondary || got[1] != primary {
					t.Fatalf("strokes = %+v, want secondary %+v then primary %+v", got, tc.secondary, primary)
				}
			}
			got := BeamStrokes(tc.b, tc.a, 5, 0)
			if len(got) != 1 || got[0] != (BeamStroke{tc.b[0], tc.b[1], tc.a[0], tc.a[1], 5}) {
				t.Fatalf("absent secondary changed primary geometry: %+v", got)
			}
		})
	}
}

// TestSegmentedBeam verifies the fixed-count boundary, tail-to-head stepping,
// and CRT draw order for rendertype 7 [06 R-WFX-01 §4].
func TestSegmentedBeam(t *testing.T) {
	head := combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)}
	tail := combat.Vec3{X: numeric.Fixed(10 * 65536), Y: numeric.Fixed(0), Z: numeric.Fixed(0)} // 10 world units raw 655360
	n := SegmentCount(head, tail)
	// 655360 /327680 =2 [03 §5.4]
	if n != 2 {
		t.Fatalf("segment count %d want 2 for span 10 with denominator 0x50000 [03 §5.4]", n)
	}
	// Short span zero => skipped [03 §5.4]
	tail2 := combat.Vec3{X: numeric.Fixed(1 * 65536), Y: numeric.Fixed(0), Z: numeric.Fixed(0)} // 1 unit -> 65536/327680=0
	if SegmentCount(head, tail2) != 0 {
		t.Fatalf("short span should be skipped (zero segments) [03 §5.4]")
	}
	// Jitter draws: each point receives rand()*11/0x8000 -5 in [-5,5] per axis [03 §5.4].
	// Use deterministic CRT seed [01 §7.2] presentation stream (I4)
	crt := rng.NewCRT(1) // deterministic [01 §7.2] (I4)
	for i := 0; i < 10; i++ {
		x, y, z := head.X, head.Y, head.Z
		jx, jy, jz := SegmentedJitter(&crt, x, y, z)
		// delta should be -5..+5 pixels *65536
		dx := int32((int64(jx) - int64(x)) / 65536)
		dy := int32((int64(jy) - int64(y)) / 65536)
		dz := int32((int64(jz) - int64(z)) / 65536)
		if dx < -5 || dx > 5 || dy < -5 || dy > 5 || dz < -5 || dz > 5 {
			t.Fatalf("jitter %d,%d,%d out of [-5,5] range [03 §5.4]", dx, dy, dz)
		}
	}
	// Each pass starts at the tail and generates n jittered endpoints, consuming
	// exactly 3*n CRT draws [06 R-WFX-01 §4].
	head5 := combat.Vec3{X: numeric.Fixed(20 * 65536), Y: numeric.Fixed(0), Z: numeric.Fixed(0)}
	tail5 := combat.Vec3{}
	n5 := SegmentCount(head5, tail5)
	if n5 != 4 {
		t.Fatalf("segment count 20 units want 4 got %d", n5)
	}
	crt2 := rng.NewCRT(12345)
	pts := SegmentedBeamPoints(head5, tail5, &crt2)
	if len(pts) != n5+1 {
		t.Fatalf("segmented points len %d want %d [06 R-WFX-01 §4]", len(pts), n5+1)
	}
	if pts[0] != tail5 {
		t.Fatalf("first point = %+v, want fixed retail tail %+v [06 R-WFX-01 §4]", pts[0], tail5)
	}
	if crt2.Draws() != uint64(3*n5) {
		t.Fatalf("one pass consumed %d CRT draws, want %d [06 R-WFX-01 §4]", crt2.Draws(), 3*n5)
	}
	// Verify deterministic: same seed yields same points.
	crt3 := rng.NewCRT(12345)
	pts2 := SegmentedBeamPoints(head5, tail5, &crt3)
	if len(pts2) != len(pts) {
		t.Fatalf("deterministic len mismatch")
	}
	for i := range pts {
		if pts[i].X != pts2[i].X || pts[i].Y != pts2[i].Y || pts[i].Z != pts2[i].Z {
			t.Fatalf("deterministic jitter mismatch at %d", i)
		}
	}
	if crt3.Draws() != uint64(3*n5) {
		t.Fatalf("second pass consumed %d CRT draws, want %d [06 R-WFX-01 §4]", crt3.Draws(), 3*n5)
	}

	// 0x4000 produces zero jitter: ((0x4000*11)/0x8000)-5 == 0.
	// This exposes the fixed-point step without encoding a random census.
	flat := &fixedCRTRand{value: 0x4000}
	points := SegmentedBeamPoints(head5, tail5, flat)
	for i, want := range []int64{0, 5, 10, 15, 20} {
		if got := points[i].X.Raw(); got != want*65536 {
			t.Fatalf("point %d X = %d, want %d [06 R-WFX-01 §4]", i, got, want*65536)
		}
	}
	if flat.draws != 3*n5 {
		t.Fatalf("fixed source draws = %d, want %d [06 R-WFX-01 §4]", flat.draws, 3*n5)
	}
}
