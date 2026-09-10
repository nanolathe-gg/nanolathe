package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// recordShadowSubject records one geometry-only frame and returns the subject's
// recorded shadow packet and its retained body.
func recordShadowSubject(t *testing.T, c *Client, v frame.UnitView) (*drawlist.ModelGeometry, *cachedModelBody) {
	t.Helper()
	c.list.Reset()
	c.modelScratch.reset()
	c.modelScratch.active = true
	defer func() { c.modelScratch.active = false }()
	if !c.drawUnitModel(v, 0, 0) {
		t.Fatal("subject was not recorded")
	}
	models := c.list.ModelCommands()
	if len(models) == 0 || models[0].Geometry == nil || models[0].Geometry.Shadow == nil {
		t.Fatalf("recorded %d commands with no shadow lane", len(models))
	}
	return models[0].Geometry.Shadow, c.cachedModelBodies[v.InstanceID]
}

func sameShadowFaces(a, b *drawlist.ModelGeometry) bool {
	if a.Width != b.Width || a.Height != b.Height || a.OriginX != b.OriginX || a.OriginY != b.OriginY ||
		a.AnchorX != b.AnchorX || a.AnchorY != b.AnchorY || len(a.Faces) != len(b.Faces) {
		return false
	}
	for i := range a.Faces {
		if len(a.Faces[i].Vertices) != len(b.Faces[i].Vertices) {
			return false
		}
		for j := range a.Faces[i].Vertices {
			if a.Faces[i].Vertices[j] != b.Faces[i].Vertices[j] {
				return false
			}
		}
	}
	return true
}

// TestRetainedShadowLaneKeepsProjectionUntilThePoseMoves locks
// docs/DESIGN_GPU_RENDERER.md §13.12 "Shadows — contract P4": the shadow
// projection is retained beside the body, is reprojected only when its own
// inputs change, carries a slot identity of the shadow lane that no body key can
// equal, and reuse reproduces the projection exactly.
func TestRetainedShadowLaneKeepsProjectionUntilThePoseMoves(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.geometryOnlyModels = true
	c.shadows, c.vehicleShadows = true, true
	c.pal = &palette.Tables{}

	first, body := recordShadowSubject(t, c, v)
	if body == nil || body.shadowRevision != 1 {
		t.Fatalf("first frame retained shadow revision %d, want 1", bodyShadowRevision(body))
	}
	key := first.Cache
	if !key.Reusable() || key.Lane != drawlist.ModelCacheLaneShadow {
		t.Fatalf("recorded shadow key = %+v, want a reusable shadow lane", key)
	}
	if key == body.cacheKey(key.HalfX, key.HalfY) {
		t.Fatal("the shadow lane took the body's own slot identity")
	}
	// The frame packet is frame scratch, so keep the corners rather than the
	// pointer before recording again.
	want := first.Clone()

	second, body := recordShadowSubject(t, c, v)
	if body.shadowRevision != 1 {
		t.Fatalf("an unchanged pose reprojected the shadow (revision %d)", body.shadowRevision)
	}
	if second.Cache != key {
		t.Fatalf("reused shadow key = %+v, want %+v", second.Cache, key)
	}
	if !sameShadowFaces(want, second) {
		t.Fatal("the reused projection is not the one the first frame recorded")
	}

	// A live piece translating is a pose the body's cached lane does not see —
	// its retained faces are the cached lane alone — but the shadow projects
	// every piece, so the projection is a new raster and says so.
	v.Pieces[1].Tx = numeric.Fixed(48 << 16)
	moved, body := recordShadowSubject(t, c, v)
	if body.shadowRevision != 2 {
		t.Fatalf("a moved pose kept shadow revision %d, want 2", body.shadowRevision)
	}
	if moved.Cache == key || moved.Cache.Lane != drawlist.ModelCacheLaneShadow {
		t.Fatalf("moved shadow key = %+v, want a new revision on the shadow lane", moved.Cache)
	}
	if sameShadowFaces(want, moved) {
		t.Fatal("a moved pose recorded the retained projection unchanged")
	}
}

func bodyShadowRevision(b *cachedModelBody) uint64 {
	if b == nil {
		return 0
	}
	return b.shadowRevision
}
