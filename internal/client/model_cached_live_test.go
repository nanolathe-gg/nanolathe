package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// recordKeyedSubject records one geometry-only frame of v and returns the
// subject's body packet.
func recordKeyedSubject(t *testing.T, c *Client, v frame.UnitView) *drawlist.ModelGeometry {
	t.Helper()
	c.list.Reset()
	c.modelScratch.reset()
	c.modelScratch.active = true
	defer func() { c.modelScratch.active = false }()
	if !c.drawUnitModel(v, 0, 0) {
		t.Fatal("subject was not recorded")
	}
	models := c.list.ModelCommands()
	if len(models) == 0 || models[0].Geometry == nil {
		t.Fatalf("recorded %d commands with no body", len(models))
	}
	return models[0].Geometry
}

// The cache key names the retained CACHED lane alone (drawlist.ModelCacheKey,
// docs/DESIGN_GPU_RENDERER.md §13.12): a keyed subject with a live piece
// carries its key beside this frame's live faces, the key is the same on the
// next frame while the live pose moves, and a validity clear that rebuilds the
// cached lane is a new revision of the same body.
func TestKeyedLiveLaneKeepsTheCachedLaneKey(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.geometryOnlyModels, c.antiAlias, c.pal = true, true, &palette.Tables{}
	v.Pieces[1].Tx = numeric.Fixed(48 << 16)

	first := recordKeyedSubject(t, c, v)
	if !first.KeyPlane || len(first.LiveFaces) == 0 || first.Supersample == nil || len(first.Supersample.LiveFaces) == 0 {
		t.Fatalf("first packet keyed=%v live=%d doubled=%v, want a keyed packet carrying both lanes", first.KeyPlane, len(first.LiveFaces), first.Supersample != nil)
	}
	key := first.Cache
	if !key.Reusable() || key.Lane != drawlist.ModelCacheLaneBody {
		t.Fatalf("keyed packet with a live lane carries key %+v, want a reusable body key", key)
	}
	firstLive := first.LiveFaces[0].Vertices[0]

	v.Pieces[1].Tx = 0
	second := recordKeyedSubject(t, c, v)
	if second.Cache != key {
		t.Fatalf("second frame key %+v, want the first frame's %+v: the live pose is not an input of the cached lane", second.Cache, key)
	}
	if len(second.LiveFaces) == 0 || second.LiveFaces[0].Vertices[0] == firstLive {
		t.Fatal("the live lane did not follow the pose while the key held")
	}

	v.CacheValidityRevision++
	third := recordKeyedSubject(t, c, v)
	if third.Cache == key || third.Cache.Body != key.Body || third.Cache.Revision == key.Revision {
		t.Fatalf("a validity clear gave key %+v after %+v, want the same body at a new revision", third.Cache, key)
	}
}

// A nanoframe keeps its key too: the reveal band and the outline are
// per-frame verdicts over the same retained faces, and the cached lane is
// re-stored — a new revision — only when the construction fraction moves.
func TestNanoframeKeepsTheCachedLaneKey(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.geometryOnlyModels, c.antiAlias, c.pal = true, true, &palette.Tables{}
	v.BuildRemaining = 0.5

	first := recordKeyedSubject(t, c, v)
	if first.Reveal == nil || len(first.Outline) == 0 {
		t.Fatalf("nanoframe packet reveal=%v outline=%d, want both", first.Reveal != nil, len(first.Outline))
	}
	key := first.Cache
	if !key.Reusable() {
		t.Fatalf("nanoframe packet carries key %+v, want a reusable one", key)
	}
	c.frameTick++
	second := recordKeyedSubject(t, c, v)
	if second.Cache != key {
		t.Fatalf("a later tick of the same nanoframe gave key %+v, want %+v", second.Cache, key)
	}
	v.BuildRemaining = 0.4
	third := recordKeyedSubject(t, c, v)
	if third.Cache == key || third.Cache.Body != key.Body {
		t.Fatalf("construction progress gave key %+v after %+v, want the same body at a new revision", third.Cache, key)
	}
}

// sameGeometryFaces compares two packets' boxes, placement and native faces.
func sameGeometryFaces(a, b *drawlist.ModelGeometry) bool {
	if a == nil || b == nil {
		return a == b
	}
	return sameShadowFaces(a, b)
}

func wreckFixtureClient(t *testing.T) *Client {
	t.Helper()
	c := newPieceFixtureClient(t)
	c.geometryOnlyModels, c.antiAlias, c.pal = true, true, &palette.Tables{}
	frames := make([]formats.GAFFrameRef, 10)
	for i := range frames {
		frames[i].Frame = &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{byte(100 + i)}}
	}
	c.texIndex = map[string]texRef{"logo": {kind: texTeam, key: "logo", entry: &formats.GAFEntry{Frames: frames}}}
	c.models = map[string]*unitModel{"wreck": teamLogoTestModel()}
	c.buffer = frame.NewBuffer()
	f := c.buffer.BeginWrite()
	f.Players[0] = frame.PlayerRow{Present: true, Logo: 7}
	if err := c.buffer.Publish(1); err != nil {
		t.Fatal(err)
	}
	return c
}

