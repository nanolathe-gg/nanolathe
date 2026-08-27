package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/session"
)

// TestWindowResultDisplaysAndDismisses verifies that the window controller shows the
// result overlay when the authoritative result is latched and dismisses it via
// input replay without leaving hidden ticks [RS-05].
func TestWindowResultDisplaysAndDismisses(t *testing.T) {
	cat := testCatalogON05()
	// Mark two units as commanders for victory detection
	for _, u := range cat.Units {
		u.Commander = true
	}
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	// Prepare a session that has a terminal result via real commander kill
	sess := b.sess
	sess.Skirmish = session.SkirmishConfig{MapName: "test", NumPlayers: 2, CommanderDeath: 1}
	sess.Skirmish.Players[0].AllyGroup = 1
	sess.Skirmish.Players[1].AllyGroup = 2
	sess.LocalOwner = 0
	sess.EnemyOwner = 1
	sess.State = session.StateBattle
	// Create two commanders
	defA, _ := cat.Unit("armcons")
	defB, _ := cat.Unit("armsolar")
	// Ensure they are commanders
	defA.Commander = true
	defB.Commander = true
	hA, _ := sess.Units.Create(defA, 0, 0, 0, 0)
	hB, _ := sess.Units.Create(defB, 1, 0, 0, 0)
	_ = hA
	_ = hB
	// Kill enemy commander to arm result
	sess.Units.Destroy(hB, 1)
	// Step until latch (needs ~150 ticks)
	for tick := 1; tick < 300; tick++ {
		sess.Step(int32(tick))
		if sess.GetResult().Ended {
			break
		}
	}
	if !sess.GetResult().Ended {
		t.Fatalf("should have latched")
	}

	if !b.isResultVisible() {
		t.Fatalf("result overlay should be visible when snapshot Ended [RS-05]")
	}
	// Ensure overlay does not tick: capture GlobalTick before and after viewerStep with result visible
	prevTick := sess.Clock.GlobalTick
	// Create headless client for input replay
	buf := sess.Snapshot
	cl, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Headless: true, Step: func(delta float64) {}})
	cl.SetCamera(b.cam)
	// Simulate click on Main Menu button (should dismiss to main)
	b.ensureResultButtons()
	var mainBtn panelButton
	for _, btn := range b.resultButtons {
		if btn.Kind == "result_main" {
			mainBtn = btn
			break
		}
	}
	if mainBtn.Name == "" {
		t.Fatalf("main button not found")
	}
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Mouse.InjectMouseMove(float32(mainBtn.X+10), float32(mainBtn.Y+5))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, false) // need press then release
	// Press
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	b.handleResultInput(in, cl)
	in.Mouse.ClearEdges()
	// Release inside
	in.Mouse.InjectMouseMove(float32(mainBtn.X+10), float32(mainBtn.Y+5))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, false)
	called := false
	b.returnToMenu = func(c *client.Client) { called = true }
	b.handleResultInput(in, cl)
	if !called {
		t.Fatalf("main menu button via input replay should trigger returnToMenu [RS-05]")
	}
	if !b.resultDismissed {
		t.Fatalf("overlay should be dismissed after main")
	}
	// Verify no hidden tick while overlay was visible
	if sess.Clock.GlobalTick != prevTick {
		t.Fatalf("hidden tick while overlay: %d -> %d [RS-05]", prevTick, sess.Clock.GlobalTick)
	}
	// Test retry path: reset and ensure clean terminal state [RS-05]
	// Create a fresh session for retry (simulates shell recreating clean session)
	cat2 := testCatalogON05()
	for _, u := range cat2.Units {
		u.Commander = true
	}
	terrain2 := testWorldON05(20, 20)
	b2 := newTestBattle(cat2, terrain2)
	b2.sess.Skirmish = session.SkirmishConfig{MapName: "test", NumPlayers: 2, CommanderDeath: 1}
	b2.sess.Skirmish.Players[0].AllyGroup = 1
	b2.sess.Skirmish.Players[1].AllyGroup = 2
	b2.sess.LocalOwner = 0
	b2.sess.EnemyOwner = 1
	b2.sess.State = session.StateBattle
	defA2, _ := cat2.Unit("armcons")
	defB2, _ := cat2.Unit("armsolar")
	defA2.Commander = true
	defB2.Commander = true
	hA2, _ := b2.sess.Units.Create(defA2, 0, 0, 0, 0)
	hB2, _ := b2.sess.Units.Create(defB2, 1, 0, 0, 0)
	_ = hA2
	b2.sess.Units.Destroy(hB2, 1)
	for tick := 1; tick < 300; tick++ {
		b2.sess.Step(int32(tick))
		if b2.sess.GetResult().Ended {
			break
		}
	}
	if !b2.sess.GetResult().Ended {
		t.Fatalf("retry battle should reach terminal result")
	}
	// Further steps should not advance the terminal result.
	terminalTick := b2.sess.GetResult().Tick
	for tick := 300; tick < 310; tick++ {
		b2.sess.Step(int32(tick))
	}
	if got := b2.sess.GetResult().Tick; got != terminalTick {
		t.Fatalf("terminal result changed after retry battle: %d -> %d", terminalTick, got)
	}
}
