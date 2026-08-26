package main

import (
	"fmt"
	"image/png"
	"os"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/input"
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
	if opts.ShotMenu != "" {
		return runShotMenu(opts, cs, out)
	}
	sess, cat, err := newBattleSession(opts, cs)
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
	// NANOLATHE_SHOT_PAN=dx,dz offsets the shot camera after centering — a
	// diagnostic for map-edge rendering (fog border, terrain void).
	if pan := os.Getenv("NANOLATHE_SHOT_PAN"); pan != "" {
		var dx, dz int64
		fmt.Sscanf(pan, "%d,%d", &dx, &dz)
		cam.X += int32(dx)
		cam.Z += int32(dz)
	}

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
	pal := loadPalette(cs)
	b := &battleSession{sess: sess, cat: cat, cam: cam, latch: input.LatchNormal}
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal)
	if err != nil {
		return err
	}
	if pal != nil {
		cl.SetPalette(pal)
	}
	cl.SetFNT(b.hud.console)
	cl.Overlay = func(c *client.Client) { b.hud.draw(c, b) }
	cl.SetModelFS(cs.fs)
	// The composed frame includes the software cursor, as retail's does
	// [07 §8]. Headless has no window system, so the pointer is placed at the
	// centre of the world viewport.
	if cursors, cerr := client.LoadCursors(cs.fs); cerr == nil {
		cl.SetCursors(cursors)
		cl.Input().Mouse.SetPosition(float32(winW/2), float32(winH/2))
	} else {
		fmt.Fprintf(out, "nanolathe: shot: %v\n", cerr)
	}

	frames := opts.Frames
	if frames <= 0 {
		frames = 30
	}
	for i := 0; i < frames; i++ {
		sess.Step(int32(i + 1)) // scaled-ms anchor: one 30 Hz tick per frame
		cl.TickTextureAnimators(1)
		if cursors := cl.Cursors(); cursors != nil {
			cursors.Step(1)
		}
		b.updateCursor(cl)
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
	frames := opts.Frames
	if frames < 1 {
		frames = 1
	}
	cl.TickTextureAnimators(frames)
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
