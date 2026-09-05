package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/ui"
)

func TestGameSpeedKeysClampAndMessage(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.Clock = &clock.State{Requested: 10, Active: 10}
	b.sess.Snapshot = &frame.Buffer{}
	// need snapshot for hasSelection? not needed
	// Decrease below 1 should clamp at 1 [07 §11][07 §2]
	b.sess.Clock.Requested = 1
	b.adjustGameSpeed(-1)
	if b.sess.Clock.Requested != 1 {
		t.Fatalf("speed clamp low want 1 got %d", b.sess.Clock.Requested)
	}
	// Increase above 20 should clamp at 20
	b.sess.Clock.Requested = 20
	b.sess.Clock.Active = 20
	b.adjustGameSpeed(1)
	if b.sess.Clock.Requested != 20 {
		t.Fatalf("speed clamp high want 20 got %d", b.sess.Clock.Requested)
	}
	// Normal -> 11 should produce Game Speed message +1
	b.sess.Clock.Requested = 10
	b.sess.Clock.Active = 10
	b.sess.Clock.GlobalTick = 100
	b.adjustGameSpeed(1)
	if b.sess.Clock.Requested != 11 {
		t.Fatalf("speed increase want 11 got %d", b.sess.Clock.Requested)
	}
	if newestRingText(b) != "Game Speed  +1" {
		t.Fatalf("speed message want 'Game Speed  +1' got %q", newestRingText(b))
	}
	// 10 -> Normal
	b.sess.Clock.Requested = 11
	b.sess.Clock.Active = 11
	b.adjustGameSpeed(-1)
	if b.sess.Clock.Requested != 10 {
		t.Fatalf("speed decrease to 10 want 10 got %d", b.sess.Clock.Requested)
	}
	if newestRingText(b) != "Game Speed Normal" {
		t.Fatalf("speed normal message want 'Game Speed Normal' got %q", newestRingText(b))
	}
	// Via handleInput: test that +/- keys are wired [07 §2]
	b.sess.Clock.Requested = 10
	b.sess.Clock.Active = 10
	b.sess.Clock.GlobalTick = 200
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Kbd.SetKey(input.KeyEqual, true)
	b.handleInput(in, nil)
	if b.sess.Clock.Requested != 11 {
		t.Fatalf("handleInput Equal should increase speed to 11 got %d", b.sess.Clock.Requested)
	}
	in.Kbd.ResetEdges()
	in.Kbd.SetKey(input.KeyEqual, false)
	in.Kbd.SetKey(input.KeyMinus, true)
	b.handleInput(in, nil)
	if b.sess.Clock.Requested != 10 {
		t.Fatalf("handleInput Minus should decrease to 10 got %d", b.sess.Clock.Requested)
	}
	in.Kbd.ResetEdges()
	// Numpad variants
	in.Kbd.SetKey(input.KeyNumpadAdd, true)
	b.handleInput(in, nil)
	if b.sess.Clock.Requested != 11 {
		t.Fatalf("NumpadAdd should increase to 11 got %d", b.sess.Clock.Requested)
	}
	in.Kbd.ResetEdges()
	in.Kbd.SetKey(input.KeyNumpadSubtract, true)
	b.handleInput(in, nil)
	if b.sess.Clock.Requested != 10 {
		t.Fatalf("NumpadSubtract should decrease to 10 got %d", b.sess.Clock.Requested)
	}
}

func TestPauseToggleUsesAuthoredOverlay(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.Clock = &clock.State{Requested: 10, Active: 10, Paused: false}
	b.sess.Snapshot = frame.NewBuffer()
	b.sess.Clock.GlobalTick = 50
	committed := b.sess.Snapshot.BeginWrite()
	committed.Paused = false
	if err := b.sess.Snapshot.Publish(50); err != nil {
		t.Fatalf("publish running frame: %v", err)
	}
	// The committed frame is the input source. Make live state disagree before
	// each toggle so a live Clock.Paused read would choose the wrong intent.
	b.sess.Clock.Paused = true
	b.togglePause()
	if !b.sess.Clock.Paused {
		t.Fatalf("pause toggle should set Paused true")
	}
	if newestRingText(b) != "" {
		t.Fatalf("pause must not invent a status string, got %q", newestRingText(b))
	}
	committed = b.sess.Snapshot.BeginWrite()
	committed.Paused = true
	if err := b.sess.Snapshot.Publish(51); err != nil {
		t.Fatalf("publish paused frame: %v", err)
	}
	b.sess.Clock.Paused = false
	b.togglePause()
	if b.sess.Clock.Paused {
		t.Fatalf("second toggle should clear Paused")
	}
	if newestRingText(b) != "" {
		t.Fatalf("resume must not invent a status string, got %q", newestRingText(b))
	}
	// Via handleInput
	committed = b.sess.Snapshot.BeginWrite()
	committed.Paused = false
	if err := b.sess.Snapshot.Publish(52); err != nil {
		t.Fatalf("publish running frame for input: %v", err)
	}
	b.sess.Clock.Paused = true
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Kbd.SetKey(input.KeyPause, true)
	b.handleInput(in, nil)
	if !b.sess.Clock.Paused {
		t.Fatalf("handleInput Pause should toggle")
	}
}

