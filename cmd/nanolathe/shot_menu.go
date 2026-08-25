package main

import (
	"fmt"
	"image/png"
	"os"
	"strings"
	"time"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// menuShotModes maps the --shot-menu argument onto a frontend panel. The names
// are the retail screens, not Nanolathe inventions.
var menuShotModes = map[string]shellMode{
	"main":     modeMenuMain,
	"single":   modeMenuSingle,
	"mission":  modeMenuMission,
	"map":      modeMenuMap,
	"skirmish": modeMenuSkirmish,
	"loading":  modeLoading,
}

// runShotMenu composes one frontend panel headless and writes it to a PNG.
// It is the menu counterpart of runShot: the same gameShell, the same
// drawRetailPanel, no window and no host screen capture [I6].
func runShotMenu(opts Options, cs *contentSet, out *os.File) error {
	mode, ok := menuShotModes[strings.ToLower(strings.TrimSpace(opts.ShotMenu))]
	if !ok {
		names := make([]string, 0, len(menuShotModes))
		for name := range menuShotModes {
			names = append(names, name)
		}
		return fmt.Errorf("nanolathe: --shot-menu %q: want one of %s", opts.ShotMenu, strings.Join(names, ", "))
	}
	shell, err := newGameShell(opts, cs)
	if err != nil {
		return err
	}
	const winW, winH = 640, 480
	shell.cam = &camera.Camera{X: 0, Z: 0, ViewW: winW, ViewH: winH, MapW: winW, MapH: winH}
	cl, err := client.New(client.Options{
		Buffer:   &snapshot.Buffer{},
		Width:    winW,
		Height:   winH,
		Headless: true,
	})
	if err != nil {
		return fmt.Errorf("nanolathe: shot-menu: %w", err)
	}
	cl.SetModelFS(cs.fs)
	cl.SetCamera(shell.cam)
	if shell.assets != nil && shell.assets.pal != nil {
		cl.SetPalette(shell.assets.pal)
	}
	if shell.font != nil {
		cl.SetFNT(shell.font)
	}
	// SELMAP.GUI is a panel window, not a screen: reach it the way the frontend
	// does so the window chain beneath it is the skirmish setup screen.
	if mode == modeMenuMap {
		shell.mapReturn = modeMenuSkirmish
		shell.openMenu(modeMenuSkirmish)
	}
	if mode == modeLoading {
		// Start the real load and sample the screen once a bar is partway, so
		// the capture is the composition under load rather than an empty one.
		shell.startBattleLoad(shell.setup.MapName)
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if p := shell.loading; p == nil || partialLoadBar(p) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	} else {
		shell.openMenu(mode)
	}
	if cursors, cerr := client.LoadCursors(cs.fs); cerr == nil {
		cl.SetCursors(cursors)
		cursors.SetIndex(render.CursorNormal)
		// Park the pointer off the panel: a hovered gadget would arm art that
		// is not part of the resting composition under test.
		cl.Input().Mouse.SetPosition(winW-1, winH-1)
	}
	cl.Overlay = func(c *client.Client) { shell.draw(c) }
	img := cl.ComposeFrame()
	f, err := os.Create(opts.Shot)
	if err != nil {
		return fmt.Errorf("nanolathe: shot-menu: %w", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("nanolathe: shot-menu: %w", err)
	}
	fmt.Fprintf(out, "nanolathe: shot-menu %s: %s %dx%d\n",
		opts.Shot, opts.ShotMenu, img.Bounds().Dx(), img.Bounds().Dy())
	return nil
}

// partialLoadBar reports whether any stage is under way but unfinished, which
// is the interesting frame to capture.
func partialLoadBar(l *loadingState) bool {
	for i := range l.percent {
		if p := l.percent[i].Load(); p > 0 && p < 100 {
			return true
		}
	}
	return false
}
