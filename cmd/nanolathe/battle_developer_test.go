package main

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func developerToken(r rune) *input.State {
	return input.StateFromSample(input.Sample{ShortcutTokenMode: true, ShortcutToken: input.Token{Kind: input.TokenText, Rune: r}})
}
func developerFunction(key input.Key, shift bool) *input.State {
	return input.StateFromSample(input.Sample{ShortcutTokenMode: true, ShortcutToken: input.Token{Kind: input.TokenEdit, Key: key}, Modifiers: input.Modifiers{Shift: shift}})
}

func TestDeveloperAuthorizationAndFilmLifetimes(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		b := &battleSession{sess: &session.Session{Gameplay: mode}}
		b.dispatchLocalCommand("+nOw Film Chris Include Reload Assert # enabled")
		if !b.developer.authorized {
			t.Fatal("exact password not accepted")
		}
		b.handleDeveloperShortcuts(developerFunction(input.KeyF11, false), nil)
		b.handleDeveloperShortcuts(developerToken('i'), nil)
		b.handleDeveloperShortcuts(developerToken('m'), nil)
		if !b.developer.film || !b.developer.information || !b.developer.quickkeysDisabled || b.sess.DebugDisplayMode != 1 {
			t.Fatal("film controls not independent")
		}
		for _, r := range "IM" {
			b.handleDeveloperShortcuts(developerToken(r), nil)
		}
		if !b.developer.information || b.sess.DebugDisplayMode != 1 {
			t.Fatal("uppercase changed film display")
		}
		b.dispatchLocalCommand("+Now film Chris Include Reload Assert")
		b.handleDeveloperShortcuts(developerFunction(input.KeyF11, false), nil)
		if b.developer.authorized || !b.developer.film || !b.developer.information {
			t.Fatal("bad password changed film lifetime")
		}
		for i := 0; i < 4; i++ {
			b.handleDeveloperShortcuts(developerToken('m'), nil)
		}
		if b.sess.DebugDisplayMode != 0 {
			t.Fatal("mode wrap failed without authorization")
		}
		b.dispatchLocalCommand("+Now Film Chris Include Reload Assert")
		b.handleDeveloperShortcuts(developerFunction(input.KeyF11, false), nil)
		if b.developer.film || b.developer.information || b.developer.quickkeysDisabled || b.sess.DebugDisplayMode != 0 {
			t.Fatal("film exit failed")
		}
	}
}

func TestModernDevShortcutPreservesHistoricalAccessAndBattleState(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		b := &battleSession{sess: &session.Session{Gameplay: mode, Econ: &economy.Service{}}}
		b.sess.SeedSessionRNG(7, 11)
		b.sess.Econ.Players[0].Stock = [2]float32{123, 456}
		simBefore, crtBefore, econBefore := *b.sess.SimRNG(), *b.sess.CrtRNG(), *b.sess.Econ
		b.dispatchLocalCommand("+dev extra")
		if b.developer.authorized {
			t.Fatal("shortcut accepted arguments")
		}
		for _, command := range []string{"+dev", "+DEV"} {
			b.dispatchLocalCommand(command)
			if b.developer.authorized != (mode == gameplay.Modern) || b.developer.film {
				t.Fatalf("shortcut authorization/film state in %v: %+v", mode, b.developer)
			}
		}
		if *b.sess.SimRNG() != simBefore || *b.sess.CrtRNG() != crtBefore || !reflect.DeepEqual(*b.sess.Econ, econBefore) {
			t.Fatal("developer shortcut changed RNG or economy")
		}
		b.dispatchLocalCommand("+Now Film Chris Include Reload Assert")
		if !b.developer.authorized {
			t.Fatalf("historical access failed in %v", mode)
		}
	}
}

func TestDeveloperContourConversionAndReplay(t *testing.T) {
	b := &battleSession{}
	b.dispatchLocalCommand("+Contour 1.00390625 -0.00390625")
	if b.developer.contourSpacing != 257 || b.developer.contourOffset != -1 {
		t.Fatal("contour scale is not 1/256")
	}
	for _, bad := range []string{"-0.001 0", "-1 0", "NaN 0", "Inf 0", "999999999999 0"} {
		b.dispatchLocalCommand("+Contour " + bad)
		if b.developer.contourSpacing != 257 {
			t.Fatal("unsafe contour accepted")
		}
	}
	b.dispatchLocalCommand("+Now Film Chris Include Reload Assert")
	b.dispatchLocalCommand("+Contour 16 2")
	b.developer.contourSpacing = 0
	b.handleDeveloperShortcuts(developerToken('\\'), nil)
	if b.developer.contourSpacing != 4096 || b.chat.lastCommand != "Contour 16 2" {
		t.Fatal("replay lost retained command")
	}
	b.dispatchLocalCommand("+Contour")
	if b.developer.contourSpacing != 0 {
		t.Fatal("missing spacing should disable contours")
	}
}