// recordFeature records one geometry-only frame of f and returns its packet.
func recordFeature(t *testing.T, c *Client, f frame.FeatureView, retained bool) *drawlist.ModelGeometry {
	t.Helper()
	c.resetListForTest()
	c.modelScratch.reset()
	c.modelScratch.active = retained
	defer func() { c.modelScratch.active = false }()
	if !c.drawFeatureModel(f) {
		t.Fatal("feature was not recorded")
	}
	models := c.list.ModelCommands()
	if len(models) != 1 || models[0].Geometry == nil {
		t.Fatalf("recorded %d commands, want the feature's body", len(models))
	}
	return models[0].Geometry
}

// A 3DO feature with a publication identity is retained beside it and
// rebased per frame (featureGeometry): the packet carries a reusable key that
// holds while the projection's inputs hold, is corner for corner the packet
// the per-frame projection builds, takes a new revision when the corpse's
// orientation changes, and is pruned with the committed feature set.
func TestFeatureModelIsRetainedAndKeyed(t *testing.T) {
	// Under a published identity, and under the map position a feature
	// without one is retained by (which is every feature the session
	// publishes today).
	for _, id := range []uint64{9, 0} {
		t.Run(map[bool]string{true: "identity", false: "position"}[id != 0], func(t *testing.T) {
			testFeatureModelIsRetainedAndKeyed(t, id)
		})
	}
}

func testFeatureModelIsRetainedAndKeyed(t *testing.T, id uint64) {
	c := wreckFixtureClient(t)
	f := frame.FeatureView{Model: "wreck", InstanceID: id, X: numeric.Fixed(100 << 16), Z: numeric.Fixed(100 << 16), Heading: 3000}
	bodyKey := featureBodyID(id, f.X, f.Z)

	fresh := recordFeature(t, c, f, false).Clone()
	if fresh.Cache.Reusable() {
		t.Fatalf("the per-frame projection carries key %+v, want none", fresh.Cache)
	}
	first := recordFeature(t, c, f, true)
	key := first.Cache
	if !key.Reusable() || key.Lane != drawlist.ModelCacheLaneBody {
		t.Fatalf("retained feature packet carries key %+v, want a reusable body key", key)
	}
	if !sameGeometryFaces(fresh, first) || !sameGeometryFaces(fresh.Supersample, first.Supersample) {
		t.Fatal("the rebased feature packet is not the per-frame projection")
	}
	body := c.cachedModelBodies[bodyKey]
	if body == nil || body.geometryRevision != 1 {
		t.Fatalf("feature body = %+v, want one retained at revision 1", body)
	}

	// Under a published identity, moving the feature is placement only: the
	// same key, the same faces at the new anchor, no re-projection. (A feature
	// retained by its position is another feature once it stands elsewhere.)
	if id != 0 {
		f.X += numeric.Fixed(7 << 16)
		moved := recordFeature(t, c, f, true)
		if moved.Cache != key || body.geometryRevision != 1 {
			t.Fatalf("a moved feature gave key %+v (revision %d), want %+v at revision 1", moved.Cache, body.geometryRevision, key)
		}
		if moved.AnchorX == first.AnchorX || len(moved.Faces) != len(first.Faces) || moved.Faces[0].Vertices[0] != first.Faces[0].Vertices[0] {
			t.Fatal("the moved packet did not keep the retained faces at a new anchor")
		}
	}

	// A corpse lying another way is a new projection.
	f.Heading = 9000
	turned := recordFeature(t, c, f, true)
	if turned.Cache == key || turned.Cache.Body != key.Body || body.geometryRevision != 2 {
		t.Fatalf("a turned feature gave key %+v (revision %d) after %+v, want the same body at revision 2", turned.Cache, body.geometryRevision, key)
	}
	if freshTurned := recordFeature(t, c, f, false); !sameGeometryFaces(freshTurned, turned) {
		t.Fatal("the re-projected feature packet is not the per-frame projection")
	}

	// The team colour of its LOGOS faces is an input.
	fr := c.buffer.BeginWrite()
	fr.Players[0] = frame.PlayerRow{Present: true, Logo: 2}
	if err := c.buffer.Publish(2); err != nil {
		t.Fatal(err)
	}
	if recoloured := recordFeature(t, c, f, true); recoloured.Cache == turned.Cache || body.geometryRevision != 3 {
		t.Fatalf("a recoloured feature gave key %+v (revision %d), want a new revision", recoloured.Cache, body.geometryRevision)
	}

	// Pruning keeps the body while the feature was recorded recently and
	// ages it out after featureRetainTicks without a recording.
	c.pruneCachedModelBodies(&frame.Frame{Tick: c.frameTick + featureRetainTicks})
	if c.cachedModelBodies[bodyKey] == nil {
		t.Fatal("a recently recorded feature's body was pruned")
	}
	c.pruneCachedModelBodies(&frame.Frame{Tick: c.frameTick + featureRetainTicks + 1})
	if c.cachedModelBodies[bodyKey] != nil {
		t.Fatal("an aged-out feature body survived the prune")
	}
}
