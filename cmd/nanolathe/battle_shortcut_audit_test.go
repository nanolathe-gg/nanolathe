package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The presentation extensions must not claim the composed retail developer
// tokens [07 R-CAM-01 §2].
func TestCtrlFunctionKeysDoNotChangeRendererOrZoom(t *testing.T) {
	b := zoomTestBattle()
	cl, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	before := b.cam.Zoom
	for _, key := range []input.Key{input.KeyF9, input.KeyF10} {
		in := input.NewState()
		in.Kbd.SetKey(input.KeyCtrl, true)
		in.Kbd.SetKey(key, true)
		b.handleInput(in, cl)
	}
	if b.cam.Zoom != before || cl.RendererToggleCount() != 0 {
		t.Fatal("Ctrl function key triggered a plain presentation shortcut")
	}
}

func TestBattleQueuedSpeedRepeatsRunOncePerFrame(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	in := b.cl.Input()
	in.ShortcutTokenMode = true
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: '='})
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: '='})
	for step := 1; step <= 3; step++ {
		b.viewerStep(0, b.cl)
		if got, want := b.sess.Clock.Requested, int32(10+min(step, 2)); got != want {
			t.Fatalf("frame %d speed=%d, want %d", step, got, want)
		}
	}
}

func TestQueuedModalCloseDoesNotReopenOnNextFrame(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.openBattleMenu()
	in := b.cl.Input()
	in.ShortcutTokenMode = true
	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	for step := 0; step < 2; step++ {
		b.viewerStep(0, b.cl)
		if b.battleState().Modal() != ui.BattleModalClosed || in.PendingTokens() != 0 {
			t.Fatalf("frame %d retained modal close token", step)
		}
	}
}

func TestUnclaimedModalTokenDoesNotBlockQueuedEscape(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.openBattleMenu()
	in := b.cl.Input()
	in.ShortcutTokenMode = true
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'x'})
	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	b.viewerStep(0, b.cl)
	if b.battleState().Modal() != ui.BattleModalOptions || in.PendingTokens() != 1 {
		t.Fatal("modal did not retire only its unclaimed first token")
	}
	b.viewerStep(0, b.cl)
	if b.battleState().Modal() != ui.BattleModalClosed || in.PendingTokens() != 0 {
		t.Fatal("unclaimed modal token blocked the later Escape")
	}
}

// Pointer capture spends its motion before the residual bookmark recall
// [07 R-CAM-01 §1], including frames whose pointer path returns early.
func TestQueuedBookmarkWinsAfterCapturedCameraMotion(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.cam = &camera.Camera{X: 500, Z: 500, ViewW: 640, ViewH: 480, MapW: 4000, MapH: 4000}
	b.cam.StoreBookmark(0)
	b.cam.X, b.cam.Z = 100, 100
	b.beginDragScroll(300, 200, b.cl)
	in := input.NewState()
	in.Mouse.SetPosition(310, 210)
	in.Mouse.SetButton(input.MouseButtonRight, true)
	in.ShortcutTokenMode = true
	in.ShortcutToken = input.Token{Kind: input.TokenEdit, Key: input.KeyF5}
	b.handleInput(in, b.cl)
	if b.cam.X != 500 || b.cam.Z != 500 {
		t.Fatalf("captured camera swallowed or overwrote bookmark: %d,%d", b.cam.X, b.cam.Z)
	}
}

// Token identity and live modifier state serve different consumers [07 §2].
func TestBattleLiteralDigitRetainsLiveAdditiveRecall(t *testing.T) {
	b := newTestBattle(hotkeyCatalog(t), testWorldON05(40, 40))
	first := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	second := placeUnit(b, "armsolar", numeric.Fixed(320*65536), numeric.Fixed(220*65536))
	replaceSelectionForTest(t, b, first)
	pressKeys(b, input.KeyCtrl, input.Key1)
	replaceSelectionForTest(t, b, second)
	b.switchAlt = true
	in := input.NewState()
	in.Kbd.SetKey(input.KeyShift, true)
	in.ShortcutTokenMode = true
	in.ShortcutToken = input.Token{Kind: input.TokenText, Rune: '1'}
	b.handleInput(in, nil)
	applyPendingBattleCommands(b)
	got := selectedHandles(t, b)
	if len(got) != 2 || !containsHandle(got, first.Handle) || !containsHandle(got, second.Handle) {
		t.Fatalf("queued digit with live Shift selected %v, want additive recall", got)
	}
}
