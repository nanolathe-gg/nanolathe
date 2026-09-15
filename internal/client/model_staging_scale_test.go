package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Classic composes attachment offsets at native resolution, then magnifies
// the complete image about the carrier anchor (DESIGN_GPU_RENDERER §14.2).
func TestCarrierStagingScaleAppliesDisplacementOnce(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		scale                              camera.ViewScale
		px, py, dx, dy, screenDX, screenDY int32
	}{
		{"native", camera.ViewScaleNative, 49, 50, 3, -3, 3, -3},
		{"double", camera.ViewScaleDetail, 49, 50, 3, -3, 6, -6},
		{"double-negative", camera.ViewScaleDetail, 52, 47, -3, 3, -6, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{cam: &camera.Camera{Scale: tc.scale}, width: 256, height: 256, indexed: make([]byte, 256*256)}
			makeModel := func(x, y int32, color byte) composedModel {
				// Fractional coordinates and negative height also exercise the
				// integer world projection's arithmetic height shear.
				draw := &presentationrender.UnitDraw{WorldPos: [3]numeric.Fixed{
					numeric.Fixed(int64(x)<<16 | 0x8000),
					numeric.Fixed(-4<<16 | 0x8000),
					numeric.Fixed(int64(y-2)<<16 | 0x8000),
				}}
				ax, ay := c.modelAnchor(draw)
				return composedModel{draw: draw, image: stagingBody(1, 1, ax, ay, color, 60)}
			}
			parent := makeModel(tc.px, tc.py, 10)
			child := makeModel(tc.px+tc.dx, tc.py+tc.dy, 20)
			originalX, originalY := child.image.anchorX, child.image.anchorY
			dx, dy := c.stagingDisplacement(parent, child)
			staged := stagingChild{model: child, keyDelta: 7, rasterDX: dx, rasterDY: dy}
			target := staged.stagingTarget()
			if target.anchorX-parent.image.anchorX != tc.dx || target.anchorY-parent.image.anchorY != tc.dy {
				t.Fatalf("raster displacement = (%d,%d), want (%d,%d)", target.anchorX-parent.image.anchorX, target.anchorY-parent.image.anchorY, tc.dx, tc.dy)
			}
			image := newStagingImage(parent.image, []stagingChild{staged})
			image.compositeChild(&target, staged.keyDelta)
			idx := int(image.imageY(target.anchorY))*image.width + int(image.imageX(target.anchorX))
			if image.color[idx] != 20 || image.height[idx] != 67 {
				t.Fatalf("staged child colour/key = %d/%d, want 20/67", image.color[idx], image.height[idx])
			}
			classicSink{c: c}.Model(drawlist.Model{Classic: &drawlist.ClassicModel{Body: c.classicModelImage(image)}})
			x, y := parent.image.anchorX+tc.screenDX, parent.image.anchorY+tc.screenDY
			if got := c.indexed[int(y)*c.width+int(x)]; got != 20 {
				t.Fatalf("final child pixel at (%d,%d) = %d, want 20", x, y, got)
			}
			if child.image.anchorX != originalX || child.image.anchorY != originalY || child.image.height[0] != 60 {
				t.Fatal("staging changed the child's retained image or shadow/trace anchor")
			}
			if tc.scale.Native() && (dx != 0 || dy != 0) {
				t.Fatal("native staging moved the original child")
			}
			c.enhanced = true
			if dx, dy := c.stagingDisplacement(parent, child); dx != 0 || dy != 0 {
				t.Fatal("output-scale composition must retain its screen displacement")
			}
		})
	}
}
