//go:build retail

package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A physical or generated button's installed name selects the definition
// [07 §9]. The picture and the placement product must agree even when the
// published CANBUILD/download union has a different order.
func TestRetailAdvancedKbotAmbusherClick(t *testing.T) {
	opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	def, ok := cat.Unit("armack")
	if !ok {
		t.Fatal("missing advanced construction kbot")
	}
	com := sess.Units.Iter()[0]
	if com == nil || com.Owner != sess.LocalOwner {
		t.Fatal("missing local starting unit")
	}
	handle, err := sess.Units.Create(def, sess.LocalOwner, com.X+numeric.FixedFromInt(64), com.Y, com.Z+numeric.FixedFromInt(64))
	if err != nil {
		t.Fatal(err)
	}
	builder := sess.Units.Unit(handle)
	for _, u := range sess.Units.Iter() {
		if u != nil {
			u.Flags &^= hud.SelectionFlag
		}
	}
	builder.Flags |= hud.SelectionFlag
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, retailPaletteForTest(t, cs), nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	for page := 1; page < int(def.BuildPageCount); page++ {
		builder.Flags = hud.EncodePageBits(builder.Flags, page)
		sess.Step(sess.Clock.ScaledAnchor + 1)
		cur := sess.Snapshot.Current()
		w, _, err := b.hud.windowForRequired(b, cur)
		if err != nil {
			t.Fatal(err)
		}
		if w == nil {
			continue
		}
		for i, gad := range w.Gadgets {
			if !strings.EqualFold(gad.Name, "armamb") {
				continue
			}
			r := w.PlacedRect(i)
			t.Logf("page=%d gadget=%d name=%s rect=%+v published=%v generated=%v", page, i, gad.Name, r, cur.CommandPage.ProductKeys, cur.CommandPage.GeneratedProducts)
			if !hudConsumeClick(b.hud, b, r.X+r.W/2, r.Y+r.H/2) {
				t.Fatal("Ambusher click not consumed")
			}
			if got := b.PlacementProduct(); got != "armamb" {
				t.Fatalf("Ambusher button armed %q, want armamb", got)
			}

			product, _ := cat.Unit(b.PlacementProduct())
			state := &b.battleState().Input
			cx, cz := world.WorldToCell(builder.X), world.WorldToCell(builder.Z)
			placed, admittedObstruction := false, false
			for z := cz - 4; z <= cz+4 && !placed; z++ {
				for x := cx - 4; x <= cx+4; x++ {
					result, err := b.checkProductPlacement(x, z, product, state.BuildFootX, state.BuildFootZ, uint16(handle))
					if err != nil {
						continue
					}
					// Community and Modern deliberately admit a preview over another
					// own mobile unit, while allocation still waits for a physically
					// clear site. Record that contract, then use an unobstructed site
					// so this test remains about the clicked product's real lifecycle.
					if result.OccupantsAdmitted {
						admittedObstruction = true
						continue
					}
					state.BuildCellX, state.BuildCellZ, state.BuildSiteH = x, z, result.SiteHeight
					state.BuildOK = true // from the production placement validator above
					placed = b.commitBuild(false)
					if placed {
						break
					}
				}
			}
			if !placed {
				t.Fatal("no valid nearby Ambusher site")
			}
			if !admittedObstruction {
				t.Fatal("fixture did not encounter the Community/Modern own-unit placement admission")
			}
			pending := sess.PendingHumanCommands()
			if len(pending) != 1 || pending[0].Kind != session.HumanMobileBuild || pending[0].MobileBuild.Product != "armamb" {
				t.Fatalf("placement queued %v, want Ambusher", pending)
			}
			for step := 0; step < 1000; step++ {
				sess.Step(sess.Clock.ScaledAnchor + 1)
				for _, u := range sess.Units.Iter() {
					if u != nil && u.Alive && u.Owner == sess.LocalOwner && u.Def != nil && strings.EqualFold(u.Def.UnitName, "armamb") {
						return
					}
				}
			}
			t.Fatalf("Ambusher order did not create its nanoframe: tick=%d site=%d,%d", sess.Clock.GlobalTick, state.BuildCellX, state.BuildCellZ)
			return
		}
	}
	t.Fatal("Ambusher button not found")
}
