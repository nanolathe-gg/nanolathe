package client

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/headless"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestCarriedUnitIsPresentedOnlyByItsCarrier is the play-test regression: a
// unit riding an Arm Atlas must be occluded by the transport's hull, not
// painted over it.
//
// The contract is [03 R-RAST-01 §7-A]: the per-unit present returns at once for
// a unit that holds a carrier link, so a carried unit reaches the screen only
// through the children step of its carrier's present, inside the carrier's
// staging image and under the key test [R-REN-03A §4]. The bucket build applies
// no carrier filter, so the cargo is still bucketed and still walked — what the
// pass finds is a present that does nothing.
//
// Three relationships, no census: the carried view alone draws nothing, the
// same view with its link cut draws a silhouette, and in the published frame
// the carrier takes part of that silhouette back. The third is the regression
// itself — while the cargo was blitted a second time from its own bucket entry
// that blit was unconditional, so every pixel of the silhouette survived
// verbatim and the count was zero.
//
// The fixture deliberately leaves the Atlas grounded. A grounded carrier is
// pass A's and a mode-0 cargo is pass B's, so the cargo's own visit lands after
// the carrier's whole staging image whatever the slot order — the ordering this
// test would otherwise depend on.
func TestCarriedUnitIsPresentedOnlyByItsCarrier(t *testing.T) {
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
	carrierDef, ok := sess.Catalog.Unit("armatlas")
	if !ok || carrierDef == nil {
		t.Skip("retail catalog has no armatlas")
	}
	cargoDef, ok := sess.Catalog.Unit("armpw")
	if !ok || cargoDef == nil {
		t.Skip("retail catalog has no armpw")
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
		t.Skip("no local commander to place the transport beside")
	}
	cell := func(n int) numeric.Fixed { return numeric.Fixed(int64(n) << 20) }
	cX, cZ := comX+cell(6), comZ+cell(2)
	gX, gZ := comX+cell(10), comZ+cell(2)
	carrierHandle, err := sess.Units.Create(carrierDef, sess.LocalOwner, cX, sess.World.HeightAt(cX, cZ), cZ)
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	cargoHandle, err := sess.Units.Create(cargoDef, sess.LocalOwner, gX, sess.World.HeightAt(gX, gZ), gZ)
	if err != nil {
		t.Fatalf("create cargo: %v", err)
	}
	carrier := sess.Units.Unit(carrierHandle)
	cargo := sess.Units.Unit(cargoHandle)
	if carrier == nil || cargo == nil {
		t.Fatal("created units are not resolvable")
	}
	sess.Movement.EnsureUnit(carrier)
	sess.Movement.EnsureUnit(cargo)

	// The attach piece comes from the carrier's own script, exactly as the load
	// executor's phase 2 asks for it — cell 0 seeded -1, so a scriptless
	// carrier would leave the root fallback [04 R-UNIT-06 §3][04 §10.2].
	piece := int32(-1)
	if bridge := carrier.ScriptBridge(); bridge != nil {
		piece = bridge.QueryTransport().Values[0]
	}
	if piece < 0 {
		t.Skip("the transport's script answers no attach piece; the fixture needs a real hang piece")
	}
	if !movement.AttachCargo(sess.Units, carrierHandle, cargoHandle, int(piece)) {
		t.Fatal("AttachCargo refused the fixture pair")
	}
	// One more tick so the carried branch slaves the cargo to the hang piece
	// and the publication boundary carries the link into the frame
	// [04 §10.2][04 R-FAC-02 §2].
	sess.Step(31)
	cur := sess.Snapshot.Current()
	if cur == nil {
		t.Fatal("no committed frame")
	}
	var carrierView, cargoView *frame.UnitView
	for i := range cur.Units {
		switch cur.Units[i].Slot {
		case carrierHandle:
			carrierView = &cur.Units[i]
		case cargoHandle:
			cargoView = &cur.Units[i]
		}
	}
	if carrierView == nil || cargoView == nil {
		t.Fatal("the fixture pair is not in the committed frame")
	}
	if cargoView.Carrier != carrierHandle || cargoView.CarriedPiece < 0 {
		t.Fatalf("the published cargo view carries no usable link: carrier %d piece %d", cargoView.Carrier, cargoView.CarriedPiece)
	}

	c := newTestClient(t)
	c.SetPalette(pal)
	c.SetModelFS(fs)
	camX := int32(int64(carrierView.X)>>16) - 320
	camZ := int32(int64(carrierView.Z)>>16) - 240
	c.cam = &camera.Camera{X: camX, Z: camZ, ViewW: 640, ViewH: 480,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}
	c.frameTick = cur.Tick

	// (1) The cargo's own bucket entry, with its carrier absent from the frame,
	// must draw nothing: the present returns on the carrier link alone and does
	// not check whether the carrier was reached this frame [03 R-RAST-01 §7-A].
	cargoAlone := c.composeUnits(t, cur, []frame.UnitView{*cargoView})
	for i := range cargoAlone {
		if cargoAlone[i] != 0 {
			t.Fatalf("a carried unit painted its own pixels with no carrier in the frame; the per-unit present must return on the carrier link [03 R-RAST-01 §7-A]")
		}
	}

	// (2) The same view with the link cut must compose pixels of its own, which
	// is what makes (1) a statement about the link rather than about an empty
	// model or an off-screen position.
	orphan := *cargoView
	orphan.Carrier = 0
	orphanOnly := c.composeUnits(t, cur, []frame.UnitView{orphan})
	silhouette := 0
	for i := range orphanOnly {
		if orphanOnly[i] != 0 {
			silhouette++
		}
	}
	if silhouette == 0 {
		t.Fatal("the cargo composes no pixels even with its carrier link cut, so (1) proves nothing about the link [03 R-RAST-01 §7-A]")
	}

	// (3) In the published frame the carrier's geometry must take some of those
	// pixels back. This is the play-test defect stated as a relationship: while
	// the cargo was presented a second time from its own bucket entry, its blit
	// was unconditional and every pixel of the silhouette survived verbatim, so
	// this count was exactly zero [R-REN-03A §4][03 R-RAST-01 §7-A].
	staged := c.composeUnits(t, cur, []frame.UnitView{*carrierView, *cargoView})
	occluded := 0
	for i := range orphanOnly {
		if orphanOnly[i] != 0 && staged[i] != orphanOnly[i] {
			occluded++
		}
	}
	if occluded == 0 {
		t.Fatalf("none of the cargo's %d pixels is resolved against the carrier; the transport's hull must occlude the unit it carries [R-REN-03A §4][03 R-RAST-01 §7-A]", silhouette)
	}
	t.Logf("carrier takes %d of the cargo's %d pixels", occluded, silhouette)
}
