package gpurender

import (
	"image"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// Geometry preparation for the model slot atlas: face triangulation, the
// two-chain strip mapper for folded rings and textured quads, and outline
// endpoints (docs/DESIGN_GPU_RENDERER.md §5.1, §11.2). Split from models.go so
// the scene-commit unit and the quad-preparation unit own separate files.

// modelGPUVertex is private preparation data for the device rasterizer. The
// public packet deliberately uses integer authored corners, but a folded ring
// needs its edge attributes carried as fractions until the fragment shader
// performs the retail byte/key narrowing [03 R-RAST-01 §1].
type modelGPUVertex struct {
	X, Y  float32
	Key   float32
	U, V  float32
	Shade float32
}

type modelGPUFace struct {
	Vertices []modelGPUVertex
	Texture  *formats.GAFFrame
	Color    uint8
	Shaded   bool
}

// ModelStats is the per-Execute accounting for modern model execution.

// modelFaceTriangles retains counter-clockwise and degenerate rings as culled
// input. Visible rings are triangulated by ears in their authored clockwise
// order. This is a conventional approximation for the GPU prototype; a crossed
// ring is rejected rather than being fan-filled into invented coverage.
func modelFaceTriangles(f drawlist.ModelFace) ([]uint16, bool, bool) {
	return modelFaceTrianglesInto(&f, nil, polygonCrosses(f.Vertices))
}

// modelFaceTrianglesInto is the same triangulation over reusable frame scratch;
// a nil scratch allocates, which only the unit tests do. crosses is the ring's
// self-intersection verdict, computed once per face per frame by the caller
// (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy").
func modelFaceTrianglesInto(f *drawlist.ModelFace, scratch *modelPrepScratch, crosses bool) ([]uint16, bool, bool) {
	n := len(f.Vertices)
	if n < 3 || n > 1<<16 {
		return nil, false, n < 3
	}
	var area int64
	for i := range f.Vertices {
		a, b := f.Vertices[i], f.Vertices[(i+1)%n]
		area += int64(a.X)*int64(b.Y) - int64(a.Y)*int64(b.X)
	}
	if area <= 0 {
		return nil, false, true
	}
	if crosses {
		return nil, true, false
	}
	// The two authored rings that dominate stock geometry share one immutable
	// index list, so a steady-state frame triangulates without allocating.
	if n == 3 {
		return modelTriIndices, true, true
	}
	if n == 4 && cross(f.Vertices[0], f.Vertices[1], f.Vertices[2]) > 0 && cross(f.Vertices[0], f.Vertices[2], f.Vertices[3]) > 0 {
		return modelQuadIndices, true, true
	}
	var remaining []int
	if scratch != nil {
		remaining = scratch.ears.take(n)
	} else {
		remaining = make([]int, n)
	}
	for i := range remaining {
		remaining[i] = i
	}
	// Collinear corners do not form an ear but do occur in authored projected
	// rings. Removing only the middle point preserves the enclosing boundary.
	for changed := true; changed && len(remaining) > 3; {
		changed = false
		for i := range remaining {
			a, b, c := remaining[(i+len(remaining)-1)%len(remaining)], remaining[i], remaining[(i+1)%len(remaining)]
			if cross(f.Vertices[a], f.Vertices[b], f.Vertices[c]) == 0 {
				remaining = append(remaining[:i], remaining[i+1:]...)
				changed = true
				break
			}
		}
	}
	var out []uint16
	if scratch != nil {
		out = scratch.indices.take(3 * (n - 2))[:0]
	} else {
		out = make([]uint16, 0, 3*(n-2))
	}
	for len(remaining) > 3 {
		found := false
		for i := range remaining {
			a, b, c := remaining[(i+len(remaining)-1)%len(remaining)], remaining[i], remaining[(i+1)%len(remaining)]
			if cross(f.Vertices[a], f.Vertices[b], f.Vertices[c]) <= 0 {
				continue
			}
			ear := true
			for _, p := range remaining {
				if p != a && p != b && p != c && insideTriangle(f.Vertices[p], f.Vertices[a], f.Vertices[b], f.Vertices[c]) {
					ear = false
					break
				}
			}
			if !ear {
				continue
			}
			out = append(out, uint16(a), uint16(b), uint16(c))
			remaining = append(remaining[:i], remaining[i+1:]...)
			found = true
			break
		}
		if !found {
			return nil, true, false
		}
	}
	out = append(out, uint16(remaining[0]), uint16(remaining[1]), uint16(remaining[2]))
	return out, true, true
}

// modelTriIndices and modelQuadIndices are shared, never-mutated index lists.
var modelTriIndices = []uint16{0, 1, 2}

func cross(a, b, c drawlist.ModelVertex) int64 {
	return (int64(b.X)-int64(a.X))*(int64(c.Y)-int64(a.Y)) - (int64(b.Y)-int64(a.Y))*(int64(c.X)-int64(a.X))
}
func insideTriangle(p, a, b, c drawlist.ModelVertex) bool {
	x, y, z := cross(a, b, p), cross(b, c, p), cross(c, a, p)
	// A point on the candidate diagonal does not occupy the ear's interior.
	// Treating it as inside can leave a valid projected ring with no ear.
	return x > 0 && y > 0 && z > 0
}
func polygonCrosses(v []drawlist.ModelVertex) bool {
	n := len(v)
	// The two rings that dominate stock geometry answer without the loop. Every
	// pair of a triangle's edges shares an endpoint, so a triangle cannot cross
	// itself; a quadrilateral has exactly two pairs of non-adjacent edges. The
	// general loop stays for the larger authored rings [03 R-RAST-01 §1].
	switch {
	case n < 4:
		return false
	case n == 4:
		return segmentsCross(v[0], v[1], v[2], v[3]) || segmentsCross(v[1], v[2], v[3], v[0])
	}
	for i := 0; i < n; i++ {
		a, b := v[i], v[(i+1)%n]
		for j := i + 1; j < n; j++ {
			if j == i || (j+1)%n == i || (i+1)%n == j {
				continue
			}
			if segmentsCross(a, b, v[j], v[(j+1)%n]) {
				return true
			}
		}
	}
	return false
}
func segmentsCross(a, b, c, d drawlist.ModelVertex) bool {
	ab1, ab2, cd1, cd2 := cross(a, b, c), cross(a, b, d), cross(c, d, a), cross(c, d, b)
	return (ab1 > 0 && ab2 < 0 || ab1 < 0 && ab2 > 0) && (cd1 > 0 && cd2 < 0 || cd1 < 0 && cd2 > 0)
}

// drawModelGeometry commits one subject from the slot the frame's atlas passes
// already rasterized. The shadow commits first, then the body or its

type preparedModelFace struct {
	// face points into the recorded geometry, which the list owns for the whole
	// frame; copying the record here cost a struct copy per face per frame
	// (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy").
	face     *drawlist.ModelFace
	tex      modelTextureSlot
	strips   []modelGPUFace
	vertices []modelGPUVertex
	indices  []uint16
	// quad is the one-based parameter index of a textured quad the fragment
	// shader maps itself; zero means the face carries its lanes on its
	// vertices, as a strip or a plain triangulated ring does.
	quad int
}

// prepareModelFace prepares one recorded face into out. crosses is the ring's
// self-intersection verdict, which the face admission already computed for this
// frame (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy").
func (r *Renderer) prepareModelFace(out *preparedModelFace, f *drawlist.ModelFace, origin image.Point, crosses bool) {
	*out = preparedModelFace{face: f}
	// A textured quad may not use a GPU diagonal for its lanes: that diagonal
	// made solar-panel textures visibly zig-zag [03 R-RAST-01 §1]. It still
	// draws as two device triangles, because the fragment shader evaluates the
	// span writer's two-chain row mapping itself from the four corners the
	// frame's parameter image carries (docs/DESIGN_GPU_RENDERER.md §11.2
	// "Textured quads without strips"). Only a ring the triangulation rejects,
	// or one whose lanes do not fit the parameter image, still needs CPU rows.
	if f.Texture != nil && len(f.Vertices) == 4 {
		r.modelStats.TexturedQuadFaces++
		if tri, paints, supported := modelFaceTrianglesInto(f, &r.modelPrep, crosses); supported {
			if !paints {
				return
			}
			if q := r.modelAtlas.quads.add(f.Vertices, origin.X, origin.Y); q != 0 {
				out.vertices, out.indices, out.quad = r.prepareModelVertices(f.Vertices), tri, q
				return
			}
		}
		out.strips = r.prepareSpanStrips(f)
		r.modelStats.TexturedQuadStrips += len(out.strips)
		return
	}
	if crosses {
		out.strips = r.prepareSpanStrips(f)
		r.modelStats.FoldedFaces++
		r.modelStats.FoldedStrips += len(out.strips)
		return
	}
	tri, paints, supported := modelFaceTrianglesInto(f, &r.modelPrep, crosses)
	if !supported {
		// A touching ring can have no valid ear yet retain positive two-chain rows
		// [03 R-RAST-01 §1]. Keep that geometry; the GPU still rasterizes its pixels.
		r.modelStats.UntriangulatedFaces++
		out.strips = r.prepareSpanStrips(f)
	} else if paints {
		out.vertices, out.indices = r.prepareModelVertices(f.Vertices), tri
	}
}

func (r *Renderer) prepareModelVertices(v []drawlist.ModelVertex) []modelGPUVertex {
	out := r.modelPrep.vertices.take(len(v))
	for i, p := range v {
		out[i] = modelGPUVertex{X: float32(p.X), Y: float32(p.Y), Key: float32(p.Key), U: float32(p.U), V: float32(p.V), Shade: float32(p.Shade)}
	}
	return out
}

// prepareModelOutline resolves the two row endpoints of every outline ring into
// reusable one-pixel quads. The endpoints keep the subject key test; no polygon
// border primitive replaces the pass [03 R-COMP-01 §3].
func (r *Renderer) prepareModelOutline(g *drawlist.ModelGeometry) []modelGPUFace {
	rows := 0
	for i := range g.Outline {
		v := g.Outline[i].Vertices
		if len(v) < 2 {
			continue
		}
		lo, hi := v[0].Y, v[0].Y
		for _, p := range v {
			lo, hi = min(lo, p.Y), max(hi, p.Y)
		}
		rows += int(hi - lo)
	}
	if rows == 0 {
		return nil
	}
	out := r.modelPrep.strips.take(2 * rows)[:0]
	corners := r.modelPrep.vertices.take(8 * rows)
	used := 0
	for i := range g.Outline {
		f := g.Outline[i]
		v := f.Vertices
		if len(v) < 2 {
			continue
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
	}
	return out
}

func foldedStrips(f drawlist.ModelFace) []modelGPUFace {
	return modelSpanStrips(f)
}

// modelSpanRows counts the rows the two-chain span mapper paints for one ring,
// without preparing a single strip. The face admission only needs to know
// whether a folded ring paints anything at all, and preparing its strips there
// allocated a backing store per folded face per frame
// [DESIGN_GPU_RENDERER.md §11.2 "Allocation policy"].
func modelSpanRows(f *drawlist.ModelFace) int {
	v := f.Vertices
	n := len(v)
	if n < 3 {
		return 0
	}
	top, bot := 0, 0
	for i := 1; i < n; i++ {
		if v[i].Y < v[top].Y {
			top = i
		}
		if v[i].Y > v[bot].Y {
			bot = i
		}
	}
	rows := 0
	for y := v[top].Y; y < v[bot].Y; y++ {
		l, a := chainAt(v, top, bot, -1, y)
		r, b := chainAt(v, top, bot, 1, y)
		if !a || !b || r.X <= l.X {
			continue
		}
		rows++
	}
	return rows
}

// modelTextureStrips prepares a textured quad from the original two-chain
// scanline mapper. Unlike the conventional triangle path, no diagonal becomes
// an interpolation boundary: every device quad is one source scanline.
func modelTextureStrips(f drawlist.ModelFace) []modelGPUFace {
	return modelSpanStrips(f)
}

func modelSpanStrips(f drawlist.ModelFace) []modelGPUFace { return modelSpanStripsInto(&f, nil, nil) }
func modelSpanStripsInto(f *drawlist.ModelFace, out []modelGPUFace, corners []modelGPUVertex) []modelGPUFace {
	v := f.Vertices
	n := len(v)
	if n < 3 {
		return nil
	}
	top, bot := 0, 0
	for i := 1; i < n; i++ {
		if v[i].Y < v[top].Y {
			top = i
		}
		if v[i].Y > v[bot].Y {
			bot = i
		}
	}
	// One backing store per face replaces one allocation per source row.
	rows := int(v[bot].Y - v[top].Y)
	if cap(out) < rows {
		out = make([]modelGPUFace, rows)
	}
	out = out[:0]
	if cap(corners) < rows*4 {
		corners = make([]modelGPUVertex, rows*4)
	} else {
		corners = corners[:rows*4]
	}
	for y := v[top].Y; y < v[bot].Y; y++ {
		l, a := chainAt(v, top, bot, -1, y)
		r, b := chainAt(v, top, bot, 1, y)
		if !a || !b || r.X <= l.X {
			continue
		}
		// A strip is one device quad for one positive CPU row. X follows the
		// CPU walk's biased 16.16 edge accumulator (computed by chainAt); all
		// other attributes stay fractional and constant through the one-pixel
		// vertical extent. Narrowing those lanes early changes key ownership.
		xl, xr := l.X, r.X
		if xr <= xl {
			continue
		}
		// Device varyings are evaluated at a pixel centre, while the span writer
		// starts each lane at its integer left column. Shift both endpoint lanes
		// back by half a horizontal step so the first device sample is leftA and
		// the last is leftA+(width-1)*step [03 R-RAST-01 §1].
		l.Key, r.Key = centerSampledSpan(l.Key, r.Key, xr-xl)
		l.U, r.U = centerSampledSpan(l.U, r.U, xr-xl)
		l.V, r.V = centerSampledSpan(l.V, r.V, xr-xl)
		l.Shade, r.Shade = centerSampledSpan(l.Shade, r.Shade, xr-xl)
		l.X, r.X = xl, xr
		l.Y, r.Y = float32(y), float32(y)
		bottomL, bottomR := l, r
		bottomL.Y, bottomR.Y = float32(y+1), float32(y+1)
		offset := len(out) * 4
		strip := corners[offset : offset+4 : offset+4]
		strip[0], strip[1], strip[2], strip[3] = l, r, bottomR, bottomL
		out = append(out, modelGPUFace{Vertices: strip, Texture: f.Texture, Color: f.Color, Shaded: f.Shaded})
	}
	return out
}

func centerSampledSpan(left, right, width float32) (float32, float32) {
	step := (right - left) / width
	return left - step*0.5, right - step*0.5
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
				U:   mix(float32(a.U), float32(b.U)), V: mix(float32(a.V), float32(b.V)),
				Shade: mix(float32(a.Shade), float32(b.Shade)),
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

func boolFloat(v bool) float32 {
	if v {
		return 1
	}
	return 0
}
