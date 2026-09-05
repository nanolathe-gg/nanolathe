//go:build retail

package main

// Visual evidence for the game-speed announcement's position, font and
// colour: the shared message-column geometry the master composer uses for
// the ring [07 R-HUD-03 §14.4], not the footer's own console face or a
// screen-centred layout [07 R-CAM-01 §3].

import (
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
)

// TestSpeedMessageShot composes one frame with the "Game Speed  +1"
// announcement latched and visible, through the same production draw path
// footerComposeShot uses. Set NANOLATHE_HUD_SHOT to a path prefix to capture
// it as "<prefix>-speed.png".
func TestSpeedMessageShot(t *testing.T) {
	prefix := os.Getenv("NANOLATHE_HUD_SHOT")
	if prefix == "" {
		t.Skip("set NANOLATHE_HUD_SHOT to a path prefix to capture the speed message shot")
	}
	b, cs, cam, pal := footerShotSession(t)
	defer cs.Close()
	b.fs = cs.fs // drawStatusMessage resolves the message-column font through it

	b.setGameSpeed(1)
	if got := b.battleState().Input.StatusMessage; got != "Game Speed  +1" {
		t.Fatalf("status message = %q, want %q", got, "Game Speed  +1")
	}
	cur, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("no committed frame")
	}
	if !b.statusVisible(cur) {
		t.Fatal("status message not visible on the committed frame")
	}

	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: footerShotW, Height: footerShotH})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(cam)
	cl.SetPalette(pal)
	cl.SetFNT(b.hud.console)
	cl.SetModelFS(cs.fs)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})

	file, err := os.Create(prefix + "-speed.png")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := encodeShot(file, cl); err != nil {
		t.Fatal(err)
	}
}
