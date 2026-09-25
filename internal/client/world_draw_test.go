package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// px converts whole map pixels to the 16.16 world scale [03 §2.1].
func px(v int32) numeric.Fixed { return numeric.Fixed(int64(v) << 16) }

// flatModel registers a single flat triangle of the given colour whose screen
// footprint is a right triangle reaching `size` pixels right of and below the
// anchor: a model +X vertex projects right and a model -Z vertex projects down
// [R-RAST-01 §2].
func flatModel(c *Client, name string, size float64, colour uint8) {
	c.models[name] = syntheticModel(
		[]pieceInfo{{name: "root", parent: -1}},
		[]syntheticTri{makeTriangle(0, "root", [3][3]float64{{0, 0, 0}, {size, 0, 0}, {0, 0, -size}}, colour, 0)},
		0,
	)
}

// TestUnitRowKeyIsWorldZNotScreenY locks the two consequences [03 R-RAST-01 §7]
// spells out for the pass split. The bucket key is world Z in plot rows, so an
// airborne unit does not sort into an earlier row as it climbs; and pass B runs
// after the projectile and effect strips, so a parked/attached unit (mirror 0)
// and an airborne unit (mirror 2) both paint after a mode-1 unit even when
// their rows are earlier than its.
//
// The pass-B unit here is a PARKED unit, not a structure: a structure's mirror
// is 1 and it belongs to pass A, per the 2026-08-30 correction under
// [03 R-RAST-01 §7]. The test always set MoverMode by hand and so still held
// when that partition was corrected; only its labels were wrong.
func TestUnitRowKeyIsWorldZNotScreenY(t *testing.T) {
	c := newTestClient(t)
	flatModel(c, "m_ground", 100, 40)
	flatModel(c, "m_parked", 40, 20)
	flatModel(c, "m_air", 40, 30)

	// The camera sits at the origin, so each unit's bucket row is z/16+16 and
	// its projected anchor carries the half-height shear -y/2 [03 §2.5]
	// [03 R-RAST-01 §7]. The two pass-B units are placed sixteen pixels north
	// of the grounded one, one whole plot row earlier.
	ground := frame.UnitView{Slot: 1, Owner: 0, X: px(200), Z: px(160), MoverMode: 1, Model: "m_ground"}
	parked := frame.UnitView{Slot: 2, Owner: 0, X: px(210), Z: px(144), MoverMode: 0, Model: "m_parked"}
	air := frame.UnitView{Slot: 3, Owner: 0, X: px(250), Z: px(144), Y: px(8), MoverMode: 2, Model: "m_air"}

	if got, want := unitBucketRow(ground.Z, 0), int32(26); got != want {
		t.Fatalf("grounded row = %d, want %d", got, want)
	}
	for _, u := range []frame.UnitView{parked, air} {
		if row := unitBucketRow(u.Z, 0); row >= unitBucketRow(ground.Z, 0) {
			t.Fatalf("slot %d row %d is not earlier than the grounded unit's row", u.Slot, row)
		}
	}
	// The airborne unit's projected screen Y is above the grounded unit's:
	// keying on it is exactly the defect this pass split removes.
	_, airY := c.cam.WorldToScreen(air.X, air.Y, air.Z)
	_, groundY := c.cam.WorldToScreen(ground.X, ground.Y, ground.Z)
	if airY >= groundY {
		t.Fatalf("scene does not exercise the shear: air screen y %d, ground %d", airY, groundY)
	}

	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Units:     []frame.UnitView{ground, parked, air},
	}
	clearIndexed(c)
	c.drawWorldPass(cur, true)
	// Pass A drew the grounded unit alone; the other two wait for pass B.
	if len(c.selectionChrome) != 1 || c.selectionChrome[0].view.Slot != pool.Handle(1) {
		t.Fatalf("pass A drew %d units, want the grounded unit alone", len(c.selectionChrome))
	}
	c.drawProjectiles(cur)
	c.drawEffects(cur)
	c.drawWorldPassB(cur, true)
	c.replayForTest()

	want := []pool.Handle{1, 2, 3}
	if len(c.selectionChrome) != len(want) {
		t.Fatalf("drawn units = %d, want %d", len(c.selectionChrome), len(want))
	}
	for i, w := range want {
		if got := c.selectionChrome[i].view.Slot; got != w {
			t.Fatalf("draw order[%d] = slot %d, want %d", i, got, w)
		}
	}
	// Where each of the two pass-B units overlaps the grounded unit, its own
	// colour survives: it painted last.
	if got := c.indexed[165*c.width+212]; got != 20 {
		t.Fatalf("parked unit over mode-1 unit: pixel = %d, want the parked unit's 20", got)
	}
	if got := c.indexed[165*c.width+252]; got != 30 {
		t.Fatalf("airborne unit over grounded unit: pixel = %d, want the aircraft's 30", got)
	}
}

