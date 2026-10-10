package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
)

// FeatureContainsPoint tests projected body faces at the committed feature
// position. The Modern command policy owns admission and overlap priority;
// this helper reads the same authored model as drawFeatureModel [I6]. It
// deliberately excludes selection plates, attachment points and degenerate
// faces (interface design "Modern submerged wreck picking").
func (c *Client) FeatureContainsPoint(v frame.FeatureView, cam *camera.Camera, x, y int32) bool {
	if c == nil || cam == nil {
		return false
	}
	m := c.modelForFeature(v)
	if m == nil || m.compiled == nil {
		return false
	}
	x, y = cam.ScreenToRecord(x+camera.OriginX, y+camera.OriginY)
	x, y = x-camera.OriginX, y-camera.OriginY
	var stateStorage [32]model.PieceState
	states := stateStorage[:]
	if len(m.compiled.Pieces) > len(states) {
		states = make([]model.PieceState, len(m.compiled.Pieces))
	}
	model.FoldRootAngles(states, m.compiled.Root, v.Heading, v.Pitch, v.Bank)
	var scratch model.ComposeScratch
	scratch.BeginModel(len(m.compiled.Pieces))
	var transform model.Transform
	var pointStorage [64][2]int32
	points := pointStorage[:]
	for i, piece := range m.compiled.Pieces {
		if len(piece.Primitives) == 0 {
			continue
		}
		if len(piece.Vertices) > len(points) {
			points = make([][2]int32, len(piece.Vertices))
		}
		transform = model.ComposeInto(m.compiled, states, i, transform, &scratch)
		for j, vertex := range piece.Vertices {
			r := transform.Apply(vertex)
			sx, sy := cam.WorldToScreen(v.X.Add(r[0]), v.Y.Add(r[1]), v.Z.Sub(r[2]))
			points[j] = [2]int32{sx - camera.OriginX, sy - camera.OriginY}
		}
		for j, face := range piece.Primitives {
			if (piece.Selection && j == 0) || len(face.VertexIndices) < 3 ||
				(face.IsColored&1 == 0 && (face.TextureName == "" || len(face.VertexIndices) != 4)) {
				continue
			}
			if featureFaceContains(points[:len(piece.Vertices)], face.VertexIndices, x, y) {
				return true
			}
		}
	}
	return false
}

// The Modern geometric policy uses an even/odd polygon test, includes edges,
// and admits positive-area faces (interface design "Modern submerged wreck
// picking"). Products widen before subtraction.
func featureFaceContains(points [][2]int32, indices []uint16, x, y int32) bool {
	var area int64
	for i, index := range indices {
		next := indices[(i+1)%len(indices)]
		if int(index) >= len(points) || int(next) >= len(points) {
			return false
		}
		a, b := points[index], points[next]
		area += int64(a[0])*int64(b[1]) - int64(b[0])*int64(a[1])
	}
	if area <= 0 {
		return false
	}
	inside := false
	for i, index := range indices {
		a, b := points[index], points[indices[(i+1)%len(indices)]]
		dx, dy := int64(b[0])-int64(a[0]), int64(b[1])-int64(a[1])
		cross := dx*(int64(y)-int64(a[1])) - dy*(int64(x)-int64(a[0]))
		if cross == 0 && x >= min(a[0], b[0]) && x <= max(a[0], b[0]) && y >= min(a[1], b[1]) && y <= max(a[1], b[1]) {
			return true
		}
		if (a[1] > y) != (b[1] > y) && ((cross > 0) == (dy > 0)) {
			inside = !inside
		}
	}
	return inside
}
