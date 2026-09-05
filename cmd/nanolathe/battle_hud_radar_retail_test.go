//go:build retail

package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// A radar tower must put an enemy it detects on the minimap in that enemy's own
// player colour, and must not put an undetected one there.
//
// The defect this locks against was never in the sim: the sensor phase set the seen bit and the publisher carried it, but
// the contacts pass resolved its unit blip from the FX bank's `radlogohigh`,
// which is one 6x6 frame. The owning-player frame selector of [03 §3.9] can
// only be satisfied by `radlogo`, whose ten frames are the ten player colours,
// so every owner but slot zero resolved no frame and drew nothing — the viewing
// player saw their own units and never an enemy, radar or no radar.
func TestRetailRadarPutsDetectedEnemyOnMinimapInOwnerColour(t *testing.T) {
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(7, 7)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}

	var cx, cy, cz numeric.Fixed
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != sess.LocalOwner {
			continue
		}
		cx, cy, cz = u.X, u.Y, u.Z
		break
	}
	if cx == 0 && cz == 0 {
		t.Fatal("the viewing player has no starting unit to build the radar beside")
	}
	radDef, ok := cat.Unit("armrad")
	if !ok {
		t.Fatal("armrad is not in the compiled catalog")
	}
	if radDef.RadarDistance <= 0 {
		t.Fatalf("armrad authors radardistance %d, want a positive emission range", radDef.RadarDistance)
	}
	rh, err := sess.Units.Create(radDef, uint8(sess.LocalOwner), cx+numeric.Fixed(64<<16), cy, cz)
	if err != nil {
		t.Fatal(err)
	}
	// A built radar is activated on completion behind `activatewhenbuilt`; this
	// one is placed rather than built, so the activation bit the emitter gate
	// reads is set directly [03 R-VIS-01 §4] pass 2.
	sess.Units.Unit(rh).Activated = true

	enemyDef, ok := cat.Unit("armpw")
	if !ok {
		t.Fatal("armpw is not in the compiled catalog")
	}
	var enemyOwner uint8
	for i := 0; i < 10; i++ {
		if sess.Econ.Players[i].Exists && uint8(i) != uint8(sess.LocalOwner) {
			enemyOwner = uint8(i)
			break
		}
	}
	if enemyOwner == uint8(sess.LocalOwner) {
		t.Skip("the composed skirmish has only one player; the sensor phase does not run")
	}
	// Inside the tower's radar circle, far outside anything's line of sight.
	inside := cx - numeric.Fixed(int64(radDef.RadarDistance/2)<<16)
	// Outside every sensor and every sight radius.
	outside := cx - numeric.Fixed(int64(radDef.RadarDistance*2)<<16)
	place := func(x numeric.Fixed) uint16 {
		y := sess.World.HeightAt(x, cz)
		if y == -1 {
			y = 0
		}
		h, err := sess.Units.Create(enemyDef, enemyOwner, x, y, cz)
		if err != nil {
			t.Fatal(err)
		}
		return uint16(h)
	}
	detected, undetected := place(inside), place(outside)

	for step := int32(1); step <= 60; step++ {
		sess.Step(step)
	}
	cur := sess.Snapshot.Current()
	if cur == nil {
		t.Fatal("no committed frame after sixty ticks")
	}
	var detectedView, undetectedView *frame.RadarContactView
	for i := range cur.Radar.Contacts {
		c := &cur.Radar.Contacts[i]
		if c.Kind != frame.RadarContactUnit {
			continue
		}
		switch uint16(c.Handle) {
		case detected:
			detectedView = c
		case undetected:
			undetectedView = c
		}
	}
	if detectedView == nil || undetectedView == nil {
		t.Fatal("the committed frame did not publish both enemy contacts")
	}
	if detectedView.Status&visibility.SeenBit == 0 {
		t.Fatalf("an enemy inside the radar circle carries status %#x, want the seen marker [03 R-VIS-01 §5]", detectedView.Status)
	}
	if undetectedView.Status&visibility.SeenBit != 0 {
		t.Fatalf("an enemy outside every sensor carries status %#x, want no seen marker [03 R-VIS-01 §5]", undetectedView.Status)
	}
	if !detectedView.PaletteKnown || detectedView.Palette == 0 {
		t.Fatalf("the detected enemy published palette %d (known %v); the blip needs the owning player's colour [03 §3.9]",
			detectedView.Palette, detectedView.PaletteKnown)
	}

	const winW, winH = 640, 480
	cam := &camera.Camera{ViewW: winW, ViewH: winH, MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}
	centerBattleStartCamera(sess, cam)
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	pal := retailPaletteForTest(t, cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The blip bank must be able to answer the owning-player selector for every
	// player slot; a bank with fewer frames silently drops every other owner.
	if got := radarGAFFrameCount(b.hud.radarBlipGAF); got < 10 {
		t.Fatalf("the unit blip bank carries %d frames, want one per player slot [03 §3.9]", got)
	}
	layout, _, ok := b.minimapLayout()
	if !ok {
		t.Fatal("the production HUD published no minimap layout")
	}
	final := b.hud.rebuildRadar(b, cur, layout)
	if final == nil {
		t.Fatal("the committed contacts did not rebuild the final surface")
	}
	playW, playH, ok := sess.PlayArea()
	if !ok {
		t.Fatal("no play area")
	}
	// The blip the owning-player selector resolves, sampled at the contact's
	// own projected centre.
	blip := radarGAFFrame(b.hud.radarBlipGAF, b.hud.radarOwnerFrameIndex(*detectedView, radarGAFFrameCount(b.hud.radarBlipGAF)))
	if blip == nil {
		t.Fatal("the owning-player selector resolved no blip frame for the detected enemy [03 §3.9]")
	}
	want, _ := blip.At(int(blip.XOffset), int(blip.YOffset))
	at := func(c *frame.RadarContactView) byte {
		rx, ry := render.RadarProjection(radarMapPixel(c.X), radarMapPixel(c.Z), radarMapPixel(c.Y), playW, playH, layout)
		v, _ := final.At(int(rx), int(ry))
		return v
	}
	if got := at(detectedView); got != want {
		t.Fatalf("the radar-detected enemy drew minimap pixel %d, want the owning player's blip %d [03 §3.9]", got, want)
	}
	if got := at(undetectedView); got == want {
		t.Fatalf("an undetected enemy drew a minimap blip [03 §3.9]")
	}
}
