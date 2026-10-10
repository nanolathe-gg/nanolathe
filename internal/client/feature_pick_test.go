package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestFeatureFaceContainsAdmitsFrontEdgesAndConcavity(t *testing.T) {
	points := [][2]int32{{0, 0}, {12, 0}, {12, 12}, {6, 6}, {0, 12}}
	front := []uint16{0, 1, 2, 3, 4}
	for _, p := range [][2]int32{{6, 0}, {0, 6}, {6, 6}, {6, 3}, {10, 10}} {
		if !featureFaceContains(points, front, p[0], p[1]) {
			t.Fatalf("front face omitted %v", p)
		}
	}
	if featureFaceContains(points, front, 6, 10) || featureFaceContains(points, front, -1, 3) {
		t.Fatal("outside a concave body admitted")
	}
	if featureFaceContains(points, []uint16{4, 3, 2, 1, 0}, 6, 3) || featureFaceContains(points, []uint16{0, 1, 1}, 6, 0) || featureFaceContains(points, []uint16{0, 1, 99}, 6, 0) {
		t.Fatal("back, degenerate or invalid face admitted")
	}
}

// Picking must agree with drawing after parent translation and root rotation,
// not merely hit a convenient origin or root-piece rectangle. Interior painted
// pixels are evidence from the existing composer; raster boundary sampling is
// deliberately outside the Modern geometric policy.
func TestFeaturePickingFollowsDrawnParentAndRootTransforms(t *testing.T) {
	for _, angles := range [][3]uint16{{0, 0, 0}, {8192, 4096, 2048}, {24576, 4096, 61440}} {
		c := compositionClient(t)
		for row := range c.pal.Shade {
			for color := range c.pal.Shade[row] {
				c.pal.Shade[row][color] = byte(color)
			}
		}
		m := featureShadowModel()
		child := m.compiled.Pieces[0]
		child.Parent, child.Translate = 0, [3]numeric.Fixed{8 << 16, 4 << 16, -4 << 16}
		m.compiled.Pieces = []model.Piece{{Parent: -1, Children: []int{1}}, child}
		c.models = map[string]*unitModel{"wreck": m}
		v := frame.FeatureView{InstanceID: 1, Model: "wreck", X: 24 << 16, Z: 32 << 16, Heading: angles[0], Pitch: angles[1], Bank: angles[2]}
		c.resetListForTest()
		if !c.drawFeatureModel(v) {
			t.Fatal("authored child did not draw")
		}
		c.replayForTest()
		found := false
		for y := 1; y < c.height-1; y++ {
			for x := 1; x < c.width-1; x++ {
				i := y*c.width + x
				if c.indexed[i] == 0 || c.indexed[i-1] == 0 || c.indexed[i+1] == 0 || c.indexed[i-c.width] == 0 || c.indexed[i+c.width] == 0 {
					continue
				}
				found = true
				if !c.FeatureContainsPoint(v, c.cam, int32(x), int32(y)) {
					t.Fatalf("angles=%v: drawn interior (%d,%d) missed", angles, x, y)
				}
			}
		}
		if !found {
			t.Fatalf("angles=%v: fixture painted no interior", angles)
		}
	}
}

func TestFeaturePickingOmitsSelectionAndAttachmentGeometry(t *testing.T) {
	c := compositionClient(t)
	m := featureShadowModel()
	m.compiled.Pieces[0].Selection = true
	c.models = map[string]*unitModel{"wreck": m}
	v := frame.FeatureView{Model: "wreck", X: 16 << 16, Z: 24 << 16}
	if c.FeatureContainsPoint(v, c.cam, 28, 28) {
		t.Fatal("selection plate admitted")
	}
	m.compiled.Pieces[0].Selection = false
	m.compiled.Pieces[0].Primitives = nil
	if c.FeatureContainsPoint(v, c.cam, 28, 28) {
		t.Fatal("attachment vertices admitted")
	}
	if c.FeatureContainsPoint(frame.FeatureView{Model: "missing"}, c.cam, 0, 0) || c.FeatureContainsPoint(v, (*camera.Camera)(nil), 0, 0) {
		t.Fatal("missing model or camera admitted")
	}
}
