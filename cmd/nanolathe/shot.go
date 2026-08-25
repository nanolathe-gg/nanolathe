package main

import (
	"fmt"
	"image/png"
	"os"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// runShot renders the integrated session headless for opts.Frames ticks and
// writes one composed frame to opts.Shot. It is the programmatic screenshot
// path: no window, no host screen — the software framebuffer is the product
// under test [I6]. Deterministic with --seed for regression probes.
func runShot(opts Options, cs *contentSet, out *os.File) error {
	if opts.ShotModel != "" {
		return runShotModel(opts, cs, out)
	}
	sess, _, err := newBattleSession(opts, cs)
	if err != nil {
		return err
	}
	terrain := sess.World
	const winW, winH = 640, 480
	mapW := int32(terrain.CellW * 16)
	mapH := int32(terrain.CellH * 16)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: winW, ViewH: winH, MapW: mapW, MapH: mapH}
	cam.Pan(0, 0)
	centerOnCommander(sess.Units, cam, winW, winH)

	cl, err := client.New(client.Options{
		Buffer:   sess.Snapshot,
		Width:    winW,
		Height:   winH,
		Headless: true,
	})
	if err != nil {
		return fmt.Errorf("nanolathe: shot: %w", err)
	}
	cl.SetTerrain(sess.World)
	cl.SetCamera(cam)
	if pal := loadPalette(cs); pal != nil {
		cl.SetPalette(pal)
	}
	if fnt := loadFNT(cs); fnt != nil {
		cl.SetFNT(fnt)
	}
	cl.SetModelFS(cs.fs)

	frames := opts.Frames
	if frames <= 0 {
		frames = 30
	}
	for i := 0; i < frames; i++ {
		sess.Step(int32(i + 1)) // scaled-ms anchor: one 30 Hz tick per frame
	}
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive {
			continue
		}
		sx, sy := cam.WorldToScreen(u.X, u.Y, u.Z)
		model := ""
		if u.Def != nil {
			model = u.Def.ObjectName
		}
		fmt.Fprintf(out, "nanolathe: shot: unit slot=%d owner=%d model=%q world=(%d,%d,%d) screen=(%d,%d)\n",
			u.Handle, u.Owner, model, int32(u.X>>16), int32(u.Y>>16), int32(u.Z>>16), sx, sy)
	}
	img := cl.ComposeFrame()
	f, err := os.Create(opts.Shot)
	if err != nil {
		return fmt.Errorf("nanolathe: shot: %w", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("nanolathe: shot: %w", err)
	}
	alive := 0
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive {
			alive++
		}
	}
	fmt.Fprintf(out, "nanolathe: shot %s: %dx%d after %d ticks, %d units\n",
		opts.Shot, img.Bounds().Dx(), img.Bounds().Dy(), frames, alive)
	return nil
}

// runShotModel renders one 3DO model in isolation at screen center through
// the real publish→compose pipeline — the fastest renderer feedback loop.
func runShotModel(opts Options, cs *contentSet, out *os.File) error {
	const winW, winH = 640, 480
	cl, err := client.New(client.Options{Width: winW, Height: winH, Headless: true})
	if err != nil {
		return err
	}
	if pal := loadPalette(cs); pal != nil {
		cl.SetPalette(pal)
	}
	cl.SetModelFS(cs.fs)
	// cam (0,0): screenX = wx+128, screenY = wz+32 → center the model origin.
	cl.SetCamera(&camera.Camera{X: 0, Z: 0, ViewW: winW, ViewH: winH, MapW: winW, MapH: winH})
	buf := cl.Buffer()
	frame := &snapshot.Frame{Tick: 1}
	frame.Units = append(frame.Units, snapshot.UnitView{
		Slot:   1,
		Owner:  0,
		Model:  opts.ShotModel,
		X:      192 << 16,
		Z:      208 << 16,
		Health: 100, MaxHealth: 100,
	})
	buf.Publish(frame)
	img := cl.ComposeFrame()
	f, err := os.Create(opts.Shot)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	fmt.Fprintf(out, "nanolathe: shot %s: model %q at center\n", opts.Shot, opts.ShotModel)
	return nil
}
