package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

func publishProjectileOrientationTick(t *testing.T, c *Client, tick uint32) {
	t.Helper()
	if c == nil || c.buffer == nil {
		t.Fatal("client has no frame buffer")
	}
	c.buffer.BeginWrite()
	if err := c.buffer.Publish(tick); err != nil {
		t.Fatalf("publish tick %d: %v", tick, err)
	}
}

func drawProjectileOrientation(t *testing.T, c *Client, p frame.ProjectileView) (uint32, map[uint8]int) {
	t.Helper()
	clearIndexed(c)
	c.resetListForTest()
	if !c.drawProjectileModel(p) {
		t.Fatal("projectile model did not draw")
	}
	c.replayForTest()
	counts := make(map[uint8]int)
	for _, px := range c.indexed {
		counts[px]++
	}
	return hashIndexed(c), counts
}

// TestProjectileModelUsesCommittedOrientationAndBoundedChildDraw exercises
// the production model entry and raster. The root is one standalone call, the
// first child is the only conditional second call, and a propeller changes that
// child without rotating the body [03 §5.4][03 R-COMP-02 §6][06 R-WFX-01 §4].
func TestProjectileModelUsesCommittedOrientationAndBoundedChildDraw(t *testing.T) {
	c := newPieceFixtureClient(t)
	c.models["projectile-orientation"] = syntheticModel(
		[]pieceInfo{
			{name: "body", parent: -1},
			{name: "propeller", parent: 0},
			{name: "unreached-grandchild", parent: 1},
		},
		[]syntheticTri{
			makeTriangle(0, "body", [3][3]float64{{0, 0, 0}, {12, 0, 0}, {0, 0, 12}}, 11, 0),
			makeTriangle(1, "propeller", [3][3]float64{{30, 0, 0}, {40, 0, 0}, {30, 0, 10}}, 22, 1),
			makeTriangle(2, "unreached-grandchild", [3][3]float64{{60, 0, 0}, {70, 0, 0}, {60, 0, 10}}, 33, 2),
		}, 0)
	p := frame.ProjectileView{
		Model:         "projectile-orientation",
		RenderType:    render.RenderTypeBaseSpriteModel,
		ExpiryTick:    9,
		Propeller:     true,
		PropellerRoll: 0,
		// The model-facing path subtracts a half circle from yaw and pitch.
		// These words make the synthetic body identity-oriented so its authored
		// front-facing triangles remain inside the fixture viewport.
		Yaw:   0x8000,
		Pitch: 0x8000,
	}

	publishProjectileOrientationTick(t, c, 9)
	_, atExpiry := drawProjectileOrientation(t, c, p)
	if atExpiry[11] == 0 || atExpiry[22] != 0 || atExpiry[33] != 0 {
		t.Fatalf("expiry equality colors body/child/grandchild = %d/%d/%d, want body only", atExpiry[11], atExpiry[22], atExpiry[33])
	}

	publishProjectileOrientationTick(t, c, 10)
	// Publish advances monotonically; draw at tick eight with a new deadline.
	p.ExpiryTick = 11
	plainHash, plain := drawProjectileOrientation(t, c, p)
	if plain[11] == 0 || plain[22] == 0 || plain[33] != 0 {
		t.Fatalf("pre-expiry colors body/child/grandchild = %d/%d/%d, want root and first child only", plain[11], plain[22], plain[33])
	}

	// Render type 6 uses its recorded orientation words verbatim, but never
	// takes the type-1 header-child call [03 §5.4][06 R-WFX-01 §4].
	p.RenderType, p.Propeller, p.Yaw, p.Pitch = render.RenderTypeRecordOrientation, false, 0, 0
	_, type6 := drawProjectileOrientation(t, c, p)
	if type6[11] == 0 || type6[22] != 0 || type6[33] != 0 {
		t.Fatalf("type-6 colors body/child/grandchild = %d/%d/%d, want parent only", type6[11], type6[22], type6[33])
	}
	p.RenderType, p.Propeller, p.Yaw, p.Pitch = render.RenderTypeBaseSpriteModel, true, 0x8000, 0x8000
	p.PropellerRoll = 0x4000
	spinHash, spin := drawProjectileOrientation(t, c, p)
	if spinHash == plainHash {
		t.Fatal("committed propeller roll did not change the production framebuffer")
	}
	if spin[11] != plain[11] {
		t.Fatalf("propeller spin changed body pixels from %d to %d", plain[11], spin[11])
	}

	// Meteor uses its independently maintained pitch word, never the ordinary
	// projectile pitch. A distinct visual proves the selected word reaches the
	// same production raster path [06 §6.5][03 §5.2].
	p.Propeller = false
	p.Roll, p.Pitch, p.MeteorPitch = 0, 0x8000, 0x8000
	p.Meteor = true
	meteorFlat, _ := drawProjectileOrientation(t, c, p)
	p.MeteorPitch = 0xc000
	meteorTilt, _ := drawProjectileOrientation(t, c, p)
	if meteorTilt == meteorFlat {
		t.Fatal("meteor pitch word did not change the production framebuffer")
	}
}

// TestProjectileModelCursorKeepsSelectedSourcePiece verifies the detached
// parent and child keep their authored piece identities at the animated-texture
// cursor seam. Both use one texture and primitive index, so renumbering each
// detached draw to piece zero would alias their playback state [03 §5.2].
func TestProjectileModelCursorKeepsSelectedSourcePiece(t *testing.T) {
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
	c.models["projectile-cursors"] = m
	entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{
		{Value: 1, Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{41}}},
		{Value: 1, Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{42}}},
	}}
	c.texIndex["shared"] = texRef{kind: texAnimated, key: "synthetic|shared", entry: entry, frame: entry.Frames[0].Frame}
	publishProjectileOrientationTick(t, c, 10)
	p := frame.ProjectileView{Model: "projectile-cursors", Handle: 77, RenderType: render.RenderTypeBaseSpriteModel, ExpiryTick: 11, Yaw: 0x8000, Pitch: 0x8000}
	if !c.drawProjectileModel(p) {
		t.Fatal("projectile model did not draw")
	}
	parentKey := modelTextureKey{kind: modelCursorProjectile, id: projectilePresentationID(p), tex: "synthetic|shared", piece: 0, primitive: 0}
	childKey := parentKey
	childKey.piece = 1
	parent, child := c.modelPresentation[parentKey], c.modelPresentation[childKey]
	if parent == nil || child == nil || parent == child {
		t.Fatalf("standalone parent/child cursor bindings = %p/%p, want independent source-piece cursors", parent, child)
	}
}