// TestFeatureRowsInterleaveWithGroundedUnits locks the feature consequences of
// [03 R-RAST-01 §6]: a short feature paints under every unit because it is
// drawn in the first feature pass, and a tall feature paints over the grounded
// units of its own row because pass A draws that row's units first and its
// deferred tall features second.
func TestFeatureRowsInterleaveWithGroundedUnits(t *testing.T) {
	c := newTestClient(t)
	flatModel(c, "m_ground", 100, 40)
	flatModel(c, "m_short", 40, 50)
	flatModel(c, "m_tall", 40, 60)

	ground := frame.UnitView{Slot: 1, Owner: 0, X: px(430), Z: px(160), MoverMode: 1, Model: "m_ground"}
	short := frame.FeatureView{CX: 26, CZ: 10, X: px(430), Z: px(160), Height: 5, Model: "m_short"}
	tall := frame.FeatureView{CX: 29, CZ: 10, X: px(470), Z: px(160), Height: 20, Model: "m_tall"}

	// The feature row is measured from the window's first row, the unit row
	// from the camera; here both name row 26 [03 R-RAST-01 §6]
	// [03 R-RAST-01 §7].
	if got, want := featureBucketRow(tall.CZ, 0), unitBucketRow(ground.Z, 0); got != want {
		t.Fatalf("tall feature row = %d, want the grounded unit's row %d", got, want)
	}

	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Fog:       frame.FogView{Valid: true, W: 32, H: 32, Ch0: make([]byte, 32*32), Ch1: make([]byte, 32*32)},
		Units:     []frame.UnitView{ground},
		Features:  []frame.FeatureView{short, tall},
	}
	clearIndexed(c)
	c.drawFeaturePass(cur, true)
	c.drawWorldPass(cur, true)
	c.replayForTest()

	if got := c.indexed[165*c.width+432]; got != 40 {
		t.Fatalf("grounded unit over short feature: pixel = %d, want the unit's 40", got)
	}
	if got := c.indexed[165*c.width+472]; got != 60 {
		t.Fatalf("tall feature over the grounded units of its own row: pixel = %d, want the feature's 60", got)
	}
}

// TestFrameWindowClipsToTheMapAndZero locks the window arithmetic of
// [03 R-RAST-01 §6]: the origin sixteen rows above and ten columns left of the
// camera, the reduction of the part that falls below zero, and the exclusion of
// the map's last row and column.
func TestFrameWindowClipsToTheMapAndZero(t *testing.T) {
	if first, n := clipWindowAxis(-16, 62, 0); first != 0 || n != 46 {
		t.Fatalf("below-zero reduction = (%d,%d), want (0,46)", first, n)
	}
	if first, n := clipWindowAxis(-16, 62, 30); first != 0 || n != 29 {
		t.Fatalf("reduction then map clip = (%d,%d), want (0,29)", first, n)
	}
	if first, n := clipWindowAxis(200, 62, 240); first != 200 || n != 39 {
		t.Fatalf("map clip excludes the last row: got (%d,%d), want (200,39)", first, n)
	}

	c := newTestClient(t)
	c.cam.Z = 24
	win := c.worldWindow()
	// 24/16 truncates to 1, so the window's first row is -15 while a unit at
	// world Z 24 keys to row 16: the two expressions differ by one when the
	// camera is not on a cell boundary, which is why they are kept apart.
	if win.rowBase != -15 {
		t.Fatalf("window first row = %d, want -15", win.rowBase)
	}
	if got := unitBucketRow(px(24), c.cam.Z); got != 16 {
		t.Fatalf("unit row at the camera = %d, want 16", got)
	}
	if got := featureBucketRow(1, c.cam.Z); got != 16 {
		t.Fatalf("feature row of the camera's own cell = %d, want 16", got)
	}
	// A unit outside the bucket rows is not drawn this frame.
	if row := unitBucketRow(px(24+16*win.bucketRows), c.cam.Z); row < win.bucketRows {
		t.Fatalf("row %d should fall outside the %d bucket rows", row, win.bucketRows)
	}
}

