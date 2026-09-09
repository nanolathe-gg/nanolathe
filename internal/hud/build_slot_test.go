package hud

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// buildMenuFixture builds a minimal catalog carrying one builder's CANBUILD
// page so BuildProductForSlot can be exercised without the retail corpus.
func buildMenuFixture(builder string, buttons ...string) *content.Catalog {
	return &content.Catalog{
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey(builder): {
				Builder:         builder,
				Buttons:         buttons,
				BaseButtonCount: len(buttons),
			},
		},
	}
}

func TestBuildProductForSlotOrdinal(t *testing.T) {
	cat := buildMenuFixture("plat", "a", "b", "c", "d", "e", "f", "g", "h")
	cases := []struct {
		page, slot int
		want       string
		ok         bool
	}{
		{1, 0, "a", true},
		{1, 5, "f", true},
		{2, 0, "g", true},
		{2, 1, "h", true},
		{2, 2, "", false}, // short final page: no seventh page-2 entry
		{3, 0, "", false}, // page beyond the authored list
		{0, 0, "", false}, // page 0 is the orders state, no products
	}
	for _, c := range cases {
		got, ok := BuildProductForSlot(cat, "plat", c.page, c.slot)
		if got != c.want || ok != c.ok {
			t.Fatalf("page %d slot %d: got (%q,%v) want (%q,%v)", c.page, c.slot, got, ok, c.want, c.ok)
		}
	}
	if got, ok := BuildProductForSlot(cat, "plat", 1, -1); ok || got != "" {
		t.Fatalf("negative slot ordinal must refuse, got (%q,%v)", got, ok)
	}
	if got, ok := BuildProductForSlot(nil, "plat", 1, 0); ok || got != "" {
		t.Fatalf("nil catalog must refuse, got (%q,%v)", got, ok)
	}
}

// TestBuildProductForSlotDisagreesWithArmplatGadgetNames measures the exact
// defect a gadget-name-keyed resolver would hit on the real seaplane
// platform: guis/armplat1.gui's first build gadget is authored `name=ARMCSA`
// (a stale editor label — nothing engine-side reads a build gadget's name for
// identity [07 §9 "Product-page assembly is closed"][fmt gui "GADGET(n)
// fields"]), while sidedata.tdf's canbuild1 for the same builder is ARMCA.
// BuildProductForSlot answers from ordinal CANBUILD position and must ignore
// the gadget's authored name entirely, so slot 0 resolves to "armca" even
// though the panel's own gadget at that position is named ARMCSA — and a
// later slot (armplat1.gui's fourth build gadget, authored ARMSFIG) must
// resolve to canbuild4 (armhawk), not collide with canbuild3's own armsfig.
//
// Skips without retail assets (retailcat.Shared -> testsupport.RetailRoot).
func TestBuildProductForSlotDisagreesWithArmplatGadgetNames(t *testing.T) {
	cat, fsys := retailcat.Shared(t)
	window, err := gui.Load(fsys, "guis/armplat1.gui")
	if err != nil || window == nil {
		t.Fatalf("guis/armplat1.gui: %v", err)
	}
	// Collect the build-product gadgets in file order: kind Button, excluding
	// the header, the bitmap-only ARMBUTT filename gadget, and the paging/order
	// controls (ARMPREV, ARMNEXT, ARMORDERS.. ARMBLAST), the same exclusion the
	// live click handler applies before it reaches the build-product branch.
	skip := map[string]bool{
		"ARMBUTT": true, "ARMPREV": true, "ARMNEXT": true,
		"ARMORDERS": true, "ARMBUILD": true, "ARMMOVE": true, "ARMSTOP": true,
		"ARMPATROL": true, "ARMATTACK": true, "ARMDEFEND": true, "ARMBLAST": true,
	}
	var gadgetNames []string
	for i, g := range window.Gadgets {
		if i == 0 || g.Kind != gui.KindButton {
			continue
		}
		if skip[g.Name] {
			continue
		}
		gadgetNames = append(gadgetNames, g.Name)
	}
	if len(gadgetNames) == 0 {
		t.Fatalf("no build-product gadgets survived exclusion in guis/armplat1.gui")
	}
	if gadgetNames[0] != "ARMCSA" {
		t.Fatalf("fixture assumption stale: armplat1.gui's first build gadget is now %q, not ARMCSA — re-derive this test's premise", gadgetNames[0])
	}

	product, ok := BuildProductForSlot(cat, "armplat", 1, 0)
	if !ok || content.CanonicalKey(product) != "armca" {
		t.Fatalf("slot 0 must resolve to canbuild1 (armca) regardless of the gadget's own name %q; got (%q,%v)", gadgetNames[0], product, ok)
	}
	if content.CanonicalKey(product) == content.CanonicalKey(gadgetNames[0]) {
		t.Fatalf("the gadget's authored name %q must NOT be the resolved product on the patched reference install", gadgetNames[0])
	}

	// The fourth build gadget collides by name with canbuild3, not its own
	// canbuild4 — a name-keyed resolver binds it to the wrong unit instead of
	// merely dropping it.
	if len(gadgetNames) > 3 && gadgetNames[3] == "ARMSFIG" {
		want, ok := BuildProductForSlot(cat, "armplat", 1, 3)
		if !ok || content.CanonicalKey(want) != "armhawk" {
			t.Fatalf("slot 3 must resolve to canbuild4 (armhawk); got (%q,%v)", want, ok)
		}
		if content.CanonicalKey(gadgetNames[3]) == content.CanonicalKey(want) {
			t.Fatalf("slot 3's own gadget name %q must not already equal the correct product — re-derive this test's premise", gadgetNames[3])
		}
	}
}
