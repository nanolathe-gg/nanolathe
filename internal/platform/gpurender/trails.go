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
func (r *Renderer) Trails(t drawlist.Trails) {
	if r == nil || r.sceneDest == nil || r.tables.atlas == nil || len(t.Marks) == 0 {
		return
	}
	// Corner order matches the scheduler's quad: (−1,−1) (1,−1) (−1,1) (1,1)
	// in (along, across) local coordinates, which the custom lanes carry to
	// the shader.
	type corners struct {
		xs, ys [4]float32
	}
	quads := make([]corners, 0, len(t.Marks))
	strengths := make([]uint8, 0, len(t.Marks))
	shapes := make([]float32, 0, len(t.Marks))
	minX, minY := float32(r.clipW()), float32(r.clipH())
	maxX, maxY := float32(0), float32(0)
	for _, m := range t.Marks {
		if m.Strength == 0 {
			continue
		}
		ax, ay := float32(m.AxisX)/256, float32(m.AxisY)/256
		px, py := float32(m.CrossX)/256, float32(m.CrossY)/256
		if m.AxisX == 0 && m.AxisY == 0 || m.CrossX == 0 && m.CrossY == 0 {
			continue
		}
		cx, cy := float32(m.X), float32(m.Y)
		q := corners{
			xs: [4]float32{cx - ax - px, cx + ax - px, cx - ax + px, cx + ax + px},
			ys: [4]float32{cy - ay - py, cy + ay - py, cy - ay + py, cy + ay + py},
		}
		for i := 0; i < 4; i++ {
			minX, maxX = min(minX, q.xs[i]), max(maxX, q.xs[i])
			minY, maxY = min(minY, q.ys[i]), max(maxY, q.ys[i])
		}
		quads = append(quads, q)
		strengths = append(strengths, m.Strength)
		shape := float32(0)
		if m.Shape == drawlist.TrailTrack {
			shape = 1
		}
		shapes = append(shapes, shape)
	}
	if len(quads) == 0 {
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
	for i, q := range quads {
		k := 1 - float32(strengths[i])/255
		low, high := rowScaleLanes(k)
		col := [4]float32{low, high, 0, 0}
		shape := shapes[i]
		r.sched.quadCorners(schedDest, q.xs, q.ys, col, [4][4]float32{
			{-1, -1, shape, destOpTrail},
			{1, -1, shape, destOpTrail},
			{-1, 1, shape, destOpTrail},
			{1, 1, shape, destOpTrail},
		})
	}
}
