package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// standaloneAnimatedFixture is a two-piece model whose every primitive carries
// one animated texture, loaded into a battle-shaped registry: the compiled
// model is bound, so a unit or feature draw of it resolves through the binder,
// while a projectile or debris draw of it carries the per-call scratch model
// the standalone builder fills, which no binder ever holds [03 §5.2].
func standaloneAnimatedFixture(t *testing.T) (*Client, frame.ProjectileView) {
	t.Helper()
	c := newPieceFixtureClient(t)
	m := syntheticModel(
		[]pieceInfo{{name: "body", parent: -1}, {name: "child", parent: 0}},
		[]syntheticTri{
			makeTriangle(0, "body", [3][3]float64{{0, 0, 0}, {12, 0, 0}, {0, 0, 12}}, 0, 0),
			makeTriangle(1, "child", [3][3]float64{{30, 0, 0}, {42, 0, 0}, {30, 0, 12}}, 0, 1),
		}, 0)
	for i := range m.compiled.Pieces {
		m.compiled.Pieces[i].Primitives[0].TextureName = "shared"
		m.compiled.Pieces[i].Primitives[0].IsColored = 0
	}
	c.models["standalone-animated"] = m
	entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{
		{Value: 1, Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{41}}},
		{Value: 1, Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{42}}},
	}}
	ref := texRef{kind: texAnimated, key: "synthetic|shared", entry: entry, frame: entry.Frames[0].Frame}
	c.texIndex["shared"] = ref

	r := newModelTextureRegistry(nil, false)
	load := modelTextureLoadKey{kind: modelLoadProjectile, id: "7"}
	r.loads[load] = m
	r.projectileByID[7] = load
	r.byCompiled[m.compiled] = load
	for i := range m.compiled.Pieces {
		r.bindings[modelTexturePrimitiveKey{load: load, piece: i, primitive: 0}] = newModelTextureCursor(ref)
	}
	c.modelTextures = r

	publishProjectileOrientationTick(t, c, 10)
	p := frame.ProjectileView{
		Model: "standalone-animated", WeaponID: 7, Handle: 77,
		RenderType: render.RenderTypeBaseSpriteModel, ExpiryTick: 11,
		Yaw: 0x8000, Pitch: 0x8000,
	}
	return c, p
}

// TestStandaloneAnimatedPrimitiveIsDrawnNotSkipped pins the binder seam.
//
// The binder is keyed on the loaded model. A projectile or debris draw carries
// the one-piece scratch model the standalone builder fills per call, so asking
// the binder about it can only miss — and a miss used to drop the whole
// primitive, which for a model whose every face is animated means the subject
// vanishes. Such a draw must fall through to the standalone cursor instead, the
// same route a client with no registry at all takes.
func TestStandaloneAnimatedPrimitiveIsDrawnNotSkipped(t *testing.T) {
	c, p := standaloneAnimatedFixture(t)
	clearIndexed(c)
	c.resetListForTest()
	if !c.drawProjectileModel(p) {
		t.Fatal("a standalone draw whose faces are all animated textures drew nothing")
	}
	c.replayForTest()
	drawn := 0
	for _, b := range c.indexed {
		if b != 0 {
			drawn++
		}
	}
	if drawn == 0 {
		t.Fatal("the standalone animated primitives contributed no pixels")
	}

	// In a battle the registry is the session's phase-7 service, so a
	// per-subject cursor beside it could never advance. None is opened.
	if len(c.modelPresentation) != 0 {
		t.Fatalf("a battle standalone draw opened %d per-subject cursors, want none", len(c.modelPresentation))
	}
}