// flatModelNorthWest is flatModel's mirror: its screen footprint is a right
// triangle reaching `size` pixels left of and above the anchor, so a feature
// anchored past the right edge of the viewport still has pixels inside it
// [R-RAST-01 §2].
func flatModelNorthWest(c *Client, name string, size float64, colour uint8) {
	c.models[name] = syntheticModel(
		[]pieceInfo{{name: "root", parent: -1}},
		[]syntheticTri{makeTriangle(0, "root", [3][3]float64{{0, 0, 0}, {-size, 0, 0}, {0, 0, size}}, colour, 0)},
		0,
	)
}

// TestFeaturePassesDoNotReadFog locks the gate of [03 R-RAST-01 §6]: neither
// feature pass reads the explored mask or the fog grids. A tree standing on a
// never-seen plot cell is drawn, and the fog overlay composed after strip 9 is
// what hides it — which is why retail shows the top of a tree poking out of the
// black instead of popping the whole sprite in when the cell is first explored.
//
// This is the PT3-10 regression: the passes previously culled a feature whose
// anchor tile read Ch0 == 15, an edge that does not line up with the 32-pixel
// fog blocks the overlay actually paints.
func TestFeaturePassesDoNotReadFog(t *testing.T) {
	c := newTestClient(t)
	flatModel(c, "m_short", 40, 50)
	flatModel(c, "m_tall", 40, 60)

	short := frame.FeatureView{CX: 26, CZ: 10, X: px(430), Z: px(160), Height: 5, Model: "m_short"}
	tall := frame.FeatureView{CX: 29, CZ: 10, X: px(470), Z: px(160), Height: 20, Model: "m_tall"}

	// Every tile never seen: Ch0 == 15 is the solid-dark short-circuit
	// [03 §3.3], the state the old gate culled on.
	dark := make([]byte, 32*32)
	for i := range dark {
		dark[i] = 15
	}
	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Fog:       frame.FogView{Valid: true, W: 32, H: 32, Ch0: dark, Ch1: make([]byte, 32*32)},
		Features:  []frame.FeatureView{short, tall},
	}
	// Both anchors sit on never-seen fog tiles (fog is per 2x2-cell visibility
	// tile, so the tile index is cell>>1) — the state the old gate culled on.
	for _, f := range []frame.FeatureView{short, tall} {
		if cur.Fog.Ch0[int((f.CZ>>1)*cur.Fog.W+(f.CX>>1))] != 15 {
			t.Fatal("scene does not exercise the never-seen state both features must ignore")
		}
	}

	clearIndexed(c)
	c.drawFeaturePass(cur, true)
	c.drawWorldPass(cur, true)
	c.replayForTest()

	if got := c.indexed[165*c.width+432]; got != 50 {
		t.Fatalf("short feature on a never-seen cell: pixel = %d, want the feature's 50", got)
	}
	if got := c.indexed[165*c.width+472]; got != 60 {
		t.Fatalf("tall feature on a never-seen cell: pixel = %d, want the feature's 60", got)
	}
}

// TestFeatureWindowAdmitsSpriteOverhang locks the frame window's margin
// [03 R-RAST-01 §6]: the column count is the viewport in whole plot cells plus
// twelve, and the origin is ten columns left of the camera, so the window runs
// two columns past the right edge of the view. A feature whose anchor cell is
// entirely off-screen but whose sprite reaches back into the viewport is drawn.
func TestFeatureWindowAdmitsSpriteOverhang(t *testing.T) {
	c := newTestClient(t)
	flatModelNorthWest(c, "m_overhang", 120, 70)

	win := c.worldWindow()
	// Camera at the origin, 640×480: columns 0..41 and rows 0..45 after the
	// below-zero reduction. Column 40 begins at map pixel 640 — one past the
	// last drawn column — and is still admitted; column 42 is not.
	lastVisibleCol := c.cam.ViewW/cellPixels - 1
	if !win.admitsCell(lastVisibleCol+1, 10) {
		t.Fatalf("column %d, the first fully off-screen one, must stay in the window", lastVisibleCol+1)
	}
	if win.admitsCell(win.firstCol+win.colCount, 10) {
		t.Fatalf("column %d is past the window and must be culled", win.firstCol+win.colCount)
	}

	f := frame.FeatureView{CX: lastVisibleCol + 1, CZ: 10, X: px(660), Z: px(160), Height: 5, Model: "m_overhang"}
	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Fog:       frame.FogView{Valid: true, W: 32, H: 32, Ch0: make([]byte, 32*32), Ch1: make([]byte, 32*32)},
		Features:  []frame.FeatureView{f},
	}
	clearIndexed(c)
	c.drawFeaturePass(cur, true)
	c.replayForTest()

	painted := 0
	for i := range c.indexed {
		if c.indexed[i] == 70 {
			painted++
		}
	}
	if painted == 0 {
		t.Fatal("a sprite anchored past the right edge painted nothing inside the viewport")
	}
}

