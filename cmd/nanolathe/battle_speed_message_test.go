//go:build retail

package main

// Visual evidence for the game-speed announcement's position, font and
// colour: the shared message-column geometry the master composer uses for
// the ring [07 R-HUD-03 §14.4], not the footer's own console face or a
// screen-centred layout [07 R-CAM-01 §3].

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
)

// TestSpeedMessageShot composes one frame with the "Game Speed  +1"
// announcement posted to the shared message ring, through the same
// production draw path footerComposeShot uses. Set NANOLATHE_HUD_SHOT to a
// path prefix to capture it as "<prefix>-speed.png".
//
// Retail has one ring, and the announcement's only drawn representation is
// the ring line the master composer's message column already paints
// [07 R-HUD-03 §14.4][07 R-CAM-01 §3]; there is no separate box to compose,
// so the client built here is wired onto b before the speed change so both
// share the one ring instance the composer reads.
func TestSpeedMessageShot(t *testing.T) {
	prefix := os.Getenv("NANOLATHE_HUD_SHOT")
	if prefix == "" {
		t.Skip("set NANOLATHE_HUD_SHOT to a path prefix to capture the speed message shot")
	}
	b, cs, cam, pal := footerShotSession(t)
	defer cs.Close()
	b.fs = cs.fs // the message column resolves its font through it

	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: footerShotW, Height: footerShotH})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(cam)
	cl.SetPalette(pal)
	cl.SetFNT(b.hud.console)
	cl.SetModelFS(cs.unmappedMount)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
	b.cl = cl

	b.setGameSpeed(1)
	lines := cl.MessageLines()
	if len(lines) == 0 || lines[len(lines)-1].Text != "Game Speed  +1" {
		t.Fatalf("shared ring lines = %+v, want the announcement as the newest line", lines)
	}

	file, err := os.Create(prefix + "-speed.png")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := encodeShot(file, cl); err != nil {
		t.Fatal(err)
	}
}