// TestStandaloneDrawCarryingItsLoadedModelFollowsTheBinderCursor is the retail
// contract of [03 R-COMP-02 §6] "Which cursor a detached piece reads": the
// standalone entries are handed the loaded model piece itself and resolve the
// frame through the cursor in that loaded primitive record — the same record,
// the same read, the same frame the living unit shows. The cursor's identity is
// the piece's immutable loaded-model index, not its position in the one-piece
// draw, so selecting the child must not read the parent's cursor.
func TestStandaloneDrawCarryingItsLoadedModelFollowsTheBinderCursor(t *testing.T) {
	c, _ := standaloneAnimatedFixture(t)
	m := c.models["standalone-animated"]
	ref := c.texIndex["shared"]
	load := modelTextureLoadKey{kind: modelLoadProjectile, id: "7"}

	// Advance only the child's cursor, so "the child's current frame", "the
	// parent's current frame" and "the first frame" are three distinct answers.
	c.modelTextures.players = []phase7Stepper{c.modelTextures.bindings[modelTexturePrimitiveKey{load: load, piece: 1, primitive: 0}]}
	c.modelTextures.StepPhase7()
	if got := c.modelTextures.animatedFrame(m.compiled, 1, 0, ref); got != ref.entry.Frames[1].Frame {
		t.Fatal("the fixture child cursor did not advance")
	}

	// A one-piece standalone draw that selects piece 1 but carries the loaded
	// model resolves piece 1's cursor, not piece 0's and not frame zero.
	draw := &render.UnitDraw{
		Model:       m.compiled,
		PieceStates: make([]compiledmodel.PieceState, len(m.compiled.Pieces)),
		Pieces: []render.PieceDraw{{
			Index: 0, SourceIndex: 1,
			WorldVertices: m.compiled.Pieces[1].Vertices,
			Primitives: []render.PrimitiveDraw{{
				TextureName: "shared", VertexIndices: m.compiled.Pieces[1].Primitives[0].VertexIndices,
			}},
		}},
	}
	polys := c.collectDrawPolys(draw, teamColor{}, 1, modelCursorDebris)
	if len(polys) != 1 {
		t.Fatalf("standalone draw produced %d faces, want 1", len(polys))
	}
	if polys[0].frame != ref.entry.Frames[1].Frame {
		t.Fatalf("detached piece read frame %p, want the loaded child cursor's frame %p", polys[0].frame, ref.entry.Frames[1].Frame)
	}
}

// TestPreviewStandaloneAnimatedKeepsItsPerSubjectCursor guards the other side:
// with no battle registry installed there is no loaded-model cursor to share,
// and the explicit preview path still opens one player per source piece so a
// parent and the child it selects do not alias [03 §5.2].
func TestPreviewStandaloneAnimatedKeepsItsPerSubjectCursor(t *testing.T) {
	c, p := standaloneAnimatedFixture(t)
	c.modelTextures = nil
	clearIndexed(c)
	c.resetListForTest()
	if !c.drawProjectileModel(p) {
		t.Fatal("a preview standalone draw whose faces are all animated textures drew nothing")
	}
	parentKey := modelTextureKey{kind: modelCursorProjectile, id: projectilePresentationID(p), tex: "synthetic|shared", piece: 0, primitive: 0}
	childKey := parentKey
	childKey.piece = 1
	parent, child := c.modelPresentation[parentKey], c.modelPresentation[childKey]
	if parent == nil || child == nil || parent == child {
		t.Fatalf("preview parent/child cursor bindings = %p/%p, want independent source-piece cursors", parent, child)
	}
}

