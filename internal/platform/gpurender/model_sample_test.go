package gpurender

import (
	"testing"
)

// The body raster's corners are biased so that the device's centre-sampled
// coverage is the span writer's: a texel (col, row) is covered exactly when
// ceil(xL(row)) <= col < ceil(xR(row)) and the row lies in [yTop, yBottom)
// [03 R-RAST-01 §1]. A face therefore never covers the row below its bottom
// edge or the column right of its right edge; the lane once pushed its far
// corners a whole texel out and drew a flat line there.
func TestModelSampleBiasMatchesSpanWriterCoverage(t *testing.T) {
	type ring [][2]int32
	cases := []struct {
		name  string
		ring  ring
		scale float32
	}{
		// CORMEX's front base wall in the doubled lane: a horizontal bottom
		// edge whose extra row was the reported light line.
		{"cormex base wall", ring{{68, 160}, {160, 160}, {168, 200}, {60, 200}}, 1},
		{"axis-aligned quad", ring{{3, 4}, {11, 4}, {11, 9}, {3, 9}}, 1},
		{"sloped triangle", ring{{2, 1}, {17, 6}, {5, 13}}, 1},
		{"sloped quad", ring{{10, 0}, {21, 7}, {12, 19}, {1, 8}}, 1},
		// A native packet doubled into the atlas: the corners are scaled and
		// the bias stays half a texel of the raster drawn into.
		{"native quad doubled", ring{{3, 2}, {9, 3}, {8, 7}, {2, 6}}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scaled := make([][2]int32, len(tc.ring))
			device := make([][2]float64, len(tc.ring))
			for i, v := range tc.ring {
				scaled[i] = [2]int32{int32(float32(v[0]) * tc.scale), int32(float32(v[1]) * tc.scale)}
				device[i] = [2]float64{float64(modelSamplePos(v[0], tc.scale)), float64(modelSamplePos(v[1], tc.scale))}
			}
			want := spanWriterCoverage(scaled)
			got := deviceFanCoverage(device)
			if len(want) == 0 {
				t.Fatal("reference covers nothing")
			}
			for p := range want {
				if !got[p] {
					t.Errorf("texel %v: span writer covers it, the device does not", p)
				}
			}
			for p := range got {
				if !want[p] {
					t.Errorf("texel %v: device covers it outside the span writer's rows and columns", p)
				}
			}
		})
	}
}

// spanWriterCoverage is the two-chain walk of [03 R-RAST-01 §1] for a
// clockwise (screen, Y down) convex ring with exact edge positions: rows are
// top inclusive and bottom exclusive, and a row covers the columns
// ceil(xL) <= c < ceil(xR). The retail 16.16 slope truncation is left out;
// the rings here are chosen so it does not move an edge texel.
func spanWriterCoverage(ring [][2]int32) map[[2]int32]bool {
	n := len(ring)
	top, bottom := 0, 0
	for i, v := range ring {
		if v[1] < ring[top][1] {
			top = i
		}
		if v[1] > ring[bottom][1] {
			bottom = i
		}
	}
	chain := func(step int) map[int32]int32 {
		out := map[int32]int32{}
		for cur := top; cur != bottom; {
			next := (cur + step + n) % n
			a, b := ring[cur], ring[next]
			if dy := b[1] - a[1]; dy > 0 {
				for r := a[1]; r < b[1]; r++ {
					num := int64(a[0])*int64(dy) + int64(b[0]-a[0])*int64(r-a[1])
					out[r] = int32(ceilDiv(num, int64(dy)))
				}
			}
			cur = next
		}
		return out
	}
	left, right := chain(-1), chain(1)
	cover := map[[2]int32]bool{}
	for r := ring[top][1]; r < ring[bottom][1]; r++ {
		for c := left[r]; c < right[r]; c++ {
			cover[[2]int32{c, r}] = true
		}
	}
	return cover
}

func ceilDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) == (b < 0) {
		q++
	}
	return q
}

// deviceFanCoverage is a graphics device's rasterization of the lane's fan
// triangulation: a texel is covered when its centre is inside a triangle, a
// centre on an edge counting only for a top or left edge.
func deviceFanCoverage(ring [][2]float64) map[[2]int32]bool {
	cover := map[[2]int32]bool{}
	for i := 1; i+1 < len(ring); i++ {
		tri := [3][2]float64{ring[0], ring[i], ring[i+1]}
		minX, minY, maxX, maxY := tri[0][0], tri[0][1], tri[0][0], tri[0][1]
		for _, v := range tri[1:] {
			minX, minY = min(minX, v[0]), min(minY, v[1])
			maxX, maxY = max(maxX, v[0]), max(maxY, v[1])
		}
		for r := int32(minY) - 1; float64(r) <= maxY; r++ {
			for c := int32(minX) - 1; float64(c) <= maxX; c++ {
				if centreInside(tri, float64(c)+0.5, float64(r)+0.5) {
					cover[[2]int32{c, r}] = true
				}
			}
		}
	}
	return cover
}

// centreInside tests a point against a clockwise (screen) triangle with the
// top-left fill rule: a top edge runs rightwards along a horizontal, a left
// edge runs upwards.
func centreInside(tri [3][2]float64, x, y float64) bool {
	for i := range 3 {
		a, b := tri[i], tri[(i+1)%3]
		e := (b[0]-a[0])*(y-a[1]) - (b[1]-a[1])*(x-a[0])
		if e < 0 {
			return false
		}
		if e == 0 {
			top := a[1] == b[1] && b[0] > a[0]
			left := b[1] < a[1]
			if !top && !left {
				return false
			}
		}
	}
	return true
}
