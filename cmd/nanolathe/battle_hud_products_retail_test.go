//go:build retail

package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// publishedProductAllowed reports CANBUILD-derived membership as published on
// the committed command page. It is a membership query only: product buttons
// and placement do not consult it [07 R-HUD-03 §6][07 §9].
func publishedProductAllowed(cat *content.Catalog, f *frame.Frame, product string) bool {
	key := content.CanonicalKey(product)
	return key != "" && slices.ContainsFunc(hud.AllowedBuildProducts(cat, f), func(candidate string) bool {
		return content.CanonicalKey(candidate) == key
	})
}

// TestRetailConstructionShipYardAndLLTAreBuildable locks the tester report:
// a construction ship afloat keeps its authored shipyard (top right) and LLT
// (row three, right) buttons live and placeable whichever executor F10 has
// selected. Retail greys a product slot only when its installed name resolves
// to no definition [07 R-HUD-03 §6], arms placement from that installed name
// and issues the world click without a CANBUILD test [07 §9]. Stock CORCS
// installs CORSY and CORLLT on page one although its CANBUILD omits both, which
// is what the Modern flat sidebar used to grey; ARMCS lists both and guards
// the ordinary path.
func TestRetailConstructionShipYardAndLLTAreBuildable(t *testing.T) {
	cases := []struct {
		side     int
		ship     string
		products []string
		listed   bool
	}{
		{0, "ARMCS", []string{"ARMSY", "ARMLLT"}, true},
		{1, "CORCS", []string{"CORSY", "CORLLT"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.ship, func(t *testing.T) {
			opts := Options{Root: testsupport.RetailRoot(t), Map: "coast to coast", Seed: 7}
			cs, err := openContent(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			cfg := session.SkirmishConfig{MapName: opts.Map, NumPlayers: 2}
			cfg.Players[0].Side = tc.side
			cfg.Players[1].Controller = session.SkirmishControllerComputer
			cfg.ApplyDefaults()
			sess, cat, err := newBattleSessionWithConfig(opts, cs, cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx := newBattleWindowContext(cs, nil)
			ctx.completeTransition()
			h, err := loadRetailBattleHUD(cs.fs, sess, cat, retailPaletteForTest(t, cs), nil, ctx)
			if err != nil {
				t.Fatal(err)
			}
			cl, err := client.New(client.Options{Width: 1280, Height: 768})
			if err != nil {
				t.Fatal(err)
			}
			b := &battleSession{sess: sess, cat: cat, cl: cl, hud: h}

			// Float the ship on the nearest deep water to the local start.
			var x, z numeric.Fixed
			for _, u := range sess.Units.Iter() {
				if u.Owner == sess.LocalOwner {
					x, z = u.X, u.Z
				}
				u.Flags &^= hud.SelectionFlag
			}
			sea := sess.World.SeaLevelWorld()
			found := false
			for r := int64(32); r < 4096 && !found; r += 32 {
				for _, dx := range []int64{-r, 0, r} {
					for _, dz := range []int64{-r, 0, r} {
						wx, wz := x+numeric.FixedFromInt(dx), z+numeric.FixedFromInt(dz)
						if !found && wx > 0 && wz > 0 && sess.World.HeightAt(wx, wz) < sea-numeric.FixedFromInt(40) {
							x, z, found = wx, wz, true
						}
					}
				}
			}
			if !found {
				t.Fatal("no deep water near the local start")
			}
			def, ok := cat.Unit(tc.ship)
			if !ok || def == nil {
				t.Fatalf("%s absent from the catalog", tc.ship)
			}
			handle, err := sess.Units.Create(def, sess.LocalOwner, x, sea, z)
			if err != nil {
				t.Fatal(err)
			}
			ship := sess.Units.Unit(handle)
			ship.Flags = hud.EncodePageBits(ship.Flags|hud.SelectionFlag, 1)
			for step := int32(1); step <= 5; step++ {
				sess.Step(step)
			}
			f := sess.Snapshot.Current()
			if f == nil || f.CommandPage.Builder != handle {
				t.Fatalf("%s does not own the command page", tc.ship)
			}
			for _, product := range tc.products {
				if got := publishedProductAllowed(cat, f, product); got != tc.listed {
					t.Fatalf("%s CANBUILD membership of %s = %v, want %v (stock sidedata)", tc.ship, product, got, tc.listed)
				}
			}

			for _, modern := range []bool{false, true} {
				// F10 swaps executors; the modern one presents Enhanced and,
				// with the default preference, the flat sidebar.
				cl.SetEnhanced(modern)
				for _, product := range tc.products {
					t.Run(fmt.Sprintf("modern=%v/%s", modern, product), func(t *testing.T) {
						w, _, err := h.windowForRequired(b, f)
						if err != nil || w == nil {
							t.Fatalf("command window: %v", err)
						}
						if flat := h.expandedSidebar.window == w; flat != modern {
							t.Fatalf("flat sidebar = %v under modern=%v", flat, modern)
						}
						index := slices.IndexFunc(w.Gadgets, func(g gui.Gadget) bool { return strings.EqualFold(g.Name, product) })
						if index < 0 {
							t.Fatalf("%s button absent", product)
						}
						gad := w.Gadgets[index]
						verdict, _ := h.sidebarGadgetVerdict(w, gad, f, commandPageIsPaged(f), cat)
						if verdict.grey || gad.GrayedOut&1 != 0 {
							t.Fatalf("%s button greyed", product)
						}
						r := w.PlacedRect(index)
						if !hudConsumeClick(h, b, r.X+r.W/2, r.Y+r.H/2) || b.PlacementProduct() != content.CanonicalKey(product) {
							t.Fatalf("%s click armed %q", product, b.PlacementProduct())
						}
						defer b.disarmPlacement()
						placed, _ := cat.Unit(product)
						state := &b.battleState().Input
						before := len(sess.PendingHumanCommands())
						cx, cz := world.WorldToCell(ship.X), world.WorldToCell(ship.Z)
						committed := false
						for d := int32(0); d <= 64 && !committed; d += 2 {
							for _, cell := range [][2]int32{{cx + d, cz}, {cx - d, cz}, {cx, cz + d}, {cx, cz - d}, {cx + d, cz + d}, {cx - d, cz - d}, {cx + d, cz - d}, {cx - d, cz + d}} {
								result, err := b.checkProductPlacement(cell[0], cell[1], placed, state.BuildFootX, state.BuildFootZ, uint16(handle))
								if err != nil || result.OccupantsAdmitted {
									continue
								}
								state.BuildCellX, state.BuildCellZ, state.BuildSiteH = cell[0], cell[1], result.SiteHeight
								state.BuildOK = true // from the production placement validator above
								if committed = b.commitBuild(false); committed {
									break
								}
							}
						}
						if !committed {
							t.Fatalf("no %s site was accepted", product)
						}
						pending := sess.PendingHumanCommands()
						if len(pending) != before+1 || pending[len(pending)-1].Kind != session.HumanMobileBuild || !strings.EqualFold(pending[len(pending)-1].MobileBuild.Product, product) {
							t.Fatalf("placement queued %v, want %s", pending[before:], product)
						}
					})
				}
			}
		})
	}
}
