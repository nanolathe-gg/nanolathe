//go:build retail

package main

// Visual evidence for WU-16-6: the ordinary footer's three hover sources
// [07 R-HUD-03 §1-§3], composed headlessly through the production draw loop.
// Each case sets the pointer, runs the same per-frame pointer pass the battle
// loop runs, and writes one PNG when its environment variable is set.

import (
	"image/png"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const footerShotW, footerShotH = 640, 480

// footerShotSession builds a battle with the local commander selected and
// steps far enough for at least one 30-tick settlement pass to have written
// the archived rate slots the footer reads [05 R-ECO-01 §5].
func footerShotSession(t *testing.T) (*battleSession, *contentSet, *camera.Camera, *palette.Tables) {
	t.Helper()
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		cs.Close()
		t.Fatal(err)
	}
	for _, u := range sess.Units.Iter() {
		if u == nil {
			continue
		}
		u.Flags &^= hud.SelectionFlag
		if u.Owner == sess.LocalOwner && u.Def != nil && u.Def.Builder && u.Def.CanMove {
			u.Flags |= hud.SelectionFlag
		}
	}
	cam := &camera.Camera{
		ViewW: footerShotW, ViewH: footerShotH,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16),
	}
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	step := int32(1)
	for ; step <= 60; step++ {
		sess.Step(step)
	}
	// Give the commander a current order so the caption anchor shows an order's
	// own state label. With no head order record the footer falls back to the
	// order table's row-0 label, which is `Ready` — retail's caption for an
	// order-free unit, not an empty string [07 R-HUD-03 §2][04 §3.1].
	for _, u := range sess.Units.Iter() {
		if u == nil || u.Owner != sess.LocalOwner || u.Flags&hud.SelectionFlag == 0 {
			continue
		}
		if err := b.DispatchOrderCommand(session.HumanOrderCommand{
			Code:     hud.LatchToCode(input.LatchMove),
			Position: orders.ResolvePos{X: u.X + numeric.FixedFromInt(512), Y: u.Y, Z: u.Z},
		}); err != nil {
			t.Fatalf("move order: %v", err)
		}
		break
	}
	for ; step <= 100; step++ {
		sess.Step(step)
	}
	centerBattleStartCamera(sess, cam)
	pal := retailPaletteForTest(t, cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, newBattleWindowContext(cs, nil))
	if err != nil {
		cs.Close()
		t.Fatal(err)
	}
	return b, cs, cam, pal
}

// footerComposeShot points the software cursor at (mx,my), runs the pointer
// pass, and writes the composed frame.
func footerComposeShot(t *testing.T, b *battleSession, cs *contentSet, cam *camera.Camera, pal *palette.Tables, mx, my int32, path string) {
	t.Helper()
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: footerShotW, Height: footerShotH})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(cam)
	cl.SetPalette(pal)
	cl.SetFNT(b.hud.console)
	cl.SetModelFS(cs.unmappedMount)
	cl.Input().Mouse.SetPosition(float32(mx), float32(my))
	b.updateFooterHover(mx, my)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := encodeShot(file, cl); err != nil {
		t.Fatal(err)
	}
}

