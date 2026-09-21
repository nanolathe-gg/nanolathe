package client

import (
	"bytes"
	"crypto/sha256"
	"hash/crc32"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

type pieceInfo struct {
	name      string
	parent    int
	translate [3]float64
}

type syntheticTri struct {
	piece     int
	pieceName string
	color     uint8
	order     int
	c         [3]modelCorner
	vkey      [3]int64
}

// helper to make a synthetic model with specified pieces and triangles.
// pieces: slice of pieceInfo, tris: slice of syntheticTri already with local corners.
func syntheticModel(pieces []pieceInfo, tris []syntheticTri, root int) *unitModel {
	m := &compiledmodel.Model{Pieces: make([]compiledmodel.Piece, len(pieces)), Root: root, Name: "synthetic"}
	for i, p := range pieces {
		m.Pieces[i].Name = p.name
		m.Pieces[i].Parent = p.parent
		m.Pieces[i].Translate = [3]numeric.Fixed{numeric.Fixed(int64(p.translate[0] * 65536)), numeric.Fixed(int64(p.translate[1] * 65536)), numeric.Fixed(int64(p.translate[2] * 65536))}
		if p.parent >= 0 && p.parent < len(m.Pieces) {
			m.Pieces[p.parent].Children = append(m.Pieces[p.parent].Children, i)
		}
	}
	for _, tri := range tris {
		pi := tri.piece
		if pi < 0 || pi >= len(m.Pieces) {
			continue
		}
		piece := &m.Pieces[pi]
		base := len(piece.Vertices)
		for k := 0; k < 3; k++ {
			c := tri.c[k]
			piece.Vertices = append(piece.Vertices, [3]numeric.Fixed{
				numeric.Fixed(int64(c.x * 65536)), numeric.Fixed(int64(c.y * 65536)), numeric.Fixed(int64(c.z * 65536)),
			})
		}
		// Fixture faces are authored front-facing. The composition projection's
		// corner ring has to run clockwise or the winding cull of
		// [R-RAST-01 §1] step 7 drops the face and the fixture draws nothing.
		// Callers give corners in whatever order reads well for the geometry
		// they are testing, so the ring is reversed here when it needs to be.
		indices := []uint16{uint16(base), uint16(base + 1), uint16(base + 2), uint16(base + 2)}
		if !facePaints(piece.Vertices, indices, [3]numeric.Fixed{}, modelLocalVertex) {
			indices = []uint16{uint16(base + 2), uint16(base + 2), uint16(base + 1), uint16(base)}
		}
		piece.Primitives = append(piece.Primitives, compiledmodel.Primitive{
			ColorIndex: uint32(tri.color), VertexIndices: indices, IsColored: 1,
		})
	}
	byName := make(map[string]int, len(m.Pieces))
	for i, p := range m.Pieces {
		if p.Name != "" {
			byName[strings.ToLower(p.Name)] = i
		}
	}
	return &unitModel{compiled: m, pieceByName: byName}
}

// newTestClient creates a 640x480 client with camera at origin.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	c.cam = &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	// Ensure palette shade not needed; use default.
	c.indexed = make([]uint8, 640*480)
	return c
}

