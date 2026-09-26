//go:build retail

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The Aegis authors Builder=0 but has an ordinary numbered GUI containing a
// mobile upgrade product. A named counted-product button uses that selected
// unit's queue independently of mobile-build capability [07 R-P0-11 §1].
func TestEscalationAegisUpgradeFromAuthoredMenu(t *testing.T) {
	roots := filepath.SplitList(os.Getenv("NANOLATHE_MOD_ROOTS_ESCALATION"))
	if len(roots) == 0 {
		t.Skip("Escalation content roots not supplied")
	}
	opts := Options{Root: testsupport.RetailRoot(t), Roots: append([]string{testsupport.RetailRoot(t)}, roots...), Map: "expanded confluence", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	cfg := session.DirectSkirmishConfig(opts.Map)
	cfg.ApplyDefaults()
	cfg.Gameplay = gameplay.Modern
	sess, cat, err := newBattleSessionWithConfig(opts, cs, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range sess.AI {
		if a != nil {
			for j := range a.Deadlines {
				a.Deadlines[j] = 1 << 30
			}
		}
	}
	def, ok := cat.Unit("ARMSHGEN")
	if !ok || def.Builder {
		t.Fatal("fixture must retain the Aegis's authored non-builder flag")
	}
	x, z := numeric.FixedFromInt(600), numeric.FixedFromInt(600)
	h, err := sess.Units.Create(def, 0, x, sess.World.HeightAt(x, z), z)
	if err != nil {
		t.Fatal(err)
	}
	sess.CompleteUnit(h)
	provider := sess.Units.Unit(h)
	recipientDef, ok := cat.Unit("ARMMEX")
	if !ok {
		t.Fatal("missing extractor")
	}
	rx := x + numeric.FixedFromInt(500)
	rh, err := sess.Units.Create(recipientDef, 0, rx, sess.World.HeightAt(rx, z), z)
	if err != nil {
		t.Fatal(err)
	}
	sess.CompleteUnit(rh)
	recipient := sess.Units.Unit(rh)
	for _, c := range []session.HumanCommand{
		{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{h}}},
		{Kind: session.HumanBuildPage, BuildPage: session.HumanBuildPageCommand{Builder: h, Page: 1}},
	} {
		if err := sess.EnqueueHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	for tick := 0; tick < 300; tick++ {
		sess.Step(sess.Clock.ScaledAnchor + 1)
	}
	if recipient.Armored {
		t.Fatal("extractor outside the base radius is already protected")
	}
	cl, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	b := &battleSession{sess: sess, cat: cat, cl: cl, cam: &camera.Camera{ViewW: 640, ViewH: 480, MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}}
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, retailPaletteForTest(t, cs), nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	w, _, err := b.hud.windowForRequired(b, sess.Snapshot.Current())
	if err != nil {
		t.Fatal(err)
	}
	if w == nil {
		t.Fatal("Aegis's numbered GUI was not loaded")
	}
	capture := func(name string) {
		t.Helper()
		dir := os.Getenv("NANOLATHE_MENU_SHOTS")
		if dir == "" {
			return
		}
		cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
		cl.SetSnapshot(sess.Snapshot)
		cl.SetPalette(retailPaletteForTest(t, cs))
		f, err := os.Create(filepath.Join(dir, "escalation-aegis-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, cl.ComposeFrame())
		closeErr := f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	capture("menu")
	clicked := false
	for i, g := range w.Gadgets {
		if !strings.EqualFold(g.Name, "ARMSHGEN_UPG") {
			continue
		}
		r := w.PlacedRect(i)
		if !hudConsumeClick(b.hud, b, r.X+r.W/2, r.Y+r.H/2) {
			t.Fatal("authored upgrade click was not consumed")
		}
		clicked = true
		break
	}
	if !clicked {
		t.Fatal("Aegis's authored upgrade gadget is absent")
	}
	pending := sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanFactoryBuild || pending[0].FactoryBuild.Builder != h || pending[0].FactoryBuild.Product != "armshgen_upg" {
		t.Fatalf("upgrade click did not queue the authored product: %+v; diagnostic=%v", pending, b.hud.LastDispatchError())
	}
	// Let the real product and parent scripts finish construction and retain
	// the upgrade. The extractor is outside the base radius (377.5), inside
	// the completed upgrade radius (570) [research/extensions/escalation-shields.md
	// "Coverage discovery and recipient behavior"].
	for tick := 0; tick < 8000; tick++ {
		p := &sess.Econ.Players[0]
		p.Stock, p.Capacity = [2]float32{1e6, 1e6}, [2]float32{1e6, 1e6}
		sess.Step(sess.Clock.ScaledAnchor + 1)
		if tick == 0 {
			capture("queued")
		}
		if len(provider.Attachment.Cargo) != 1 || !recipient.Armored {
			continue
		}
		upgrade := sess.Units.Unit(provider.Attachment.Cargo[0])
		if upgrade == nil || upgrade.Def.CanonicalKey != "armshgen_upg" || upgrade.Remaining != 0 || upgrade.Attachment.Carrier != h || provider.InBuildStance {
			continue
		}
		for _, u := range []*units.Unit{provider, upgrade, recipient} {
			if diags := u.GetScript().Diagnostics(); len(diags) != 0 {
				t.Fatalf("%s diagnostics: %v", u.Def.UnitName, diags)
			}
		}
		capture("complete")
		return
	}
	t.Fatalf("Aegis upgrade never completed with extended protection: cargo=%+v armor=%v stance=%v diagnostics=%v", provider.Attachment.Cargo, recipient.Armored, provider.InBuildStance, provider.GetScript().Diagnostics())
}
