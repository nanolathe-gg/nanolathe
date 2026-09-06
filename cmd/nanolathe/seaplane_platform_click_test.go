//go:build retail

package main

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
)

// TestSeaplanePlatformBuildClicksAreOrdinalNotGadgetName is the play-test
// regression this unit was dispatched for: pressing the seaplane platform's
// (ARMPLAT) build button for what the panel itself labels the construction
// seaplane did nothing. guis/armplat1.gui's first build gadget is authored
// `name=ARMCSA` — a stale per-panel editor label the engine never reads for
// identity — while `sidedata.tdf`'s canbuild1 for ARMPLAT is ARMCA; retail's
// product-page assembly patches CANBUILD products into build-page slots by
// ORDINAL position, "entries 1-6 map to page one..." [07 §9 "Product-page
// assembly is closed"]. The panel's fourth build gadget (authored `ARMSFIG`)
// is a second, worse case: name-matching it would cross-bind canbuild3
// instead of the slot's own canbuild4 (ARMHAWK).
//
// This test clicks the real armplat1.gui at the pixel positions of build
// slot 1 and build slot 4 (1-based, matching the CANBUILD/page-slot
// numbering [07 R-HUD-03 §6]) and asserts the factory queue receives armca
// and armhawk respectively — never armcsa (unbuildable in retail: absent
// from every CANBUILD page in the patched reference install) and never
// armsfig on slot 4.
func TestSeaplanePlatformBuildClicksAreOrdinalNotGadgetName(t *testing.T) {
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
	}

	platDef, ok := cat.Unit("armplat")
	if !ok || platDef == nil || !platDef.Builder {
		t.Fatal("retail catalog has no armplat builder")
	}
	buttons := hud.BuildProductsFor(cat, "armplat")
	if len(buttons) < 4 {
		t.Fatalf("armplat CANBUILD page has only %d entries, want at least 4: %v", len(buttons), buttons)
	}
	wantSlot1, wantSlot4 := buttons[0], buttons[3]
	if !strings.EqualFold(wantSlot1, "armca") || !strings.EqualFold(wantSlot4, "armhawk") {
		t.Fatalf("fixture assumption stale: armplat CANBUILD is now %v, want [armca ... armhawk ...] at 1 and 4 — re-derive this test's premise", buttons)
	}

	var spawnX, spawnZ numeric.Fixed
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == sess.LocalOwner && u.Def != nil && strings.HasSuffix(strings.ToLower(u.Def.UnitName), "com") {
			spawnX, spawnZ = u.X, u.Z
			break
		}
	}
	if spawnX == 0 && spawnZ == 0 {
		t.Fatal("no local commander to place the platform beside")
	}
	// This fixture bypasses placement/terrain validation by creating the
	// platform directly, the same shortcut TestRetailFactoryProductClickQueues
	// AndBuilds takes for the kbot lab: what this test exercises is the click
	// resolution and the resulting factory queue, not siting a floating
	// factory on water (that contract is locked separately, in
	// internal/construction's floating-factory exit test).
	const platOffsetCells = 14
	platHandle, err := sess.Units.Create(platDef, sess.LocalOwner,
		spawnX+numeric.Fixed(int64(platOffsetCells)<<20), numeric.Fixed(0), spawnZ)
	if err != nil {
		t.Fatal(err)
	}
	plat := sess.Units.Unit(platHandle)
	if plat == nil {
		t.Fatal("platform not in pool")
	}
	for _, u := range sess.Units.Iter() {
		if u == nil {
			continue
		}
		u.Flags &^= hud.SelectionFlag
		if u.Handle == platHandle {
			// Page 0 is the orders state and carries no products
			// [07 R-HUD-03 §6].
			u.Flags |= hud.SelectionFlag
			u.Flags = hud.EncodePageBits(u.Flags, 1)
		}
	}
	for step := int32(31); step <= 60; step++ {
		sess.Step(step)
	}
	cur := sess.Snapshot.Current()
	if cur == nil {
		t.Fatal("no published frame")
	}
	if cur.CommandPage.Builder != platHandle {
		t.Fatalf("CommandPage.Builder = %d, want platform %d; page=%+v", cur.CommandPage.Builder, platHandle, cur.CommandPage)
	}

	const winW, winH = 640, 480
	cam := &camera.Camera{
		ViewW: winW, ViewH: winH,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16),
	}
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	pal := retailPaletteForTest(t, cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, _, err := b.hud.windowForRequired(b, cur)
	if err != nil {
		t.Fatalf("command window: %v", err)
	}
	if w == nil || !strings.HasSuffix(strings.ToLower(w.Name), "armplat1.gui") {
		if w == nil {
			t.Fatal("platform window is nil; want armplat1.gui")
		}
		t.Fatalf("platform window = %q; want suffix armplat1.gui", w.Name)
	}
	// Confirm the fixture is exercising the real defect: the gadget occupying
	// build slot 1 is authored ARMCSA, not ARMCA.
	paged := commandPageIsPaged(cur)
	ordinals := buildProductSlotOrdinals(w, cur, paged)
	gadgetForSlot := func(slot int) (int, gui.Gadget) {
		for i, o := range ordinals {
			if o == slot {
				return i, w.Gadgets[i]
			}
		}
		t.Fatalf("no gadget occupies build slot %d in armplat1.gui", slot+1)
		return -1, gui.Gadget{}
	}
	slot1Index, slot1Gadget := gadgetForSlot(0)
	if !strings.EqualFold(slot1Gadget.Name, "armcsa") {
		t.Fatalf("fixture assumption stale: armplat1.gui's slot-1 gadget is now %q, not ARMCSA — re-derive this test's premise", slot1Gadget.Name)
	}
	slot4Index, slot4Gadget := gadgetForSlot(3)
	if !strings.EqualFold(slot4Gadget.Name, "armsfig") {
		t.Fatalf("fixture assumption stale: armplat1.gui's slot-4 gadget is now %q, not ARMSFIG — re-derive this test's premise", slot4Gadget.Name)
	}

	clickAndExpect := func(name string, gadgetIndex int, want string) {
		t.Helper()
		r := w.PlacedRect(gadgetIndex)
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		if !b.hud.sameButton(b, cx, cy, cx, cy) {
			t.Fatalf("%s: gadget %d not hit at its own rect center", name, gadgetIndex)
		}
		if !b.hud.consumeClick(b, cx, cy) {
			t.Fatalf("%s: click not consumed by HUD", name)
		}
		if b.battleState().Input.BuildDef != "" {
			t.Fatalf("%s: factory product click must not arm placement, got buildDef %q", name, b.battleState().Input.BuildDef)
		}
		sess.Step(sess.Clock.ScaledAnchor + 1)
		q := orders.QueueForUnit(plat)
		if q == nil || q.LenPrimary() == 0 {
			t.Fatalf("%s: factory queue empty after click; pending=%v", name, sess.PendingHumanCommands())
		}
		// A second click always appends/coalesces onto the tail, so the last
		// entry after this click is this click's product regardless of what an
		// earlier click in this test already queued — armca and armhawk are
		// distinct keys, so they never coalesce into one entry.
		tail := q.Primary()[q.LenPrimary()-1]
		if !strings.EqualFold(tail.BuildDefKey, want) {
			t.Fatalf("%s: factory queue tail product = %q, want %q", name, tail.BuildDefKey, want)
		}
	}

	clickAndExpect("slot 1", slot1Index, "armca")
	clickAndExpect("slot 4", slot4Index, "armhawk")
}
