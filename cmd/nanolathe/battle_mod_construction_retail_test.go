//go:build retail

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Exercise authored button identity through admission, placement and construction,
// with automatic content layout selection for each installed faction.
func TestRetailModFactoryConstructionFromModernMenu(t *testing.T) {
	for _, mod := range []struct {
		name  string
		sides int
	}{{"prota", 2}, {"zero", 3}, {"escalation", 2}} {
		roots := filepath.SplitList(os.Getenv("NANOLATHE_MOD_ROOTS_" + strings.ToUpper(mod.name)))
		for side := 0; side < mod.sides; side++ {
			t.Run(fmt.Sprintf("%s/side%d", mod.name, side), func(t *testing.T) {
				if len(roots) == 0 {
					t.Skip("installed mod roots not supplied")
				}
				opts := Options{Root: testsupport.RetailRoot(t), Roots: append([]string{testsupport.RetailRoot(t)}, roots...), Map: "ashap plateau", Seed: 7}
				cs, err := openContent(opts)
				if err != nil {
					t.Fatal(err)
				}
				defer cs.Close()
				cfg := session.SkirmishConfig{MapName: opts.Map, NumPlayers: 2}
				cfg.Players[0].Side = side
				cfg.Players[1].Controller = session.SkirmishControllerComputer
				cfg.ApplyDefaults()
				sess, cat, err := newBattleSessionWithConfig(opts, cs, cfg)
				if err != nil {
					t.Fatal(err)
				}
				for _, u := range sess.Units.Iter() {
					if u != nil && u.Owner == sess.LocalOwner && u.Def != nil && u.Def.Builder {
						u.Flags |= hud.SelectionFlag
						break
					}
				}
				for step := int32(1); step <= 30; step++ {
					sess.Step(step)
				}
				cl, err := client.New(client.Options{Width: 640, Height: 480})
				if err != nil {
					t.Fatal(err)
				}
				cl.SetEnhanced(true)
				b := &battleSession{sess: sess, cat: cat, cl: cl, cam: &camera.Camera{ViewW: 640, ViewH: 480, MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}}
				b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, retailPaletteForTest(t, cs), nil, newBattleWindowContext(cs, nil))
				if err != nil {
					t.Fatal(err)
				}
				f := sess.Snapshot.Current()
				builder := sess.Units.Unit(f.CommandPage.Builder)
				if builder == nil {
					t.Fatal("missing selected commander")
				}
				paging, active := b.hud.expandedSidebarPaging(b, f)
				if !active {
					t.Fatal("missing fitted menu")
				}
				for page := 1; page < paging.Count; page++ {
					b.hud.selectExpandedSidebarPage(b, f, page)
					w, _, err := b.hud.windowForRequired(b, f)
					if err != nil {
						t.Fatal(err)
					}
					for i, g := range w.Gadgets {
						product, ok := cat.Unit(g.Name)
						if !ok || product == nil || !product.Builder || product.BMCode != 0 {
							continue
						}
						r := w.PlacedRect(i)
						if !hudConsumeClick(b.hud, b, r.X+r.W/2, r.Y+r.H/2) || b.PlacementProduct() != strings.ToLower(g.Name) {
							t.Fatalf("factory %s did not arm", g.Name)
						}
						state := &b.battleState().Input
						cx, cz := world.WorldToCell(builder.X), world.WorldToCell(builder.Z)
						placed := false
						for z := cz - 8; z <= cz+8 && !placed; z++ {
							for x := cx - 8; x <= cx+8; x++ {
								result, err := b.checkProductPlacement(x, z, product, state.BuildFootX, state.BuildFootZ, uint16(builder.Handle))
								if err != nil {
									continue
								}
								state.BuildCellX, state.BuildCellZ, state.BuildSiteH = x, z, result.SiteHeight
								state.BuildOK = true
								placed = b.commitBuild(false)
								if placed {
									break
								}
							}
						}
						if !placed {
							continue
						} // Try another authored factory when no nearby site is legal.
						pending := sess.PendingHumanCommands()
						if len(pending) != 1 || pending[0].Kind != session.HumanMobileBuild || !strings.EqualFold(pending[0].MobileBuild.Product, g.Name) {
							t.Fatalf("wrong factory command: %v", pending)
						}
						for tick := 0; tick < 1000; tick++ {
							sess.Step(sess.Clock.ScaledAnchor + 1)
							for _, u := range sess.Units.Iter() {
								if u != nil && u.Alive && u.Owner == sess.LocalOwner && u.Def != nil && strings.EqualFold(u.Def.UnitName, g.Name) {
									t.Logf("%s placed %s at tick %d", builder.Def.UnitName, g.Name, sess.Clock.GlobalTick)
									return
								}
							}
						}
						t.Fatalf("factory %s did not create a construction frame", g.Name)
					}
				}
				t.Fatal("no factory with a legal construction site")
			})
		}
	}
}