// TestRetailFooterHoverShots is the visual gate for WU-16-6. Set
// NANOLATHE_HUD_SHOT to a path prefix; three PNGs are written next to it.
func TestRetailFooterHoverShots(t *testing.T) {
	prefix := os.Getenv("NANOLATHE_HUD_SHOT")
	if prefix == "" {
		t.Skip("set NANOLATHE_HUD_SHOT to a path prefix to capture the footer shots")
	}
	b, cs, cam, pal := footerShotSession(t)
	defer cs.Close()
	cur := b.sess.Snapshot.Current()
	if cur == nil {
		t.Fatal("no committed frame")
	}

	// (a) the commander: name, damage bar, archived rates and caption.
	var commander *frame.UnitView
	for i := range cur.Units {
		v := &cur.Units[i]
		if v.Owner == b.sess.LocalOwner && v.Flags&hud.SelectionFlag != 0 {
			commander = v
			break
		}
	}
	if commander == nil {
		t.Fatal("local commander is not in the committed frame")
	}
	p := client.NewViewportTransform(cam, 0, 0).WorldToSurface(commander.X, commander.Y, commander.Z)
	footerComposeShot(t, b, cs, cam, pal, p.X, p.Y, prefix+"-unit.png")
	t.Logf("unit hover at (%d,%d): handle=%d kills=%d archived M+%v E+%v M-%v E-%v",
		p.X, p.Y, b.footerHoverUnit, commander.Kills,
		commander.ArchivedMetalMake, commander.ArchivedEnergyMake,
		commander.ArchivedMetalUse, commander.ArchivedEnergyUse)

	// (b) a build button on the commander's authored page.
	window, _, err := b.hud.windowForRequired(b, cur)
	if err != nil {
		t.Fatalf("command window: %v", err)
	}
	if window == nil {
		t.Fatal("commander page did not open")
	}
	var bx, by int32
	var gadgetName string
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Kind != gui.KindButton || gad.Active == 0 {
			continue
		}
		if _, ok := b.cat.Unit(content.CanonicalKey(gad.Name)); !ok {
			continue
		}
		r := window.PlacedRect(i)
		bx, by = r.X+r.W/2, r.Y+r.H/2
		gadgetName = gad.Name
		break
	}
	if gadgetName == "" {
		t.Fatal("commander page has no product gadget resolving to a definition")
	}
	footerComposeShot(t, b, cs, cam, pal, bx, by, prefix+"-build.png")
	def, _ := b.cat.Unit(content.CanonicalKey(gadgetName))
	t.Logf("build card gadget %q at (%d,%d): %q  M:%.0f E:%.0f / %q",
		gadgetName, bx, by, def.Name, def.BuildCostMetal, def.BuildCostEnergy, def.Description)

	// (c) a metal deposit: the feature line with its authored M: amount.
	fx, fy, key := int32(-1), int32(-1), ""
	for _, fv := range cur.Features {
		fd := b.cat.Features[content.CanonicalKey(fv.DefName)]
		if fd == nil || fd.Metal == 0 || fd.NoDisplayInfo {
			continue
		}
		q := client.NewViewportTransform(cam, 0, 0).WorldToSurface(fv.X, fv.Y, fv.Z)
		if !b.hud.overWorld(q.X, q.Y) {
			continue
		}
		// Hovering a unit outranks a feature; pick a cell with no unit on it.
		if handle, _, hit := client.PickSnapshotUnit(cur, q.X, q.Y, cam, b.sess.LocalOwner); hit && handle != 0 {
			continue
		}
		fx, fy, key = q.X, q.Y, fv.DefName
		break
	}
	if key == "" {
		t.Skip("no on-screen metal deposit near the commander to hover")
	}
	// The unit word is kept until the pointer re-enters the view over another
	// unit, so clear it the way moving onto empty ground does.
	b.footerHoverUnit = 0
	footerComposeShot(t, b, cs, cam, pal, fx, fy, prefix+"-feature.png")
	fd := b.cat.Features[content.CanonicalKey(key)]
	t.Logf("feature hover %q at (%d,%d): %q M:%d E:%d indestructible=%v cell=(%d,%d)",
		key, fx, fy, fd.Description, fd.Metal, fd.Energy, fd.Indestructible,
		world.WorldToCell(0), world.WorldToCell(0))
	if !strings.EqualFold(b.footerHoverFeature, key) {
		t.Errorf("hovered feature = %q, want %q", b.footerHoverFeature, key)
	}
}

// encodeShot writes one composed frame as a PNG.
func encodeShot(w io.Writer, cl *client.Client) error {
	return png.Encode(w, cl.ComposeFrame())
}