func TestPauseToggleUsesCanonicalTruthWithoutNewSnapshot(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.sess.Clock = &clock.State{Requested: 10, Active: 10}
	b.sess.Snapshot = frame.NewBuffer()
	committed := b.sess.Snapshot.BeginWrite()
	committed.Paused = false
	if err := b.sess.Snapshot.Publish(1); err != nil {
		t.Fatalf("publish running frame: %v", err)
	}
	b.togglePause()
	if !b.battleState().Paused() || !b.sess.Clock.Paused {
		t.Fatal("pause toggle did not update canonical and session pause state")
	}
	// Paused simulation emits no new frame. The second toggle must use the UI
	// truth rather than the stale running committed frame.
	b.togglePause()
	if b.battleState().Paused() || b.sess.Clock.Paused {
		t.Fatal("pause toggle inverted from stale committed frame")
	}
}

func TestPauseOverlayUsesCanonicalTruthBeforeNextPublication(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.sess.Clock = &clock.State{Requested: 10, Active: 10}
	b.sess.Snapshot = frame.NewBuffer()
	committed := b.sess.Snapshot.BeginWrite()
	committed.Paused = false
	if err := b.sess.Snapshot.Publish(1); err != nil {
		t.Fatalf("publish running frame: %v", err)
	}
	b.togglePause()
	if !pauseOverlayVisible(b, b.sess.Snapshot.Current()) {
		t.Fatal("paused overlay did not appear immediately from canonical UI truth")
	}
	// Current remains the running frame because the paused scheduler runs no
	// tick. A second intent still hides the overlay synchronously.
	b.togglePause()
	if pauseOverlayVisible(b, b.sess.Snapshot.Current()) {
		t.Fatal("paused overlay remained after synchronous unpause")
	}
}

func TestESCMenuTokenPath(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.Clock = &clock.State{Requested: 10, Active: 10}
	b.sess.Snapshot = &frame.Buffer{}
	b.battleState().Input.Latch = input.LatchNormal
	b.battleState().Input.BuildDef = ""
	// ESC when latch normal and not placing should open menu [07 §2]
	cl, _ := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	cl.SetCamera(b.cam)
	// Simulate viewerStep ESC handling
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Kbd.SetKey(input.KeyEscape, true)
	// viewerStep logic: if latch normal and buildDef empty, ESC opens menu
	if b.battleState().Input.Latch == input.LatchNormal && b.battleState().Input.BuildDef == "" {
		b.openBattleMenu()
	}
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatalf("ESC should open battle menu when idle, got %v", b.battleState().Modal())
	}
	// ESC when menu open should be handled by handleBattleMenuInput (close or back)
	// Simulate second ESC to close options
	b.handleBattleMenuInput(in, cl)
	// After handling ESC in menu, it should close (since options -> closed)
	if b.battleState().Modal() != ui.BattleModalClosed {
		// handleBattleMenuInput with ESC when options should close
		// We already injected Escape, but need to call again with fresh edge
		in2 := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
		in2.Kbd.SetKey(input.KeyEscape, true)
		b.handleBattleMenuInput(in2, cl)
		if b.battleState().Modal() != ui.BattleModalClosed {
			t.Fatalf("ESC in menu should close, got %v", b.battleState().Modal())
		}
	}
	// ESC when latch armed should disarm, not open menu [07 §9]
	b.battleState().Input.Latch = input.LatchMove
	b.battleState().Input.BuildDef = ""
	in3 := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in3.Kbd.SetKey(input.KeyEscape, true)
	b.handleInput(in3, nil)
	if b.battleState().Input.Latch != input.LatchNormal {
		t.Fatalf("ESC should disarm latch, got %v", b.battleState().Input.Latch)
	}
	if b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatalf("ESC disarm should not open menu when latch was armed")
	}
	// Now second ESC after disarm should open menu
	in4 := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in4.Kbd.SetKey(input.KeyEscape, true)
	// viewerStep would open
	if b.battleState().Input.Latch == input.LatchNormal && b.battleState().Input.BuildDef == "" {
		b.openBattleMenu()
	}
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatalf("second ESC after disarm should open menu")
	}
}
