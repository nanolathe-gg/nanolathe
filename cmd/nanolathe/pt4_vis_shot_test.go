//go:build retail

package main

import (
	"fmt"
	"image/png"
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
)

// TestPT4MapEdgeShot composes the battle screen with the viewing player's units
// parked against a map corner and the camera at the matching clamp floor, so
// the unexplored map border is on screen. It writes the frames to
// $NANOLATHE_PT4_SHOT_DIR.
//
// It is a capture harness: no --shot flag can reach this view, because the
// capture path has no camera control and the battle camera starts at the
// player's start position, which on every stock map is well inside the border.
func TestPT4MapEdgeShot(t *testing.T) {
	dir := os.Getenv("NANOLATHE_PT4_SHOT_DIR")
	if dir == "" {
		t.Skip("set NANOLATHE_PT4_SHOT_DIR to capture map-edge frames")
	}
	corners := []struct {
		name  string
		unitX int32 // map pixel the local units are parked at; negative counts back from the far edge
		unitZ int32
		camX  int32 // camera origin request; the clamp resolves it
		camZ  int32
	}{
		{"nw", 96, 96, -9999, -9999},
		{"se", -96, -96, 9999, 9999},
	}
	for _, c := range corners {
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
			// Park the viewing player's units on the corner so their line of
			// sight reveals terrain right up to the map border.
			px, pz := c.unitX, c.unitZ
			if px < 0 {
				px += sess.World.PlayRight
			}
			if pz < 0 {
				pz += sess.World.PlayBottom
			}
			for _, u := range sess.Units.Iter() {
				if u == nil || !u.Alive || u.Owner != sess.LocalOwner {
					continue
				}
				u.X, u.Z = numeric.Fixed(int64(px)<<16), numeric.Fixed(int64(pz)<<16)
				u.Y = sess.World.HeightAt(u.X, u.Z)
			}
			for step := int32(1); step <= 60; step++ {
				sess.Step(step)
			}
			const winW, winH = 640, 480
			cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: winW, Height: winH})
			if err != nil {
				t.Fatal(err)
			}
			cl.SetModelFS(cs.fs)
			b, err := composeBattleEntry(sess, cat, cs, cl, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer b.teardown(cl)
			b.cam.ViewW, b.cam.ViewH = winW, winH
			b.cam.X, b.cam.Z = c.camX, c.camZ
			b.cam.Clamp()
			b.viewerStep(1.0/30.0, cl)
			path := fmt.Sprintf("%s/%s.png", dir, c.name)
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, cl.ComposeFrame()); err != nil {
				f.Close()
				t.Fatal(err)
			}
			f.Close()
			t.Logf("wrote %s (units %d,%d camera %d,%d)", path, px, pz, b.cam.X, b.cam.Z)
		}()
	}
}

// TestPT4RadarMinimapShot composes the battle screen with a radar tower beside
// the viewing player's start and an enemy inside its circle but outside every
// line of sight, so the side rail's minimap can be reviewed as a picture. It
// writes the frame to $NANOLATHE_PT4_SHOT_DIR.
func TestPT4RadarMinimapShot(t *testing.T) {
	dir := os.Getenv("NANOLATHE_PT4_SHOT_DIR")
	if dir == "" {
		t.Skip("set NANOLATHE_PT4_SHOT_DIR to capture the radar minimap frame")
	}
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
	radDef, ok := cat.Unit("armrad")
	if !ok {
		t.Fatal("armrad missing")
	}
	rh, err := sess.Units.Create(radDef, uint8(sess.LocalOwner), cx+numeric.Fixed(64<<16), cy, cz)
	if err != nil {
		t.Fatal(err)
	}
	sess.Units.Unit(rh).Activated = true
	enemyDef, ok := cat.Unit("armpw")
	if !ok {
		t.Fatal("armpw missing")
	}
	var enemyOwner uint8
	for i := 0; i < 10; i++ {
		if sess.Econ.Players[i].Exists && uint8(i) != uint8(sess.LocalOwner) {
			enemyOwner = uint8(i)
			break
		}
	}
	for _, step := range []int32{-2, -1, 1, 2} {
		x := cx - numeric.Fixed(int64(radDef.RadarDistance/2)<<16)
		z := cz + numeric.Fixed(int64(step*96)<<16)
		y := sess.World.HeightAt(x, z)
		if y == -1 {
			y = 0
		}
		if _, err := sess.Units.Create(enemyDef, enemyOwner, x, y, z); err != nil {
			t.Fatal(err)
		}
	}
	for step := int32(1); step <= 60; step++ {
		sess.Step(step)
	}
	const winW, winH = 640, 480
	cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: winW, Height: winH})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetModelFS(cs.fs)
	b, err := composeBattleEntry(sess, cat, cs, cl, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.teardown(cl)
	b.cam.ViewW, b.cam.ViewH = winW, winH
	b.viewerStep(1.0/30.0, cl)
	path := dir + "/radar-minimap.png"
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, cl.ComposeFrame()); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	t.Logf("wrote %s", path)
}
