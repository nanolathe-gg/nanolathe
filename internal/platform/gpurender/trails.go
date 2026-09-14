package gpurender

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Trails draws one frame's Enhanced ground marks (docs/DESIGN_GPU_RENDERER.md
// §15). Each mark is a rotated quad through the destination pass whose
// fragment scales the terrain colour by 1 − strength × coverage, the coverage
// a soft oval or a soft-sided segment the shader evaluates from the quad's
// local coordinates (destOpTrail). The blend is the row families' scale
// blend, so the whole batch is one destination command over the union of
// its marks: multiplies commute, so marks that overlap need no ordering
// between them, and the scheduler still places the batch after the terrain
// it darkens and before the features and units drawn over it.
//
// The scheduler needs the batch's screen rectangle before any quad is
// appended, so the marks are visited twice: once for the bounds and once to
// emit. Neither pass keeps a list — a mark's four corners are eight adds over
// values already in the record, which is cheaper than the scratch that would
// carry them between the passes and leaves the family allocating nothing in
// steady state (§11.2 "Allocation policy").
func (r *Renderer) Trails(t drawlist.Trails) {
	if r == nil || r.sceneDest == nil || r.tables.atlas == nil || len(t.Marks) == 0 {
		return
	}
	minX, minY := float32(r.clipW()), float32(r.clipH())
	maxX, maxY := float32(0), float32(0)
	any := false
	for i := range t.Marks {
		xs, ys, ok := trailQuad(t.Marks[i])
		if !ok {
			continue
		}
		for j := 0; j < 4; j++ {
			minX, maxX = min(minX, xs[j]), max(maxX, xs[j])
			minY, maxY = min(minY, ys[j]), max(maxY, ys[j])
		}
		any = true
	}
	if !any {
		return
	}
	x0, y0 := maxInt(int(math.Floor(float64(minX))), 0), maxInt(int(math.Floor(float64(minY))), 0)
	x1, y1 := minInt(int(math.Ceil(float64(maxX))), r.clipW()), minInt(int(math.Ceil(float64(maxY))), r.clipH())
	if x0 >= x1 || y0 >= y1 {
		return
	}
	if !r.sched.beginBlended(schedDest, x0, y0, x1, y1,
		[4]*ebiten.Image{1: r.tables.atlas}, nil, blendScaleDestination, schedReadNone) {
		return
	}
	for i := range t.Marks {
		xs, ys, ok := trailQuad(t.Marks[i])
		if !ok {
			continue
		}
		k := 1 - float32(t.Marks[i].Strength)/255
		low, high := rowScaleLanes(k)
		col := [4]float32{low, high, 0, 0}
		shape := float32(0)
		if t.Marks[i].Shape == drawlist.TrailTrack {
			shape = 1
		}
		r.sched.quadCorners(schedDest, xs, ys, col, [4][4]float32{
			{-1, -1, shape, destOpTrail},
			{1, -1, shape, destOpTrail},
			{-1, 1, shape, destOpTrail},
			{1, 1, shape, destOpTrail},
		})
	}
}

// trailQuad returns one mark's four corners, or ok=false for a mark that
// contributes nothing: a faded-out strength, or an axis or cross vector of zero
// length, whose quad would be degenerate. The corner order matches the
// scheduler's quad — (−1,−1) (1,−1) (−1,1) (1,1) in (along, across) local
// coordinates — which the custom lanes carry to the shader.
func trailQuad(m drawlist.Trail) (xs, ys [4]float32, ok bool) {
	if m.Strength == 0 {
		return xs, ys, false
	}
	if m.AxisX == 0 && m.AxisY == 0 || m.CrossX == 0 && m.CrossY == 0 {
		return xs, ys, false
	}
	ax, ay := float32(m.AxisX)/256, float32(m.AxisY)/256
	px, py := float32(m.CrossX)/256, float32(m.CrossY)/256
	cx, cy := float32(m.X), float32(m.Y)
	xs = [4]float32{cx - ax - px, cx + ax - px, cx - ax + px, cx + ax + px}
	ys = [4]float32{cy - ay - py, cy + ay - py, cy - ay + py, cy + ay + py}
	return xs, ys, true
}
