//go:build retail

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestRetailFactoryProductClickQueuesAndBuilds locks the skirmish play-test
// flow: select a kbot lab, click its authored Peewee build button, and the
// product must queue on the factory and eventually roll off the exit line.
// It pins the two contracts this flow depends on: the factory's authored
// CanMove=1 must not classify it as a mobile builder (the building class is
// the authored bmcode [08 "Classifier eligibility, destinations, and order"]),
// and the retail compiler's engine-write operand order [R-P0-10].
func TestRetailFactoryProductClickQueuesAndBuilds(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("retail assets unavailable: %v", err)
		}
		root = home + "/TotalAnnihilation"
	}
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

	labDef, ok := cat.Unit("armlab")
	if !ok || labDef == nil || !labDef.Builder {
		t.Fatal("retail catalog has no armlab builder")
	}
	if labDef.CanMove {
		// The stock Kbot Lab authors CanMove=1 with BMcode=0; the factory/mobile
		// distinction must come from the bmcode-derived building-class bit, not
		// from mobility [05 "Factory production lifecycle"].
		t.Log("stock armlab authors CanMove=1 (BMcode=0); factory classification must be bmcode-driven")
	}
	var spawnX, spawnZ numeric.Fixed
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == sess.LocalOwner && u.Def != nil && strings.HasSuffix(strings.ToLower(u.Def.UnitName), "com") {
			spawnX, spawnZ = u.X, u.Z
			break
		}
	}
	if spawnX == 0 && spawnZ == 0 {
		t.Fatal("no local commander to place the lab beside")
	}
	labHandle, err := sess.Units.Create(labDef, sess.LocalOwner, spawnX, numeric.Fixed(0), spawnZ)
	if err != nil {
		t.Fatal(err)
	}
	lab := sess.Units.Unit(labHandle)
	if lab == nil {
		t.Fatal("lab not in pool")
	}
	for _, u := range sess.Units.Iter() {
		if u == nil {
			continue
		}
		u.Flags &^= hud.SelectionFlag
		if u.Handle == labHandle {
			u.Flags |= hud.SelectionFlag
		}
	}
	for step := int32(31); step <= 60; step++ {
		sess.Step(step)
	}
	cur := sess.Snapshot.Current()
	if cur == nil {
		t.Fatal("no published frame")
	}
	if cur.CommandPage.Builder != labHandle {
		t.Fatalf("CommandPage.Builder = %d, want lab %d; page=%+v", cur.CommandPage.Builder, labHandle, cur.CommandPage)
	}

	const winW, winH = 640, 480
	cam := &camera.Camera{
		ViewW: winW, ViewH: winH,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16),
	}
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	pal := loadPalette(cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := b.hud.windowFor(b, cur)
	if w == nil || !strings.HasSuffix(strings.ToLower(w.Name), "armlab1.gui") {
		if w == nil {
			t.Fatal("lab window is nil; want armlab1.gui")
		}
		t.Fatalf("lab window = %q; want suffix armlab1.gui", w.Name)
	}

	clicked := ""
	var clickX, clickY int32
	for i, gad := range w.Gadgets {
		if i == 0 || gad.Kind != gui.KindButton || gad.Active == 0 || gad.GrayedOut != 0 {
			continue
		}
		candidates := append([]string{gad.Name, gad.Text}, gad.Labels...)
		for _, cand := range candidates {
			if cand == "" {
				continue
			}
			// The Peewee button is authored as ARMPW.
			if !strings.EqualFold(content.CanonicalKey(cand), "armpw") {
				continue
			}
			r := w.PlacedRect(i)
			clickX, clickY = r.X+r.W/2, r.Y+r.H/2
			clicked = cand
			break
		}
		if clicked != "" {
			break
		}
	}
	if clicked == "" {
		t.Fatal("armlab page has no authored Peewee button")
	}
	if !b.hud.sameButton(b, clickX, clickY, clickX, clickY) {
		t.Fatal("product button not hit at its own rect center")
	}
	if !b.hud.consumeClick(b, clickX, clickY) {
		t.Fatal("product click not consumed by HUD")
	}
	if b.battleState().Input.BuildDef != "" {
		t.Fatalf("factory product click must not arm placement, got buildDef %q", b.battleState().Input.BuildDef)
	}
	sess.Step(61)
	q := orders.QueueForUnit(lab)
	if q == nil || q.LenPrimary() == 0 {
		t.Fatalf("factory queue empty after product click; pending=%v", sess.PendingHumanCommands())
	}
	tail := q.Primary()[q.LenPrimary()-1]
	if tail.BuildDefKey != "armpw" {
		t.Fatalf("factory queue tail product = %q, want armpw", tail.BuildDefKey)
	}
	// Run production: a fresh peewee must eventually roll off the exit line.
	built := pool.Handle(0)
	for tick := int32(62); tick <= 1500; tick++ {
		sess.Step(tick)
		for _, u := range sess.Units.Iter() {
			if u == nil || !u.Alive || u.Owner != sess.LocalOwner {
				continue
			}
			if strings.EqualFold(u.Def.UnitName, "armpw") {
				built = u.Handle
			}
		}
		if built != 0 {
			break
		}
	}
	if built == 0 {
		t.Fatalf("no peewee produced within 1500 ticks; head phase=%d gate=%x", q.Head().Phase, q.Head().DynamicGate)
	}
}
