//go:build retail

package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestRetailARMLabGeneratedSecondPageQueuesWarriorAndFlea(t *testing.T) {
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
	labDef, ok := cat.Unit("armlab")
	if !ok || labDef == nil {
		t.Fatal("retail catalog has no ARMLAB")
	}
	var x, y, z numeric.Fixed
	for _, u := range sess.Units.Iter() {
		if u == nil || u.Owner != sess.LocalOwner {
			continue
		}
		u.Flags &^= hud.SelectionFlag
		x, y, z = u.X, u.Y, u.Z
	}
	labHandle, err := sess.Units.Create(labDef, sess.LocalOwner, x, y, z)
	if err != nil {
		t.Fatalf("create ARMLAB: %v", err)
	}
	lab := sess.Units.Unit(labHandle)
	lab.Flags |= hud.SelectionFlag | hud.EncodePageBits(0, 1)

	step := int32(1)
	stepOnce := func() {
		t.Helper()
		sess.Step(step)
		step++
	}
	for i := 0; i < 5; i++ {
		stepOnce()
	}
	cur := sess.Snapshot.Current()
	if cur == nil || cur.CommandPage.Builder != labHandle || cur.CommandPage.Page != 1 {
		t.Fatalf("ARMLAB first page snapshot = %#v", cur)
	}

	cam := &camera.Camera{
		ViewW: 640, ViewH: 480,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16),
	}
	centerBattleStartCamera(sess, cam)
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	pal := retailPaletteForTest(t, cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	clickNamed := func(w *gui.Window, suffix string) {
		t.Helper()
		for i, gad := range w.Gadgets {
			if i == 0 || gad.Kind != gui.KindButton || gad.Active == 0 || !strings.HasSuffix(strings.ToUpper(gad.Name), suffix) {
				continue
			}
			r := w.PlacedRect(i)
			if !b.hud.consumeClick(b, r.X+r.W/2, r.Y+r.H/2) {
				t.Fatalf("%s click was not consumed", suffix)
			}
			stepOnce()
			return
		}
		t.Fatalf("window %q has no %s gadget", w.Name, suffix)
	}

	first, _, err := b.hud.windowForRequired(b, cur)
	if err != nil || first == nil {
		t.Fatalf("ARMLAB page 1: window=%v err=%v", first, err)
	}
	clickNamed(first, "NEXT") // actual page-cycle button behavior [07 R-HUD-03 §6]
	cur = sess.Snapshot.Current()
	if cur == nil || cur.CommandPage.Page != 2 {
		t.Fatalf("NEXT selected page %v, want ARMLAB page 2", cur)
	}
	if _, err := cs.fs.Stat("guis/armlab2.gui"); !errors.Is(err, vfs.ErrNotFound) {
		t.Fatalf("guis/armlab2.gui unexpectedly exists or probe failed differently: %v", err)
	}
	generated, pageArt, err := b.hud.windowForRequired(b, cur)
	if err != nil || generated == nil {
		t.Fatalf("generated ARMLAB page 2: window=%v err=%v", generated, err)
	}
	if !strings.HasSuffix(strings.ToLower(generated.Name), "armdl.gui") {
		t.Fatalf("generated source window = %q, want ARMDL template", generated.Name)
	}
	want := []string{"armwar", "armflea"}
	for button, product := range want {
		gad := generated.Gadgets[button+4]
		if !strings.EqualFold(gad.Name, product) || !strings.EqualFold(gad.Art, "IGPATCH") || gad.GAFFile&1 == 0 || gad.GrayedOut&1 != 0 || gad.CommonAttribs != 4 {
			t.Fatalf("ARMDL slot %d = %+v, want %s product gadget", button, gad, product)
		}
		entry := b.hud.gadgetArtEntry(gad, pageArt)
		if entry == nil || len(entry.Frames) != 3 {
			t.Fatalf("%s generated gadget art = %#v, want three frames", product, entry)
		}
		r := generated.PlacedRect(button + 4)
		if !b.hud.consumeClick(b, r.X+r.W/2, r.Y+r.H/2) {
			t.Fatalf("%s generated product click was not consumed", product)
		}
		stepOnce()
	}
	if shot := os.Getenv("NANOLATHE_ARMLAB_PAGE2_SHOT"); shot != "" {
		writeRetailHUDShot(t, b, cs, cam, pal, 640, 480, shot)
	}

	queue := orders.QueueForUnit(lab)
	if queue == nil {
		t.Fatal("ARMLAB has no factory queue after generated product clicks")
	}
	found := map[string]bool{}
	for _, node := range queue.Primary() {
		if node != nil && node.Param2 != 0 {
			found[content.CanonicalKey(node.BuildDefKey)] = true
		}
	}
	for _, product := range want {
		if !found[product] {
			t.Fatalf("ARMLAB real primary queue lacks %s after click: %+v", product, queue.Primary())
		}
	}
}