// TestBoundModelStillResolvesThroughTheBinder is the other side of the seam: a
// draw that does carry the loaded model must keep using the binder's cursor,
// which phase 7 advances, and must not open a standalone one beside it.
func TestBoundModelStillResolvesThroughTheBinder(t *testing.T) {
	c, _ := standaloneAnimatedFixture(t)
	m := c.models["standalone-animated"]
	if modelTextureCursorModel(c.modelTextures, &render.UnitDraw{Model: m.compiled}) != m.compiled {
		t.Fatal("the fixture registry does not hold the loaded model")
	}
	ref := c.texIndex["shared"]
	first := c.modelTextures.animatedFrame(m.compiled, 0, 0, ref)
	if first != ref.entry.Frames[0].Frame {
		t.Fatal("a bound primitive did not begin at its first frame")
	}
	c.modelTextures.players = []phase7Stepper{c.modelTextures.bindings[modelTexturePrimitiveKey{load: modelTextureLoadKey{kind: modelLoadProjectile, id: "7"}, piece: 0, primitive: 0}]}
	c.modelTextures.StepPhase7()
	if got := c.modelTextures.animatedFrame(m.compiled, 0, 0, ref); got != ref.entry.Frames[1].Frame {
		t.Fatal("a bound primitive did not follow its binder cursor")
	}
	if len(c.modelPresentation) != 0 {
		t.Fatalf("a bound draw opened %d standalone cursors", len(c.modelPresentation))
	}
	if modelTextureCursorModel(c.modelTextures, nil) != nil {
		t.Fatal("a nil draw must not resolve a cursor model")
	}
	if modelTextureCursorModel(nil, &render.UnitDraw{Model: m.compiled}) != nil {
		t.Fatal("a nil registry must not resolve a cursor model")
	}
	if modelTextureCursorModel(c.modelTextures, &render.UnitDraw{}) != nil {
		t.Fatal("a draw carrying no model must not resolve a cursor model")
	}
	// A standalone draw carries the scratch model in Model and the loaded model
	// in SourceModel; the cursor belongs to the loaded one [R-COMP-02 §6].
	scratch := &compiledmodel.Model{}
	standalone := &render.UnitDraw{Model: scratch, SourceModel: m.compiled}
	if got := modelTextureCursorModel(c.modelTextures, standalone); got != m.compiled {
		t.Fatalf("a standalone draw resolved %p, want the loaded model %p", got, m.compiled)
	}
}

// TestBattleStandaloneDrawFollowsTheLoadedModelCursorEndToEnd goes through the
// real standalone builder rather than a hand-assembled draw, which is the only
// way to exercise the field that carries the loaded model across the per-call
// scratch model: render.BuildProjectileModelPiecesInto copies one piece into a
// scratch model and records the model it copied from, and the compose walk asks
// the binder about THAT model.
//
// Without it the walk can only miss the binder and fall back to the entry's
// first frame, which is exactly what a detached piece must not do: retail
// resolves it through the loaded primitive's own cursor, so it shows whatever
// frame the living parent shows [03 R-COMP-02 §6].
func TestBattleStandaloneDrawFollowsTheLoadedModelCursorEndToEnd(t *testing.T) {
	c, p := standaloneAnimatedFixture(t)
	m := c.models["standalone-animated"]
	ref := c.texIndex["shared"]
	load := modelTextureLoadKey{kind: modelLoadProjectile, id: "7"}

	// Step the root piece's cursor off frame zero, so "the cursor's frame" and
	// "the first frame" are different answers and the assertion can tell a
	// followed cursor from a fallback.
	c.modelTextures.players = []phase7Stepper{c.modelTextures.bindings[modelTexturePrimitiveKey{load: load, piece: 0, primitive: 0}]}
	c.modelTextures.StepPhase7()
	want := c.modelTextures.animatedFrame(m.compiled, 0, 0, ref)
	if want == ref.entry.Frames[0].Frame {
		t.Fatal("fixture cursor did not leave the first frame; the test cannot discriminate")
	}

	parent, _ := render.BuildProjectileModelPiecesInto(m.compiled, p, 0, c.borrowProjectileScratch(), c.borrowProjectileScratch())
	if parent == nil {
		t.Fatal("the standalone builder produced no draw")
	}
	if parent.SourceModel != m.compiled {
		t.Fatalf("standalone draw carried source model %p, want the loaded model %p", parent.SourceModel, m.compiled)
	}
	polys := c.collectDrawPolys(parent, teamColor{}, 1, modelCursorProjectile)
	if len(polys) == 0 {
		t.Fatal("the standalone draw produced no faces")
	}
	for i := range polys {
		if polys[i].frame != want {
			t.Fatalf("face %d read frame %p, want the loaded model's cursor frame %p (a fallback to the first frame is %p)",
				i, polys[i].frame, want, ref.entry.Frames[0].Frame)
		}
	}
}