// TestFeatureVisibilityGate locks the four established feature admissions of
// [03 R-RAST-01 §6][03 §5.1.5]: unflagged definitions are unconditional,
// flagged definitions accept the local placer, then either of the two
// footprint corners in committed visibility, and otherwise are denied.
func TestFeatureVisibilityGate(t *testing.T) {
	const local = uint8(0)
	base := frame.FeatureView{
		Owner: 1, OwnerKnown: true, CX: 2, CZ: 2, FootX: 2, FootZ: 2,
		Y: 0, NoDrawUnderGray: true,
	}
	invalid := &frame.Frame{Selection: frame.SelectionView{LocalPlayer: local}}
	if !featureVisibleForFrame(invalid, frame.FeatureView{NoDrawUnderGray: false}) {
		t.Fatal("unflagged feature was denied without visibility data")
	}
	if featureVisibleForFrame(invalid, base) {
		t.Fatal("flagged foreign feature bypassed missing visibility data")
	}

	placer := base
	placer.Owner = local
	if !featureVisibleForFrame(invalid, placer) {
		t.Fatal("flagged local-placer feature was denied")
	}

	// The anchor corner (2,2) maps to visibility tile (1,1), while the
	// footprint-displaced corner (4,4) maps to (2,2). Only the latter is lit,
	// proving this is the two-corner predicate rather than an anchor-only gate.
	los := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: local},
		Visibility: frame.VisibilityView{
			W: 4, H: 4, Valid: true, CoverageBytes: true,
			Visible: []uint8{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0},
		},
	}
	if !featureVisibleForFrame(los, base) {
		t.Fatal("flagged feature with visible displaced corner was denied")
	}
	los.Visibility.Visible[10] = 0
	if featureVisibleForFrame(los, base) {
		t.Fatal("flagged feature with both LOS corners denied was admitted")
	}
}

// TestMapOwnedFeatureDrawsWithoutLOSInTallPassOnly locks the ProTA 4.8
// renderer hook (research/extensions/prota-engine.md "Map-owned features
// drawn without line of sight"): a nodrawundergray feature whose placer selector is 11 is
// drawn by the second (tall-feature) pass with no LOS, while a short one with
// the same selector keeps the first pass's gate and is skipped, and a tall
// retail map feature (selector 10) still needs LOS.
func TestMapOwnedFeatureDrawsWithoutLOSInTallPassOnly(t *testing.T) {
	c := newTestClient(t)
	flatModel(c, "m_short", 40, 50)
	flatModel(c, "m_tall", 40, 60)

	owned := world.MapOwnedFeaturePlacer
	short := frame.FeatureView{CX: 26, CZ: 10, X: px(430), Z: px(160), Height: 5, Model: "m_short",
		Owner: owned, OwnerKnown: true, NoDrawUnderGray: true, FootX: 1, FootZ: 1}
	tall := frame.FeatureView{CX: 29, CZ: 10, X: px(470), Z: px(160), Height: 20, Model: "m_tall",
		Owner: owned, OwnerKnown: true, NoDrawUnderGray: true, FootX: 1, FootZ: 1}
	noLOS := frame.VisibilityView{W: 32, H: 32, Valid: true, CoverageBytes: true, Visible: make([]uint8, 32*32)}
	cur := &frame.Frame{
		Selection:  frame.SelectionView{LocalPlayer: 0},
		Fog:        frame.FogView{Valid: true, W: 32, H: 32, Ch0: make([]byte, 32*32), Ch1: make([]byte, 32*32)},
		Visibility: noLOS,
		Features:   []frame.FeatureView{short, tall},
	}
	draw := func() {
		clearIndexed(c)
		c.resetListForTest()
		c.drawFeaturePass(cur, true)
		c.drawWorldPass(cur, true)
		c.replayForTest()
	}
	draw()
	if got := c.indexed[165*c.width+472]; got != 60 {
		t.Fatalf("tall owner-11 feature outside LOS: pixel = %d, want the feature's 60", got)
	}
	if got := c.indexed[165*c.width+432]; got == 50 {
		t.Fatal("short owner-11 feature outside LOS was drawn; the hook is on the tall pass only")
	}

	cur.Features[1].Owner = world.TerrainFeaturePlacer
	draw()
	if got := c.indexed[165*c.width+472]; got == 60 {
		t.Fatal("tall selector-10 feature outside LOS was drawn")
	}
}
