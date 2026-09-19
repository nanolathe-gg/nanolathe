//go:build retail

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// TestShareMarkerShot composes the battle screen with and without an automatic
// sharing threshold on the viewing player, so the top strip's share marker
// [07 R-HUD-03 §4] can be reviewed as a picture. It writes the frames and a
// ten-times magnification of the strip to $NANOLATHE_SHARE_SHOT_DIR.
//
// It is a capture harness: no flag reaches a nonzero threshold, because the
// battle initializer clears both thresholds [05 R-SHARE-01 §3] and the SHARE
// screen that would set them is not wired yet.
func TestShareMarkerShot(t *testing.T) {
	dir := os.Getenv("NANOLATHE_SHARE_SHOT_DIR")
	if dir == "" {
		t.Skip("set NANOLATHE_SHARE_SHOT_DIR to capture the share-marker frames")
	}
	for _, tc := range []struct {
		name          string
		energy, metal float32
	}{
		{"before", 0, 0},
		{"after", 300, 120},
	} {
		func() {
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
			for step := int32(1); step <= 120; step++ {
				sess.Step(step)
			}
			const winW, winH = 640, 480
			cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: winW, Height: winH})
			if err != nil {
				t.Fatal(err)
			}
			cl.SetModelFS(cs.unmappedMount)
			b, err := composeBattleEntry(sess, cat, cs, cl, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer b.teardown(cl)
			b.cam.ViewW, b.cam.ViewH = winW, winH
			b.cam.Clamp()
			// The displayed stocks ease from zero an eighth of the gap per
			// presented frame [05 R-ECO-01 §6], so present enough frames for
			// the fill to reach the live stock the marker is gated on.
			for range 80 {
				b.viewerStep(1.0/30.0, cl)
				cl.BeginPresentationFrame()
			}
			// The thresholds are set after battle entry, whose initializer
			// clears them [05 R-SHARE-01 §3], and the tick that follows
			// publishes them onto the committed frame.
			local := int(sess.LocalOwner)
			sess.Econ.Players[local].EnergyShareThreshold = tc.energy
			sess.Econ.Players[local].MetalShareThreshold = tc.metal
			for step := int32(121); step <= 125; step++ {
				sess.Step(step)
			}
			cl.BeginPresentationFrame()
			img := cl.ComposeFrame()
			shareShotPNG(t, fmt.Sprintf("%s/ds41-share-marker-%s.png", dir, tc.name), img)
			shareShotPNG(t, fmt.Sprintf("%s/ds41-share-marker-%s-strip10x.png", dir, tc.name),
				shareShotMagnify(img, image.Rect(128, 0, 640, 24), 10))
		}()
	}
}

func shareShotPNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}

func shareShotMagnify(src image.Image, r image.Rectangle, scale int) image.Image {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx()*scale, r.Dy()*scale))
	for y := 0; y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			out.Set(x, y, src.At(r.Min.X+x/scale, r.Min.Y+y/scale))
		}
	}
	return out
}