func TestFilmQuickkeysYieldToResidualButChatOwnsText(t *testing.T) {
	b, cl, _ := paletteViewer(t, []gui.Gadget{{Kind: gui.KindButton, Name: "MOVE", Active: 1, QuickKey: 'M', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}}})
	b.dispatchLocalCommand("+Now Film Chris Include Reload Assert")
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyF11})
	b.viewerStep(0, cl)
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'm'})
	b.viewerStep(0, cl)
	if b.battleState().Input.Latch != input.LatchNormal || b.sess.DebugDisplayMode != 1 {
		t.Fatal("film key was claimed by palette")
	}
	// A modal owns function/text tokens; it must not cycle the film mode.
	b.battleState().OpenOptions()
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'm'})
	b.viewerStep(0, cl)
	if b.sess.DebugDisplayMode != 1 {
		t.Fatal("modal leaked developer token")
	}
}

func TestDeveloperProbeHotkeyRetainsIdentityAndNoHoverDisarms(t *testing.T) {
	b, cl, _ := paletteViewer(t, nil)
	publishPaletteFrame(t, b, func(f *frame.Frame) { f.Units = []frame.UnitView{{Slot: 3, InstanceID: 42}} })
	b.footerHoverUnit = 3
	b.handleDeveloperShortcuts(developerFunction(input.KeyF1, true), cl)
	target := b.developer.probes.State
	if !target.Enabled || target.InstanceID != 42 {
		t.Fatal("probe did not pin published identity")
	}
	b.handleDeveloperShortcuts(developerFunction(input.KeyF1, true), cl)
	if b.developer.probes.State != target {
		t.Fatal("repeat toggled target")
	}
	b.footerHoverUnit = 0
	b.handleDeveloperShortcuts(developerFunction(input.KeyF1, true), cl)
	if b.developer.probes.State.Enabled || b.developer.probes.State.InstanceID != 42 {
		t.Fatal("no hover must disable but retain target")
	}
}

func TestDeveloperPickMatchesTerrainInverse(t *testing.T) {
	terrain := testWorldON05(32, 32)
	terrain.SeaLevel = 20
	d := &frame.DeveloperView{Width: 32, Height: 32, SeaLevel: 20, Cells: make([]frame.DeveloperCell, 1024)}
	for z := int32(0); z < 32; z++ {
		for x := int32(0); x < 32; x++ {
			h := uint8((x*13 + z*7) % 120)
			terrain.Plot[z*32+x].SetHeight(h)
			d.Cells[z*32+x].Height = h
		}
	}
	b := &battleSession{cam: &camera.Camera{X: 32, Z: 48, ViewW: 640, ViewH: 480}}
	for _, zoom := range []camera.Zoom{camera.ZoomUnit, camera.ZoomUnit * 2, camera.ZoomUnit * 3 / 2} {
		b.cam.Zoom, b.cam.Scale = zoom, zoom.Step()
		for _, p := range [][2]int32{{128, 32}, {174, 66}, {211, 231}, {640, 448}} {
			x, z, ok := developerPick(b, d, p[0], p[1])
			if !ok {
				t.Fatal("pick unavailable")
			}
			px := b.cam.X + zoom.Inverse(p[0])
			pz := b.cam.Z + zoom.Inverse(p[1])
			wx, _, wz := terrain.CursorToWorld(px, pz)
			if x != int32(wx>>16) || z != int32(wz>>16) {
				t.Fatalf("point %v zoom %v got %d,%d want %d,%d", p, zoom, x, z, wx>>16, wz>>16)
			}
		}
	}
}

func TestDeveloperPickProjectsBackToFramebufferPointer(t *testing.T) {
	d := &frame.DeveloperView{Width: 128, Height: 128, Cells: make([]frame.DeveloperCell, 128*128)}
	for i := range d.Cells {
		d.Cells[i].Height = 40
	}
	b := &battleSession{cam: &camera.Camera{X: -64, Z: -16, ViewW: 640, ViewH: 480}}
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		b.cam.Scale = scale
		for _, p := range [][2]int32{{160, 96}, {384, 240}, {600, 400}} {
			x, z, ok := developerPick(b, d, p[0], p[1])
			if !ok {
				t.Fatal("pick unavailable")
			}
			sx, sy := b.cam.WorldToScreen(numeric.Fixed(x)<<16, numeric.Fixed(40)<<16, numeric.Fixed(z)<<16)
			if sx-camera.OriginX != p[0] || sy-camera.OriginY != p[1] {
				t.Fatalf("framebuffer pointer %v projected to %d,%d at scale %v", p, sx-camera.OriginX, sy-camera.OriginY, scale)
			}
		}
	}
}

func TestDeveloperModalClosesRestoreQuickkeys(t *testing.T) {
	for _, escape := range []bool{false, true} {
		b, in := battleOptionsAcceleratorFixture(nil)
		b.developer.film = true
		b.developer.quickkeysDisabled = true
		if escape {
			in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
			b.handleBattleMenuInput(in, nil)
		} else {
			b.activateBattleMenuButton("OK", nil)
		}
		if b.developer.quickkeysDisabled || !b.developer.film {
			t.Fatal("dialog close did not restore independent quickkey flag")
		}
	}
	b := &battleSession{}
	if b.configureDeveloperShot(Options{ShotContour: "-0.001"}) == nil {
		t.Fatal("negative sub-quantum contour accepted")
	}
}
