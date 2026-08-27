package client

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
)

// ebitenApp adapts Client to Ebitengine's game loop. Update steps the injected
// callback (clock/sub-ticks/publish, C9) after refreshing input; Draw composes
// the indexed framebuffer, expands it through the palette, and uploads it.
type ebitenApp struct {
	c *Client
}

// Update runs at ebiten.TPS (60/s). Delta is the fixed 1/TPS period: stable
// input pacing for menus and camera, and the session converts to sim ticks via
// its own accumulator (wall-clock time never enters the sim, I6).
func (a *ebitenApp) Update() error {
	a.c.in.pollEbiten()
	dt := 1.0 / float64(ebiten.TPS())
	a.c.runtime += dt
	if a.c.opts.Step != nil {
		a.c.opts.Step(dt)
	}
	// [PLAN_03 C16] anchor alpha to last publish.
	a.c.updatePublishAnchor()
	if a.c.ExitRequested() {
		return ebiten.Termination
	}
	return nil
}

// Draw presents one composed frame. The image is recreated only when the
// logical size changes; WritePixels replaces its contents wholesale.
func (a *ebitenApp) Draw(screen *ebiten.Image) {
	c := a.c
	if c.img == nil || c.img.Bounds().Dx() != c.width || c.img.Bounds().Dy() != c.height {
		c.img = ebiten.NewImage(c.width, c.height)
	}
	c.Frame(c.computeAlpha())
	c.img.WritePixels(c.rgba)
	op := &ebiten.DrawImageOptions{}
	screen.DrawImage(c.img, op)
}

// Layout keeps the logical resolution fixed; Ebitengine letterboxes if the
// window is resized.
func (a *ebitenApp) Layout(outsideWidth, outsideHeight int) (int, int) {
	return a.c.width, a.c.height
}

// applyCursorMode hides the window system's pointer whenever a software cursor
// is installed, so the drawn cursor is the only one visible [07 §8]. Headless
// runs never touch the window system.
func (c *Client) applyCursorMode() {
	if c == nil || c.opts.Headless || c.cursors == nil {
		return
	}
	ebiten.SetCursorMode(ebiten.CursorModeHidden)
}

// RunGame starts the windowed main loop and blocks until the window closes.
// In headless mode it returns immediately without creating a window (C11).
// It must be called from main after option parsing.
func RunGame(c *Client) error {
	if c == nil {
		return fmt.Errorf("nanolathe: run window: logical path %s, providers searched [], expected client with installed retail software cursor", CursorGAFPath)
	}
	if c.opts.Headless {
		return nil
	}
	if c.cursors == nil {
		return fmt.Errorf("nanolathe: run window: logical path %s, providers searched [], expected installed retail software cursor", CursorGAFPath)
	}
	ebiten.SetWindowSize(c.width, c.height)
	if c.opts.Title != "" {
		ebiten.SetWindowTitle(c.opts.Title)
	}
	c.applyCursorMode()
	return ebiten.RunGame(&ebitenApp{c: c})
}
