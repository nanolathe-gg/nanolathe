package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func TestPlaceBattleModalCentersOverRetailPlayfield(t *testing.T) {
	tests := []struct {
		name       string
		w, h, x, y int32
	}{
		{name: "exit menu", w: 150, h: 155, x: 309, y: 162},
		{name: "yes or no", w: 400, h: 100, x: 184, y: 190},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window := &gui.Window{
				Rect:    gui.Rect{X: 7, Y: 9, W: test.w, H: test.h, RawX: 7, RawY: 9},
				OriginX: 7,
				OriginY: 9,
				Gadgets: []gui.Gadget{{Rect: gui.Rect{X: 7, Y: 9, W: test.w, H: test.h}}},
			}
			placeBattleModal(window, 640, 480)
			if window.Rect.X != test.x || window.Rect.Y != test.y {
				t.Fatalf("origin = (%d,%d), want (%d,%d)", window.Rect.X, window.Rect.Y, test.x, test.y)
			}
			if window.Rect.RawX != 7 || window.Rect.RawY != 9 {
				t.Fatalf("authored origin was discarded: raw=(%d,%d)", window.Rect.RawX, window.Rect.RawY)
			}
		})
	}
}

// A re-placed modal carries its controls with it. Retail shifts the child
// gadget records by the window origin when the window opens, so the whole
// window moves as one body at any display mode; nothing here may leave a
// control behind at the authored 640x480 coordinates
// [07 "Retail frontend control activation and raster rules"][07 R-HUD-05].
func TestPlaceBattleModalCarriesGadgetsAtEveryDisplayMode(t *testing.T) {
	// EXITMENU's authored geometry: a 150x155 window whose three choices are
	// stored window-locally at x=16/15.
	const winW, winH = 150, 155
	local := []gui.Rect{{X: 16, Y: 20, W: 120, H: 20}, {X: 16, Y: 50, W: 120, H: 20}, {X: 15, Y: 123, W: 120, H: 20}}
	for _, mode := range []struct{ w, h int }{{640, 480}, {800, 600}, {1024, 768}, {1280, 1024}} {
		window := &gui.Window{
			Rect:    gui.Rect{X: 279, Y: 117, W: winW, H: winH, RawX: 279, RawY: 117},
			OriginX: 279,
			OriginY: 117,
			Gadgets: []gui.Gadget{{Rect: gui.Rect{X: 279, Y: 117, W: winW, H: winH}}},
		}
		for _, r := range local {
			window.Gadgets = append(window.Gadgets, gui.Gadget{Kind: gui.KindButton, Rect: r})
		}
		placeBattleModal(window, mode.w, mode.h)
		for i := 1; i < len(window.Gadgets); i++ {
			placed := window.PlacedRect(i)
			// The control keeps its authored offset from the window origin...
			if placed.X-window.Rect.X != local[i-1].X || placed.Y-window.Rect.Y != local[i-1].Y {
				t.Fatalf("%dx%d gadget %d offset = (%d,%d), want authored (%d,%d)",
					mode.w, mode.h, i, placed.X-window.Rect.X, placed.Y-window.Rect.Y, local[i-1].X, local[i-1].Y)
			}
			// ...and therefore stays inside the frame that moved.
			if placed.X < window.Rect.X || placed.Y < window.Rect.Y ||
				placed.X+placed.W > window.Rect.X+window.Rect.W ||
				placed.Y+placed.H > window.Rect.Y+window.Rect.H {
				t.Fatalf("%dx%d gadget %d at %+v escaped the window %+v", mode.w, mode.h, i, placed, window.Rect)
			}
		}
	}
}

// Retail texture-maps a gadget frame onto its authored rectangle in exactly
// one case — a blank surface (kind 6) holding a raw frame. Everything else is
// stamped at the gadget origin, which is why `ARMOPT.GUI`'s 128x362 `OPTBG`
// picture box must not stretch the 128x354 RLE plate behind the pause menu's
// buttons [07 "Retail frontend control activation and raster rules"]
// [07 R-WGT-01 §8].
func TestModalArtResamplesOnlyRawSurfaces(t *testing.T) {
	rect := gui.Rect{X: 0, Y: 128, W: 128, H: 362}
	rle := &formats.GAFFrame{Width: 128, Height: 354, Compressed: 1}
	raw := &formats.GAFFrame{Width: 128, Height: 354, Compressed: 0}
	tests := []struct {
		name  string
		kind  gui.Kind
		frame *formats.GAFFrame
		want  bool
	}{
		{name: "picture box with RLE plate", kind: gui.KindPicture, frame: rle},
		{name: "picture box with raw plate", kind: gui.KindPicture, frame: raw},
		{name: "button", kind: gui.KindButton, frame: raw},
		{name: "surface with RLE frame", kind: gui.KindSurface, frame: rle},
		{name: "surface with raw frame", kind: gui.KindSurface, frame: raw, want: true},
		{name: "surface already the authored size", kind: gui.KindSurface, frame: &formats.GAFFrame{Width: 128, Height: 362}},
		{name: "no art", kind: gui.KindSurface, frame: nil},
	}
	for _, test := range tests {
		if got := modalArtResampled(test.kind, test.frame, rect); got != test.want {
			t.Errorf("%s: resampled = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestBattleMenuPauseAndResume(t *testing.T) {
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}}
	b.openBattleMenu()
	if b.battleState().Modal() != ui.BattleModalOptions || !b.sess.Clock.Paused || !b.battleState().Paused() {
		t.Fatalf("open menu = state %d clock paused %v ui paused %v; want options and paused", b.battleState().Modal(), b.sess.Clock.Paused, b.battleState().Paused())
	}
	b.closeBattleMenu()
	if b.battleState().Modal() != ui.BattleModalClosed || b.sess.Clock.Paused || b.battleState().Paused() {
		t.Fatalf("close menu = state %d clock paused %v ui paused %v; want closed and running", b.battleState().Modal(), b.sess.Clock.Paused, b.battleState().Paused())
	}
}

func TestBattleMenuPauseTruthSurvivesStaleCommittedFrame(t *testing.T) {
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}}
	b.openBattleMenu()
	// No tick is published while paused; closing the menu must still use the
	// canonical UI truth and synchronously resume the session.
	if !b.battleState().Paused() || !b.sess.Clock.Paused {
		t.Fatal("opening options did not synchronously pause")
	}
	b.closeBattleMenu()
	if b.battleState().Paused() || b.sess.Clock.Paused {
		t.Fatal("closing options did not synchronously unpause")
	}
}