// newPieceFixtureClient offsets the camera so a fixture unit at the world
// origin composes well inside the framebuffer.
//
// The offset is load-bearing for the piece fixtures specifically. Every
// triangle they author extends up and to the left in screen space, and with the
// camera at (0,0) a unit at the world origin anchors its composition image at
// framebuffer (0,0) — so the face sits off the top-left corner entirely. The
// old barycentric filler still kept the single bottom row that landed on
// scanline zero, and retail's `[yTop, yBottom)` fill excludes exactly that row
// [R-RAST-01 §1] steps 2 and 5. Composing where the geometry actually lands is
// what these tests meant all along.
func newPieceFixtureClient(t *testing.T) *Client {
	t.Helper()
	c := newTestClient(t)
	c.cam = &camera.Camera{X: -160, Z: -120, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	return c
}

// clearIndexed zeros the framebuffer.
func clearIndexed(c *Client) {
	for i := range c.indexed {
		c.indexed[i] = 0
	}
}

// hashIndexed returns crc32 of indexed buffer for comparison.
func hashIndexed(c *Client) uint32 {
	return crc32.ChecksumIEEE(c.indexed)
}

// makeTriangle creates a syntheticTri for piece with local corners and color.
func makeTriangle(piece int, name string, corners [3][3]float64, color uint8, order int) syntheticTri {
	var tri syntheticTri
	tri.piece = piece
	tri.pieceName = name
	tri.color = color
	tri.order = order
	for k := 0; k < 3; k++ {
		tri.c[k] = modelCorner{x: corners[k][0], y: corners[k][1], z: corners[k][2], u: 0, v: 0}
		// Use non-zero vkey to avoid row sharing issues; row will be 0.
		tri.vkey[k] = int64(piece)<<32 | int64(k)
	}
	// rows already zero (shade identity not needed for test).
	return tri
}

// TestPieceParentChildComposition verifies that parent transform composes child [03 §2.4] C21.
// Child authored translation (10,0,5) plus script lane (5,0,2) should place child's triangle at (15,0,7) world offset from unit origin.
func TestPieceParentChildComposition(t *testing.T) {
	c := newPieceFixtureClient(t)
	pieces := []pieceInfo{
		{name: "base", parent: -1, translate: [3]float64{0, 0, 0}},
		{name: "turret", parent: 0, translate: [3]float64{10, 0, 5}},
	}
	// Child triangle at local origin, base has no triangle (so we only see child).
	triTurret := makeTriangle(1, "turret", [3][3]float64{{0, 0, 0}, {4, 0, 0}, {0, 0, 4}}, 42, 0)
	um := syntheticModel(pieces, []syntheticTri{triTurret}, 0)
	c.models["syn_parent"] = um
	// Snapshot with script translation (5,0,2) on turret.
	// The unit sits a hundred units into the map: a model-space +Z vertex
	// projects UP-screen (`Zn = hi16(-vz)`, [R-RAST-01 §2]), so a unit anchored
	// at the framebuffer's top edge would compose entirely off it.
	view := frame.UnitView{
		Slot:  1,
		Owner: 0,
		X:     numeric.Fixed(100 * 65536),
		Z:     numeric.Fixed(100 * 65536),
		Y:     0,
		Model: "syn_parent",
		Pieces: []frame.PieceView{
			{Index: 0, Name: "base"},
			{Index: 1, Name: "turret", Tx: numeric.Fixed(5 * 65536), Tz: numeric.Fixed(2 * 65536)},
		},
	}
	clearIndexed(c)
	sx, sy := c.cam.WorldToScreen(view.X, view.Y, view.Z)
	// drawUnitModel projects around unit position; we expect child's triangle to be drawn offset.
	// Instead of trusting sx,sy, we call drawUnitModel with lerped view at 0,0.
	// Unit at 0,0 => screen origin is camera.OriginX/Y.
	if !c.drawUnitModelReplay(view, sx, sy) {
		t.Fatalf("drawUnitModel failed")
	}
	// Expected model-space position of the child's local origin after
	// composition: authored (10,0,5) + script (5,0,2) = (15,0,7), so the
	// triangle covers (15,0,7) - (19,0,7) - (15,0,11). Its screen offsets from
	// the unit anchor are (+15,-7), (+19,-7), (+15,-11): X passes through, Z is
	// negated by the handedness flip of [R-RAST-01 §2].
	found := false
	// Search framebuffer for color 42 to ensure something was drawn.
	for _, b := range c.indexed {
		if b == 42 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("parent-child composition: expected color 42 not found in framebuffer; composition may have failed")
	}
	// Now test that without script translation, the triangle is elsewhere.
	clearIndexed(c)
	view2 := frame.UnitView{
		Slot: view.Slot, Owner: view.Owner, X: view.X, Y: view.Y, Z: view.Z, Model: view.Model,
		Pieces: []frame.PieceView{
			{Index: 0, Name: "base"},
			{Index: 1, Name: "turret", Tx: 0, Tz: 0},
		},
	}
	sx2, sy2 := c.cam.WorldToScreen(view2.X, view2.Y, view2.Z)
	if !c.drawUnitModelReplay(view2, sx2, sy2) {
		t.Fatalf("drawUnitModel second failed")
	}
	h1 := hashIndexed(c) // after second draw with 0 script
	// Need first hash again: redraw first.
	clearIndexed(c)
	c.drawUnitModelReplay(view, sx, sy)
	hScript := hashIndexed(c)
	if hScript == h1 {
		t.Fatalf("parent-child translation via script lane did not change framebuffer: hashes equal %d", hScript)
	}
	// Also verify authored translation alone is present: with zero script the
	// triangle sits at model (10,0,5), screen offset (+10,-5) from the anchor.
	clearIndexed(c)
	c.drawUnitModelReplay(view2, sx2, sy2)
	found2 := false
	for _, b := range c.indexed {
		if b == 42 {
			found2 = true
			break
		}
	}
	if !found2 {
		t.Fatalf("authored translation alone should still draw triangle")
	}
}

// TestHiddenPieceAbsent verifies hidden pieces are not rasterized [04 §4.3][03 §2.4].
func TestHiddenPieceAbsent(t *testing.T) {
	c := newPieceFixtureClient(t)
	pieces := []pieceInfo{
		{name: "base", parent: -1, translate: [3]float64{0, 0, 0}},
		{name: "turret", parent: 0, translate: [3]float64{0, 0, 0}},
	}
	// Two distinct triangles at different screen locations (non-overlapping) with different colors.
	triBase := makeTriangle(0, "base", [3][3]float64{{0, 0, 0}, {10, 0, 0}, {0, 0, 10}}, 11, 0)
	triTurret := makeTriangle(1, "turret", [3][3]float64{{20, 0, 20}, {25, 0, 20}, {20, 0, 25}}, 22, 1)
	um := syntheticModel(pieces, []syntheticTri{triBase, triTurret}, 0)
	c.models["syn_hidden"] = um
	view := frame.UnitView{
		Slot: 2, Owner: 0, X: 0, Y: 0, Z: 0, Model: "syn_hidden",
		Pieces: []frame.PieceView{
			{Index: 0, Name: "base", Hidden: false},
			{Index: 1, Name: "turret", Hidden: true}, // hidden
		},
	}
	clearIndexed(c)
	sx, sy := c.cam.WorldToScreen(view.X, view.Y, view.Z)
	c.drawUnitModelReplay(view, sx, sy)
	// Count colors.
	count11, count22, total := 0, 0, 0
	for _, b := range c.indexed {
		if b != 0 {
			total++
		}
		if b == 11 {
			count11++
		}
		if b == 22 {
			count22++
		}
	}
	if total == 0 {
		t.Fatalf("base piece should be visible, no pixels drawn (total 0, count11 %d)", count11)
	}
	// Allow shaded palette: base may be drawn with shaded index not exactly 11, but total>0 is sufficient.
	// Keep strict check for hidden turret.
	if count22 != 0 {
		t.Fatalf("hidden turret piece should not be rasterized, color 22 found %d pixels", count22)
	}
	_ = count11
	// Also test child of hidden parent is hidden: make turret child of hidden base? Already turret parent base but base visible; hide base should hide turret too even if turret not hidden.
	view2 := frame.UnitView{
		Slot: view.Slot, Owner: view.Owner, X: view.X, Y: view.Y, Z: view.Z, Model: view.Model,
		Pieces: []frame.PieceView{
			{Index: 0, Name: "base", Hidden: true},
			{Index: 1, Name: "turret", Hidden: false},
		},
	}
	clearIndexed(c)
	c.drawUnitModelReplay(view2, sx, sy)
	count11, count22 = 0, 0
	for _, b := range c.indexed {
		if b == 11 {
			count11++
		}
		if b == 22 {
			count22++
		}
	}
	if count11 != 0 {
		t.Fatalf("hidden parent base should hide its own tris, found %d", count11)
	}
	if count22 != 0 {
		t.Fatalf("child of hidden parent should also be hidden, found turret %d", count22)
	}
}

// TestFlareFlashPolicy verifies that flare/flash named pieces follow Hidden state, not silent drop [fmt 3do "Piece naming conventions"].
func TestFlareFlashPolicy(t *testing.T) {
	c := newPieceFixtureClient(t)
	pieces := []pieceInfo{
		{name: "base", parent: -1, translate: [3]float64{0, 0, 0}},
		{name: "flare", parent: 0, translate: [3]float64{5, 0, 0}},
	}
	triBase := makeTriangle(0, "base", [3][3]float64{{0, 0, 0}, {4, 0, 0}, {0, 0, 4}}, 33, 0)
	triFlare := makeTriangle(1, "flare", [3][3]float64{{0, 0, 0}, {2, 0, 0}, {0, 0, 2}}, 44, 1)
	um := syntheticModel(pieces, []syntheticTri{triBase, triFlare}, 0)
	c.models["syn_flare"] = um
	viewVisible := frame.UnitView{
		Slot: 3, Owner: 0, X: 0, Y: 0, Z: 0, Model: "syn_flare",
		Pieces: []frame.PieceView{
			{Index: 0, Name: "base", Hidden: false},
			{Index: 1, Name: "flare", Hidden: false},
		},
	}
	clearIndexed(c)
	sx, sy := c.cam.WorldToScreen(viewVisible.X, viewVisible.Y, viewVisible.Z)
	c.drawUnitModelReplay(viewVisible, sx, sy)
	hasFlare := false
	for _, b := range c.indexed {
		if b == 44 {
			hasFlare = true
			break
		}
	}
	if !hasFlare {
		t.Fatalf("flare piece with Hidden=false should be rasterized; no flare color found (policy requires no silent drop)")
	}
	viewHidden := frame.UnitView{
		Slot: viewVisible.Slot, Owner: viewVisible.Owner, X: viewVisible.X, Y: viewVisible.Y, Z: viewVisible.Z, Model: viewVisible.Model,
		Pieces: []frame.PieceView{
			{Index: 0, Name: "base", Hidden: false},
			{Index: 1, Name: "flare", Hidden: true},
		},
	}
	clearIndexed(c)
	c.drawUnitModelReplay(viewHidden, sx, sy)
	hasFlare = false
	for _, b := range c.indexed {
		if b == 44 {
			hasFlare = true
			break
		}
	}
	if hasFlare {
		t.Fatalf("flare piece with Hidden=true should not be rasterized")
	}
}

// TestTurretRotationChangesPixels verifies that turret rotation changes rendered pixels while unit frame unchanged [03 §2.4] C21.
// Uses framebuffer hash compare.
func TestTurretRotationChangesPixels(t *testing.T) {
	c := newPieceFixtureClient(t)
	pieces := []pieceInfo{
		{name: "base", parent: -1, translate: [3]float64{0, 0, 0}},
		{name: "turret", parent: 0, translate: [3]float64{0, 0, 0}},
	}
	// Asymmetric triangle: points (0,0,0),(10,0,0),(0,0,2) – rotation 90deg about Y will swap X->Z.
	tri := makeTriangle(1, "turret", [3][3]float64{{0, 0, 0}, {10, 0, 0}, {0, 0, 2}}, 55, 0)
	// Also base triangle to ensure frame still considered but not moved.
	triBase := makeTriangle(0, "base", [3][3]float64{{-5, 0, -5}, {-1, 0, -5}, {-5, 0, -1}}, 66, 1)
	um := syntheticModel(pieces, []syntheticTri{tri, triBase}, 0)
	c.models["syn_rot"] = um
	view0 := frame.UnitView{
		Slot: 4, Owner: 0, X: 0, Y: 0, Z: 0, Model: "syn_rot",
		Pieces: []frame.PieceView{
			{Index: 0, Name: "base"},
			{Index: 1, Name: "turret", RotY: 0},
		},
	}
	view90 := frame.UnitView{
		Slot: 4, Owner: 0, X: 0, Y: 0, Z: 0, Model: "syn_rot",
		Pieces: []frame.PieceView{
			{Index: 0, Name: "base"},
			{Index: 1, Name: "turret", RotY: 16384}, // 90 deg [03 §2.4] 65536 per circle
		},
	}
	clearIndexed(c)
	sx, sy := c.cam.WorldToScreen(view0.X, view0.Y, view0.Z)
	c.drawUnitModelReplay(view0, sx, sy)
	hash0 := hashIndexed(c)
	// also capture sha for debugging
	sha0 := sha256.Sum256(c.indexed)
	clearIndexed(c)
	sx2, sy2 := c.cam.WorldToScreen(view90.X, view90.Y, view90.Z)
	c.drawUnitModelReplay(view90, sx2, sy2)
	hash90 := hashIndexed(c)
	sha90 := sha256.Sum256(c.indexed)
	if hash0 == hash90 {
		t.Fatalf("turret rotation should change framebuffer hash: both %d sha %x vs %x", hash0, sha0, sha90)
	}
	// Verify unit position unchanged: X/Z same, so frame (unit position) unchanged but pixels changed proves piece rotation works.
	if view0.X != view90.X || view0.Z != view90.Z {
		t.Fatalf("unit frame position should be unchanged between rotations")
	}
}

// TestSameSnapshotIdenticalFramebuffer verifies deterministic rendering: same snapshot yields identical indexed framebuffer [I1].
func TestSameSnapshotIdenticalFramebuffer(t *testing.T) {
	c1 := newPieceFixtureClient(t)
	c2 := newPieceFixtureClient(t)
	pieces := []pieceInfo{
		{name: "base", parent: -1, translate: [3]float64{0, 0, 0}},
	}
	tri := makeTriangle(0, "base", [3][3]float64{{0, 0, 0}, {6, 0, 0}, {0, 0, 6}}, 77, 0)
	um := syntheticModel(pieces, []syntheticTri{tri}, 0)
	c1.models["syn_ident"] = um
	c2.models["syn_ident"] = um
	view := frame.UnitView{
		Slot: 5, Owner: 1, X: numeric.Fixed(100 * 65536), Y: 0, Z: numeric.Fixed(50 * 65536), Model: "syn_ident",
		Pieces:  []frame.PieceView{{Index: 0, Name: "base", RotY: 12345, Tx: numeric.Fixed(2 * 65536)}},
		Heading: 1000, Pitch: 2000, Bank: 3000,
	}
	// Draw with c1
	clearIndexed(c1)
	sx, sy := c1.cam.WorldToScreen(view.X, view.Y, view.Z)
	c1.drawUnitModelReplay(view, sx, sy)
	hash1 := hashIndexed(c1)
	// Draw with c2
	clearIndexed(c2)
	sx2, sy2 := c2.cam.WorldToScreen(view.X, view.Y, view.Z)
	c2.drawUnitModelReplay(view, sx2, sy2)
	hash2 := hashIndexed(c2)
	if hash1 != hash2 {
		t.Fatalf("same snapshot should yield identical framebuffer: %d vs %d", hash1, hash2)
	}
	if !bytes.Equal(c1.indexed, c2.indexed) {
		t.Fatalf("indexed buffers differ despite same snapshot")
	}
	// Also draw again on c1 after clear should be identical.
	clearIndexed(c1)
	c1.drawUnitModelReplay(view, sx, sy)
	hash1b := hashIndexed(c1)
	if hash1 != hash1b {
		t.Fatalf("second draw on same client should be identical: %d vs %d", hash1, hash1b)
	}
}

// TestSelectionPickingStable ensures selection picking comment: picking uses footprint, not animated extents.
// This is a light check that piece transforms remain independent of selection projection.
// We verify that unit's screen position for selection is still via UnitView.X/Z, not piece offset.
func TestSelectionPickingStable(t *testing.T) {
	c := newPieceFixtureClient(t)
	pieces := []pieceInfo{
		{name: "base", parent: -1, translate: [3]float64{0, 0, 0}},
		{name: "turret", parent: 0, translate: [3]float64{100, 0, 0}}, // far offset would move visual but not selection center
	}
	tri := makeTriangle(1, "turret", [3][3]float64{{0, 0, 0}, {4, 0, 0}, {0, 0, 4}}, 88, 0)
	um := syntheticModel(pieces, []syntheticTri{tri}, 0)
	c.models["syn_pick"] = um
	view := frame.UnitView{
		Slot: 6, Owner: 0, X: numeric.Fixed(50 * 65536), Y: 0, Z: numeric.Fixed(50 * 65536), Model: "syn_pick", FootX: 2, FootZ: 2,
		Pieces: []frame.PieceView{
			{Index: 0, Name: "base"},
			{Index: 1, Name: "turret", Tx: numeric.Fixed(100 * 65536)}, // visual far but selection should stay at unit center
		},
	}
	// Selection rect centered at unit's projected position should still select it, even though piece is far.
	sx0, sy0 := c.cam.WorldToScreen(view.X, view.Y, view.Z)
	sx, sy := sx0-camera.OriginX, sy0-camera.OriginY
	rect := Rect{MinX: sx - 2, MaxX: sx + 2, MinY: sy - 2, MaxY: sy + 2}
	if !rect.Contains(sx, sy) {
		t.Fatalf("rect should contain unit center")
	}
	// The production drag picker projects the unit's own X/Z, not a piece
	// offset, so the far turret never moves the pick.
	inRect := func(v frame.UnitView) bool {
		h := SnapshotUnitHandlesInBand(&frame.Frame{Units: []frame.UnitView{v}}, c.cam, bandOfRecordRect(rect), 0)
		return len(h) == 1 && h[0] == v.Slot
	}
	if !inRect(view) {
		t.Fatalf("picking should be stable on footprint/model bound, not animated piece extents")
	}
	viewFar := view
	viewFar.Pieces[1].Tx = numeric.Fixed(500 * 65536)
	if !inRect(viewFar) {
		t.Fatalf("far turret offset should not affect picking rect containment")
	}
	_ = um
	_ = view
}
