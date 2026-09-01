package client

import (
	"image"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/headless"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestAttachedChildIndex locks the two properties the per-unit present's
// children step depends on [03 R-RAST-01 §7]: a carried child is reachable
// from its carrier, and a piece-less carry is not — retail's present draws
// only children that follow a real carrier piece, and the hang byte's no-piece
// sentinel reads back negative [04 R-UNIT-06 §3][04 R-FAC-02 §1]. It also
// locks the list order as the carrier's LIFO cargo list.
func TestAttachedChildIndex(t *testing.T) {
	units := []frame.UnitView{
		{Slot: 1},                                // carrier
		{Slot: 2, Carrier: 1, CarriedPiece: 3},   // real hang piece
		{Slot: 3, Carrier: 1, CarriedPiece: 0},   // piece 0 is a real piece
		{Slot: 4, Carrier: 1, CarriedPiece: -1},  // piece-less carry: excluded
		{Slot: 5, Carrier: 5, CarriedPiece: 1},   // self-carry is not a link
		{Slot: 6, Carrier: 900, CarriedPiece: 1}, // carrier not in this frame
	}
	var b worldBuckets
	b.indexChildren(units)

	var got []int
	for i := b.firstChild(1); i >= 0; i = b.nextChild(i) {
		got = append(got, int(b.units[i].Slot))
	}
	// Children link to the front of the list, so the walk is descending slot:
	// the most recently attached child first [04 R-UNIT-06 §3].
	if len(got) != 2 || got[0] != 3 || got[1] != 2 {
		t.Fatalf("carrier 1 children = %v, want [3 2] with the piece-less slot 4 excluded", got)
	}
	if b.firstChild(5) >= 0 {
		t.Fatal("a unit that names itself as its carrier must not be its own child")
	}
	if b.firstChild(2) >= 0 || b.firstChild(0) >= 0 {
		t.Fatal("a unit with no children must have an empty list")
	}

	// The reset clears the whole index; a second frame with no attachments must
	// not inherit the first frame's lists.
	b.reset()
	b.indexChildren([]frame.UnitView{{Slot: 1}, {Slot: 2}})
	if b.firstChild(1) >= 0 {
		t.Fatal("reset left a stale carrier list behind")
	}
}

// TestFactoryNanoframeIsPresentedWithItsFactory is the play-test regression:
// a unit under construction inside a stock Kbot Lab must appear on the lab's
// build plate, not underneath it.
//
// The product hangs from the QueryBuildInfo piece and keeps the grounded mode
// mirror [04 R-FAC-02 §1][04 R-FAC-02 §2], so it is bucketed in pass A on its
// own world-Z plot row — and the plate sits in front of the lab's own origin,
// which on this model is one 16-pixel row earlier. Painted only as its own
// bucket entry, the product is drawn first and the lab's body paints over it.
// Retail's per-unit present draws "the unit and then each attached child"
// [03 R-RAST-01 §7], which is the later present this test requires.
//
// The assertion is a relationship, not a census: composing the same committed
// frame with the carrier link removed must leave strictly fewer of the
// product's own pixels standing.
func TestFactoryNanoframeIsPresentedWithItsFactory(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer fs.Close()
	pal, err := palette.Load(fs)
	if err != nil {
		t.Skipf("palette unavailable: %v", err)
	}
	rng.SeedGlobal(1, 1)
	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioDirectOTA, Map: "Great Divide",
		LocalOwner: -1, SimulationSeed: 1, CRTSeed: 1, FS: fs,
	})
	if err != nil {
		t.Skipf("battle composition unavailable: %v", err)
	}
	sess := composed.Session
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
	}
	labDef, ok := sess.Catalog.Unit("armlab")
	if !ok || labDef == nil {
		t.Skip("retail catalog has no armlab")
	}
	var comX, comZ numeric.Fixed
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == sess.LocalOwner && u.Def != nil &&
			strings.HasSuffix(strings.ToLower(u.Def.UnitName), "com") {
			comX, comZ = u.X, u.Z
			break
		}
	}
	if comX == 0 && comZ == 0 {
		t.Skip("no local commander to place the lab beside")
	}
	// Ten cells clear of the commander: the lab is 6x6 and a factory cannot
	// open its yard while a foreign unit holds a cell the open state selects
	// [04 R-FAC-02 §5].
	const labOffsetCells = 10
	labHandle, err := sess.Units.Create(labDef, sess.LocalOwner,
		comX+numeric.Fixed(int64(labOffsetCells)<<20), 0, comZ)
	if err != nil {
		t.Fatalf("create lab: %v", err)
	}
	lab := sess.Units.Unit(labHandle)
	if lab == nil {
		t.Fatal("lab not in pool")
	}
	if err := sess.Build.RegisterBuildingPlacement(lab); err != nil {
		t.Fatalf("RegisterBuildingPlacement: %v", err)
	}
	if err := construction.QueueFactoryBuild(lab, "armpw", 1, sess.Catalog); err != nil {
		t.Fatalf("QueueFactoryBuild: %v", err)
	}

	c := newTestClient(t)
	c.SetPalette(pal)
	c.SetModelFS(fs)

	var cur *frame.Frame
	var product *frame.UnitView
	for step := int32(31); step <= 600 && product == nil; step++ {
		sess.Step(step)
		cur = sess.Snapshot.Current()
		if cur == nil {
			continue
		}
		for i := range cur.Units {
			u := &cur.Units[i]
			if u.Carrier == labHandle && u.BuildRemaining > 0 {
				product = u
			}
		}
	}
	if product == nil {
		t.Fatal("the lab never carried a nanoframe; the factory never allocated its product")
	}
	labView := (*frame.UnitView)(nil)
	for i := range cur.Units {
		if cur.Units[i].Slot == labHandle {
			labView = &cur.Units[i]
		}
	}
	if labView == nil {
		t.Fatal("the lab is not in the committed frame")
	}

	camX := int32(int64(labView.X)>>16) - 320
	camZ := int32(int64(labView.Z)>>16) - 240
	c.cam = &camera.Camera{X: camX, Z: camZ, ViewW: 640, ViewH: 480,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}
	c.frameTick = cur.Tick

	productRow := unitBucketRow(product.Z, camZ)
	labRow := unitBucketRow(labView.Z, camZ)
	if productRow >= labRow {
		t.Fatalf("the nanoframe's plot row %d is not earlier than the lab's %d, so this fixture no longer reproduces the occlusion it exists to lock", productRow, labRow)
	}

	// The product's own silhouette, composed alone.
	alone := c.composeUnits(t, cur, []frame.UnitView{*product})
	// The published frame, and the same frame with the carrier link cut.
	full := c.composeUnits(t, cur, []frame.UnitView{*labView, *product})
	orphan := *product
	orphan.Carrier = 0
	cut := c.composeUnits(t, cur, []frame.UnitView{*labView, orphan})

	fullSurvivors, cutSurvivors, silhouette := 0, 0, 0
	for i := range alone {
		if alone[i] == 0 {
			continue
		}
		silhouette++
		if full[i] == alone[i] {
			fullSurvivors++
		}
		if cut[i] == alone[i] {
			cutSurvivors++
		}
	}
	if silhouette == 0 {
		t.Fatal("the nanoframe composed no pixels of its own")
	}
	if fullSurvivors <= cutSurvivors {
		t.Fatalf("the nanoframe is not presented with its factory: %d/%d of its pixels stand with the carrier link, %d/%d without it [03 R-RAST-01 §7]",
			fullSurvivors, silhouette, cutSurvivors, silhouette)
	}

	if dir := os.Getenv("NANOLATHE_SHOT_DIR"); dir != "" {
		writeIndexedPNG(t, c, cut, dir+"/nanoframe-before.png")
		writeIndexedPNG(t, c, full, dir+"/nanoframe-after.png")
	}
}

// composeUnits runs the two production unit passes over a frame holding just
// the named units and returns a copy of the indexed surface.
func (c *Client) composeUnits(t *testing.T, base *frame.Frame, units []frame.UnitView) []uint8 {
	t.Helper()
	one := &frame.Frame{
		Tick:      base.Tick,
		Units:     units,
		Selection: frame.SelectionView{LocalPlayer: units[0].Owner},
	}
	for i := range c.indexed {
		c.indexed[i] = 0
	}
	c.drawWorldPass(one, true)
	c.drawWorldPassB(one, true)
	return append([]uint8(nil), c.indexed...)
}

func writeIndexedPNG(t *testing.T, c *Client, indexed []uint8, path string) {
	t.Helper()
	copy(c.indexed, indexed)
	c.convertIndexedToRGBA()
	img := image.NewRGBA(image.Rect(0, 0, c.width, c.height))
	copy(img.Pix, c.rgba)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
