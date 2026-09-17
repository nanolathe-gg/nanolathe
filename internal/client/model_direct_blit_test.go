package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// directBlitSubject is a keyless mover: no ZBuffer and every piece live, so the
// body commits and the live lane then rasterizes straight into framebuffer
// space [03 R-REN-03A §4]. The two stock definitions that reach the keyless
// route are CORFAV and CORTRUCK [fmt fbi].
func directBlitSubject(t *testing.T) (*Client, frame.UnitView) {
	t.Helper()
	c := newTestClient(t)
	c.models["direct-blit"] = syntheticModel([]pieceInfo{{name: "hull", parent: -1}}, []syntheticTri{
		makeTriangle(0, "hull", [3][3]float64{{0, 0, 0}, {12, 0, 0}, {0, 0, -12}}, 201, 0),
	}, 0)
	v := frame.UnitView{
		InstanceID: 51, Slot: 5, Model: "direct-blit",
		X: numeric.Fixed(120 << 16), Z: numeric.Fixed(90 << 16),
		CacheRevision: 1, CacheValidityRevision: 1,
		Pieces: []frame.PieceView{{Index: 0, DontCache: true}},
	}
	return c, v
}

// recordDirectBlitSubject draws the fixture at one view scale and replays it.
func recordDirectBlitSubject(t *testing.T, scale camera.ViewScale) (*Client, frame.UnitView) {
	t.Helper()
	c, v := directBlitSubject(t)
	c.cam.Scale = scale
	if got := c.modelBlitScale(); got != scale {
		t.Fatalf("classic blit scale = %v, want the view scale %v", got, scale)
	}
	clearIndexed(c)
	c.resetListForTest()
	c.modelScratch.reset()
	c.modelScratch.active = true
	defer func() { c.modelScratch.active = false }()
	sx, sy := c.cam.WorldToScreen(v.X, v.Y, v.Z)
	if !c.drawUnitModel(v, sx, sy) {
		t.Fatalf("scale %v: keyless mover was not recorded", scale)
	}
	c.replayForTest()
	return c, v
}

// TestDirectLiveLaneBlitsNative pins the blit factor the direct live lane
// commits with.
//
// Original rasterizes a cached body at native size and doubles it on the blit,
// so the cached command carries the view scale. The direct live lane is the
// other half of §14.2: its corners come from the camera at the current record
// step, so the committed image already holds framebuffer coordinates and must
// blit one-to-one. Accepting the view-scale fallback would place and size it
// twice over — four times the intended offset from the viewport origin. The
// sibling direct paths (debris, fragments) declare the same native blit.
func TestDirectLiveLaneBlitsNative(t *testing.T) {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		c, _ := recordDirectBlitSubject(t, scale)
		commands := c.list.ModelCommands()
		if len(commands) != 2 {
			t.Fatalf("scale %v: want the cached body and its live lane, got %d commands", scale, len(commands))
		}
		body, live := commands[0].Classic, commands[1].Classic
		if body == nil || body.Body == nil || live == nil || live.Body == nil {
			t.Fatalf("scale %v: both commands must carry a classic body", scale)
		}
		if got := body.Body.Blit; got != scale {
			t.Fatalf("scale %v: cached body blit = %v, want the view scale", scale, got)
		}
		if got := live.Body.Blit; got != camera.ViewScaleNative {
			t.Fatalf("scale %v: direct live body blit = %v, want %v (the projection already carried the scale)", scale, got, camera.ViewScaleNative)
		}
	}
}

// TestDirectLiveLanePlacementFollowsProjection is the pixel half of the same
// contract. The live lane draws the same face as the body it follows, so at the
// detail view every live pixel has to land inside the doubled body's box. A
// lane blitted at the view scale a second time would leave that box entirely.
func TestDirectLiveLanePlacementFollowsProjection(t *testing.T) {
	c, v := recordDirectBlitSubject(t, camera.ViewScaleDetail)
	sx, sy := c.cam.WorldToScreen(v.X, v.Y, v.Z)
	// The composition anchor is the projected point relative to the viewport
	// origin, and the doubled body spans twice the fixture's twelve pixels
	// from it [R-REN-03A §1].
	left, top := int(sx-camera.OriginX), int(sy-camera.OriginY)
	const span = 2 * 12
	drawn := 0
	for i, b := range c.indexed {
		if b != 201 {
			continue
		}
		drawn++
		x, y := i%c.width, i/c.width
		if x < left || x >= left+span || y < top || y >= top+span {
			t.Fatalf("detail-view pixel at (%d,%d) is outside the doubled body box (%d,%d)-(%d,%d): the live lane was scaled twice",
				x, y, left, top, left+span-1, top+span-1)
		}
	}
	if drawn == 0 {
		t.Fatal("the detail-view fixture drew nothing")
	}
}
