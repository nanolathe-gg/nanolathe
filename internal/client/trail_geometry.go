package client

import (
	"math"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Geometry-derived marks are Nanolathe presentation policy, not retail gait
// reconstruction (DESIGN_GPU_RENDERER §15.2). The immutable model supplies
// dimensions; committed distance still supplies the timing.
type trailGeometry struct {
	footWidth, footLength, footSpread float64
	bodyWidth                         float64
}

type trailStyle struct {
	stride, halfLength, halfWidth, spread float64
}

func (c *Client) trailStyleFor(u frame.UnitView, class trailClass) trailStyle {
	st := &c.trails
	var geometry trailGeometry
	if m := c.modelForUnit(u); m != nil {
		var found bool
		geometry, found = st.geometry[m]
		if !found {
			geometry = measureTrailGeometry(m.compiled)
			if st.geometry == nil {
				st.geometry = make(map[*unitModel]trailGeometry)
			}
			st.geometry[m] = geometry
		}
	}
	// Preserve the old presentation fallback when a model has no usable named
	// contact or body geometry. Footprint metadata is never used over geometry.
	foot := float64(max(u.FootX, 1))
	if class == trailTracks {
		style := trailStyle{stride: trailTrackStride, halfLength: trailTrackStride / 2, halfWidth: 1.75, spread: foot*4 + 2}
		if geometry.bodyWidth > 0 {
			// Scale the former 3.5-pixel strip against a 32-pixel body. Inset
			// its centre by a full strip width: the body's widest surface can
			// overhang the tread, so leave half a strip inside its outer edge.
			style.halfWidth = geometry.bodyWidth * (1.75 / 32)
			style.spread = geometry.bodyWidth/2 - 2*style.halfWidth
		}
		return style
	}
	if geometry.footWidth > 0 && geometry.footLength > 0 {
		return trailStyle{stride: max(trailFeetStride, geometry.footLength), halfLength: geometry.footLength / 2, halfWidth: geometry.footWidth / 2, spread: geometry.footSpread}
	}
	return trailStyle{stride: trailFeetStride, halfLength: 4, halfWidth: 2, spread: foot * 2}
}

type trailBounds struct {
	min, max [3]float64
	valid    bool
}

func (b *trailBounds) add(p [3]float64) {
	if !b.valid {
		b.min, b.max, b.valid = p, p, true
		return
	}
	for i := range p {
		b.min[i] = min(b.min[i], p[i])
		b.max[i] = max(b.max[i], p[i])
	}
}

// measureTrailGeometry ignores selection polygons, unused vertices and emit
// points: those are not surfaces [03 §2.4][fmt 3do]. A leg assembly starts at
// its highest leg-named ancestor and includes its named descendants (toes as
// well as soles). This prevents counting one leg's thigh, shin and foot as
// three feet or dropping the Krogoth sole in favour of its leaf toe.
func measureTrailGeometry(m *compiledmodel.Model) trailGeometry {
	var out trailGeometry
	if m == nil {
		return out
	}
	origins := make([][3]numeric.Fixed, len(m.Pieces))
	roots := make([]int, len(m.Pieces))
	bounds := make([]trailBounds, len(m.Pieces))
	for i, piece := range m.Pieces {
		origins[i] = compiledmodel.Compose(m, nil, i).Position()
		roots[i] = -1
		if !trailLegPiece(piece.Name) {
			continue
		}
		root := i
		for parent, depth := piece.Parent, 0; parent >= 0 && parent < len(m.Pieces) && depth < len(m.Pieces); depth++ {
			if trailLegPiece(m.Pieces[parent].Name) {
				root = parent
			}
			parent = m.Pieces[parent].Parent
		}
		roots[i] = root
		visitTrailEdges(piece, origins[i], func(a, b [3]float64) { bounds[root].add(a); bounds[root].add(b) })
	}
	// The lowest two world pixels form an approximate sole. Intersect edges
	// with the band ceiling as well as including vertices, so pointed single-
	// piece legs get a finite tip rather than the width of the whole limb.
	soles := make([]trailBounds, len(m.Pieces))
	for i, piece := range m.Pieces {
		root := roots[i]
		if root < 0 || !bounds[root].valid {
			continue
		}
		ceiling := bounds[root].min[1] + 2
		visitTrailEdges(piece, origins[i], func(a, b [3]float64) {
			if a[1] <= ceiling {
				soles[root].add(a)
			}
			if b[1] <= ceiling {
				soles[root].add(b)
			}
			if (a[1] < ceiling && b[1] > ceiling) || (b[1] < ceiling && a[1] > ceiling) {
				t := (ceiling - a[1]) / (b[1] - a[1])
				soles[root].add([3]float64{a[0] + t*(b[0]-a[0]), ceiling, a[2] + t*(b[2]-a[2])})
			}
		})
	}
	count := 0
	for _, sole := range soles {
		if !sole.valid {
			continue
		}
		// A two-pixel minimum keeps a pointed contact drawable: a one-pixel
		// oval centred on an integer can miss every raster sample. It is a visual
		// policy, not an assertion of an authored physical sole thickness.
		out.footWidth += max(2, sole.max[0]-sole.min[0])
		out.footLength += max(2, sole.max[2]-sole.min[2])
		out.footSpread += math.Abs((sole.min[0] + sole.max[0]) / 2)
		count++
	}
	if count > 0 {
		out.footWidth /= float64(count)
		out.footLength /= float64(count)
		out.footSpread /= float64(count)
	}
	// Conventional tanks often paint their treads on the base texture. Use
	// body faces, never turret/barrel extents or the selection polygon.
	candidates := []int{m.Root}
	for i, piece := range m.Pieces {
		switch strings.ToLower(piece.Name) {
		case "base", "body", "chassis":
			if i != m.Root {
				candidates = append(candidates, i)
			}
		}
	}
	for _, i := range candidates {
		if i < 0 || i >= len(m.Pieces) {
			continue
		}
		var body trailBounds
		visitTrailEdges(m.Pieces[i], origins[i], func(a, b [3]float64) { body.add(a); body.add(b) })
		if body.valid && body.max[0] > body.min[0] {
			out.bodyWidth = body.max[0] - body.min[0]
			break
		}
	}
	return out
}

func visitTrailEdges(piece compiledmodel.Piece, origin [3]numeric.Fixed, visit func(a, b [3]float64)) {
	for i, face := range piece.Primitives {
		if piece.Selection && i == 0 || len(face.VertexIndices) < 3 {
			continue
		}
		for j, index := range face.VertexIndices {
			next := face.VertexIndices[(j+1)%len(face.VertexIndices)]
			if int(index) >= len(piece.Vertices) || int(next) >= len(piece.Vertices) {
				continue
			}
			var a, b [3]float64
			for axis := range a {
				a[axis] = float64(piece.Vertices[index][axis]+origin[axis]) / float64(numeric.FixedOne)
				b[axis] = float64(piece.Vertices[next][axis]+origin[axis]) / float64(numeric.FixedOne)
			}
			visit(a, b)
		}
	}
}
