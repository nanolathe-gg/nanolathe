package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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
// after the projectile and effect strips, so a structure (mover mode 0) and an
// airborne unit (mover mode 2) both paint after a grounded unit (mover mode 1)
// even when their rows are earlier than its. "This is the retail order; it is
// not a bug to fix."
func TestUnitRowKeyIsWorldZNotScreenY(t *testing.T) {
	c := newTestClient(t)
	flatModel(c, "m_ground", 100, 40)
	flatModel(c, "m_structure", 40, 20)
	flatModel(c, "m_air", 40, 30)

	// The camera sits at the origin, so each unit's bucket row is z/16+16 and
	// its projected anchor carries the half-height shear -y/2 [03 §2.5]
	// [03 R-RAST-01 §7]. The two pass-B units are placed sixteen pixels north
	// of the grounded one, one whole plot row earlier.
	ground := frame.UnitView{Slot: 1, Owner: 0, X: px(200), Z: px(160), MoverMode: 1, Model: "m_ground"}
	structure := frame.UnitView{Slot: 2, Owner: 0, X: px(210), Z: px(144), MoverMode: 0, Model: "m_structure"}
	air := frame.UnitView{Slot: 3, Owner: 0, X: px(250), Z: px(144), Y: px(8), MoverMode: 2, Model: "m_air"}

	if got, want := unitBucketRow(ground.Z, 0), int32(26); got != want {
		t.Fatalf("grounded row = %d, want %d", got, want)
	}
	for _, u := range []frame.UnitView{structure, air} {
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
		Units:     []frame.UnitView{ground, structure, air},
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
		t.Fatalf("structure over grounded unit: pixel = %d, want the structure's 20", got)
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