func TestBattleMenuExitGameConfirmationRequestsTermination(t *testing.T) {
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}}
	cl, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b.openBattleMenu()
	b.activateBattleMenuButton("EXIT", cl)
	if b.battleState().Modal() != ui.BattleModalExit {
		t.Fatalf("EXIT state = %d; want exit menu", b.battleState().Modal())
	}
	b.activateBattleMenuButton("EXITGAME", cl)
	if b.battleState().Modal() != ui.BattleModalConfirmExit {
		t.Fatalf("EXITGAME state = %d; want confirmation", b.battleState().Modal())
	}
	b.activateBattleMenuButton("CHOICE1", cl)
	if !cl.ExitRequested() {
		t.Fatal("Yes did not request client termination")
	}
}

func TestBattleMenuMainMenuConfirmationInvokesReturn(t *testing.T) {
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}}
	returned := false
	b.returnToMenu = func(*client.Client) { returned = true }
	b.openBattleMenu()
	b.activateBattleMenuButton("EXIT", nil)
	b.activateBattleMenuButton("MAINMENU", nil)
	if b.battleState().Modal() != ui.BattleModalConfirmMain {
		t.Fatalf("MAINMENU state = %d; want main-menu confirmation", b.battleState().Modal())
	}
	b.activateBattleMenuButton("CHOICE1", nil)
	if !returned {
		t.Fatal("Yes did not invoke frontend return")
	}
}

func TestBattleMenuTabCloseConsumesClosingFrame(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.openBattleMenu()
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatal("test setup did not open options modal")
	}
	cl, err := client.New(client.Options{
		Buffer: b.sess.Snapshot,
		Width:  640,
		Height: 480,
	})
	if err != nil {
		t.Fatal(err)
	}
	in := cl.Input()
	in.Kbd.SetKey(input.KeyTab, true)
	in.Kbd.SetKey(input.KeyM, true)
	in.Mouse.SetPosition(200, 200)
	in.Mouse.SetButton(input.MouseButtonLeft, true)

	// The frame starts modal, so Tab closes ARMOPT but the simultaneous M and
	// mouse edges must not arm an order or enqueue a world action [07 §2][07 §3].
	b.viewerStep(0, cl)
	if b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatalf("Tab did not close options modal: %d", b.battleState().Modal())
	}
	if b.battleState().Input.Latch != input.LatchNormal {
		t.Fatalf("closing modal frame leaked M hotkey and armed latch %d", b.battleState().Input.Latch)
	}
	if got := b.sess.PendingHumanCommands(); len(got) != 0 {
		t.Fatalf("closing modal frame leaked %d human commands", len(got))
	}
}

// A loaded clock can be paused without an options window. Input must work
// before a draw synchronizes the overlay, and the resume edge owns the frame.
func TestInstalledPausedBattleResumesWithOneTabBeforeDraw(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  input.Key
	}{{"Tab", input.KeyTab}, {"F2", input.KeyF2}, {"Pause", input.KeyPause}} {
		t.Run(tc.name, func(t *testing.T) {
			key := tc.key
			b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
			b.hud = &retailBattleHUD{}
			b.sess.Clock.Paused = true
			cl := b.cl
			installBattleClient(cl, b)
			if !b.battleState().Paused() {
				t.Fatal("install did not initialize the saved pause truth")
			}
			cl.Input().Kbd.SetKey(key, true)
			if key == input.KeyTab {
				cl.Input().Kbd.SetKey(input.KeyM, true)
				cl.Input().Mouse.SetPosition(200, 200)
				cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
			}
			b.viewerStep(0, cl)
			if key == input.KeyF2 {
				if !b.sess.Clock.Paused || b.battleState().Modal() != ui.BattleModalOptions {
					t.Fatal("F2 did not retain access to the paused options window")
				}
				return
			}
			if b.sess.Clock.Paused || b.battleState().Paused() || b.battleState().Modal() != ui.BattleModalClosed {
				t.Fatal("one press did not resume the paused battle")
			}
			if b.battleState().Input.Latch != input.LatchNormal || len(b.sess.PendingHumanCommands()) != 0 {
				t.Fatal("resume frame leaked unrelated world input")
			}
		})
	}
}
