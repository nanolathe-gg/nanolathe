package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Sprite anchors use the anchor cell's four heights, not the height at the
// footprint center. Body and shadow must share that placement [03 §5.1.4].
func TestSpriteFeatureAnchorUsesAnchorCellHeights(t *testing.T) {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		c, shadow, body := newFeatureRasterClient(t)
		c.cam = &camera.Camera{Scale: scale}
		c.terrain = &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16)}
		c.terrain.PlotAt(2, 2).SetHeight(83)
		v := featureRasterView()
		v.CX, v.CZ, v.FootX, v.FootZ = 1, 1, 2, 2
		v.X, v.Z = px(32), px(32)
		v.Y = c.terrain.HeightAt(v.X, v.Z)
		v.SeqName = "body"
		if v.Y != px(83) {
			t.Fatalf("fixture center height = %v, want 83", v.Y)
		}
		c.drawFeature(&v)
		commands := recordedFeatureSprites(&c.list)
		if len(commands) != 2 {
			t.Fatalf("body/shadow commands = %d, want 2", len(commands))
		}
		// 83 >> 3 is 10; center sampling would shear by 41. Floor the
		// shear before scaling, rather than scaling the fractional mean.
		wantX, wantY := scale.Project(32), scale.Project(32-10)
		shadow = c.viewFrame(shadow)
		body = c.viewFrame(body)
		if commands[0].X != wantX-int32(shadow.XOffset) || commands[0].Y != wantY-int32(shadow.YOffset) ||
			commands[1].X != wantX-int32(body.XOffset) || commands[1].Y != wantY-int32(body.YOffset) {
			t.Fatalf("scale %v: sprite commands do not share anchor (%d,%d): %+v", scale, wantX, wantY, commands)
		}
		// A 3DO feature's pseudo-unit keeps its center-sampled instance Y.
		v.Model, v.Filename = "wreck", ""
		x, y := c.featureScreenPos(v)
		if x != wantX || y != scale.Project(32-41) {
			t.Fatalf("3DO anchor = (%d,%d), want instance-height projection", x, y)
		}
	}
}
