//go:build retail

package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// Physical pages resolve their installed gadget names [07 §9], even when
// CANBUILD has a different ordering. Both the construction seaplane and
// fighter button must queue themselves rather than another aircraft.
func TestSeaplanePlatformBuildClicksUseInstalledNames(t *testing.T) {
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
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, newBattleWindowContext(cs, nil))
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
	findGadget := func(name string) (int, gui.Gadget) {
		for i, gad := range w.Gadgets {
			if strings.EqualFold(gad.Name, name) {
				return i, gad
			}
		}
		t.Fatalf("missing gadget %s", name)
		return -1, gui.Gadget{}
	}
	constructorIndex, _ := findGadget("ARMCSA")
	fighterIndex, _ := findGadget("ARMSFIG")

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
		// The actual factory order must retain the clicked product identity.
		tail := q.Primary()[q.LenPrimary()-1]
		if !strings.EqualFold(tail.BuildDefKey, want) {
			t.Fatalf("%s: factory queue tail product = %q, want %q", name, tail.BuildDefKey, want)
		}
	}

	clickAndExpect("named construction seaplane", constructorIndex, "armcsa")
	clickAndExpect("named fighter", fighterIndex, "armsfig")
}
