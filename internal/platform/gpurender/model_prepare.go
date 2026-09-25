package gpurender

import "github.com/nanolathe-gg/nanolathe/internal/drawlist"

// Outline preparation for the model lane: the CPU row walk that turns a
// nanoframe outline ring into its per-row endpoint quads, with the span
// writer's own edge arithmetic (docs/DESIGN_GPU_RENDERER.md §22)
// [03 R-COMP-01 §3][03 R-RAST-01 §1]. The lane draws most rings on the device
// from their edge setup instead (model_outline.go); this walk draws the rest.

// modelGPUVertex is one prepared endpoint corner: its position and key,
// carried as fractions until the fragment narrows them.
type modelGPUVertex struct {
	X, Y float32
	Key  float32
}

// modelGPUFace is one endpoint quad in the outline's flat colour.
type modelGPUFace struct {
	Vertices []modelGPUVertex
	Color    uint8
}

// prepareOutlineRing walks one ring's rows: on every row where the right
// chain's column is past the left's, the two columns are the row's endpoints,
// each one native pixel kept inside the packet's box.
func (r *Renderer) prepareOutlineRing(g *drawlist.ModelGeometry, f *drawlist.ModelFace) []modelGPUFace {
	v := f.Vertices
	if len(v) < 2 {
		return nil
	}
	top, bottom := 0, 0
	for j := range v {
		if v[j].Y < v[top].Y {
			top = j
		}
		if v[j].Y > v[bottom].Y {
			bottom = j
		}
	}
	rows := int(v[bottom].Y - v[top].Y)
	if rows == 0 {
		return nil
	}
	out := r.modelPrep.strips.take(2 * rows)[:0]
	corners := r.modelPrep.vertices.take(8 * rows)
	used := 0
	for y := v[top].Y; y < v[bottom].Y; y++ {
		left, a := chainAt(v, top, bottom, -1, y)
		right, b := chainAt(v, top, bottom, 1, y)
		if !a || !b || right.X <= left.X {
			continue
		}
		for _, p := range [2]modelGPUVertex{left, right} {
			// Outlines also visit rings dropped by the body material dispatch;
			// retain the classic composition-box clip for those endpoints.
			if g.Width > 0 && (p.X < 0 || p.X >= float32(g.Width) || p.Y < 0 || p.Y >= float32(g.Height)) {
				continue
			}
			q, s, t := p, p, p
			q.X++
			s.X++
			s.Y++
			t.Y++
			quad := corners[used : used+4 : used+4]
			quad[0], quad[1], quad[2], quad[3] = p, q, s, t
			used += 4
			out = append(out, modelGPUFace{Vertices: quad, Color: f.Color})
		}
	}
	return out
}

func chainAt(v []drawlist.ModelVertex, top, bot, step int, y int32) (modelGPUVertex, bool) {
	n := len(v)
	cur := top
	var candidate modelGPUVertex
	found := false
	for k := 0; k <= n; k++ {
		next := cur + step
		if next < 0 {
			next = n - 1
		}
		if next >= n {
			next = 0
		}
		a, b := v[cur], v[next]
		// The scan writer owns rows in the half-open edge interval. Admitting
		// the lower endpoint carries an edge into a row whose CPU polygon never
		// writes, which is visible at a folded junction [03 R-RAST-01 §1].
		if b.Y > a.Y && y >= a.Y && y < b.Y {
			d := float32(b.Y - a.Y)
			q := float32(y-a.Y) / d
			mix := func(x, z float32) float32 { return x + (z-x)*q }
			candidate = modelGPUVertex{
				X: float32(biasedEdgeX(a, b, y)), Y: float32(y),
				Key: mix(float32(a.Key), float32(b.Key)),
			}
			found = true
		}
		if next == bot {
			break
		}
		cur = next
	}
	return candidate, found
}

// biasedEdgeX reproduces the span writer's fixed-point edge setup. The
// numerator slope is truncated once in signed 16.16, while the +65535 bias
// makes the row coordinate the authored ceiling after the arithmetic shift.
func biasedEdgeX(a, b drawlist.ModelVertex, y int32) int32 {
	dy := int64(b.Y - a.Y)
	if dy <= 0 {
		return a.X
	}
	xStart := (int64(a.X) << 16) + 65535
	xStep := (int64(b.X-a.X) << 16) / dy
	return int32((xStart + xStep*int64(y-a.Y)) >> 16)
}
