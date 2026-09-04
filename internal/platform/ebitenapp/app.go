package ebitenapp

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/audiobackend"
	"github.com/nanolathe/nanolathe/internal/client"
)

// app adapts a client.Client to Ebitengine's game loop. Update steps the
// client's injected callback (clock/sub-ticks/publish, C9) after refreshing
// input; Draw asks the client to compose its expanded framebuffer and uploads
// the result.
//
// The *ebiten.Image lives here, not on the client: it is a device resource,
// and the client's business is the pixels it hands over.
type app struct {
	c   *client.Client
	img *ebiten.Image
	// windowW/windowH are the size last pushed to the window system. The
	// client owns the logical size and the adapter only follows it, so the
	// load transition's Client.Resize moves the window on the next update
	// without the shell ever reaching a device [07 R-FE-01 §11][I6].
	windowW, windowH int
}

// Update runs at ebiten.TPS (60/s). Delta is the fixed 1/TPS period: stable
// input pacing for menus and camera, and the session converts to sim ticks via
// its own accumulator (wall-clock time never enters the sim, I6).
func (a *app) Update() error {
	a.syncWindowSize()
	pollInput(a.c.Input())
	a.c.SetFocused(ebiten.IsFocused())
	a.c.Step(1.0 / float64(ebiten.TPS()))
	if a.c.ExitRequested() {
		return ebiten.Termination
	}
	return nil
}

// Draw presents one composed frame. The image is recreated only when the
// logical size changes; WritePixels replaces its contents wholesale.
func (a *app) Draw(screen *ebiten.Image) {
	width, height := a.c.Size()
	if a.img == nil || a.img.Bounds().Dx() != width || a.img.Bounds().Dy() != height {
		a.img = ebiten.NewImage(width, height)
	}
	a.img.WritePixels(a.c.Present())
	screen.DrawImage(a.img, &ebiten.DrawImageOptions{})
}

// syncWindowSize pushes a logical size change out to the window system. It is
// the "move the window and re-select the mode" half of the display-mode change
// [07 R-FE-01 §11]; the client already re-allocated the offscreen.
func (a *app) syncWindowSize() {
	width, height := a.c.Size()
	if width == a.windowW && height == a.windowH {
		return
	}
	a.windowW, a.windowH = width, height
	ebiten.SetWindowSize(width, height)
}

// Layout keeps the logical resolution fixed; Ebitengine letterboxes if the
// window is resized.
func (a *app) Layout(outsideWidth, outsideHeight int) (int, int) {
	return a.c.Size()
}

// DesktopSize reports the current monitor's size in logical pixels, or (0, 0)
// when no monitor is available — before the loop is entered, or in a headless
// process.
//
// The display-mode table the `VIDSLDR` slider indexes is gated on the desktop
// size in retail's windowed (GDI) presentation: 640x480, 800x600 and 1024x768
// unconditionally, then 1280x1024 only when the desktop is at least 1280x1024
// and 1600x1200 only when the desktop is at least 1600x1200, both axes
// inclusive [07 R-FE-02 §9]. This is the screen-metrics query that gate reads.
func DesktopSize() (int, int) {
	monitor := ebiten.Monitor()
	if monitor == nil {
		return 0, 0
	}
	return monitor.Size()
}

// Run starts the windowed main loop and blocks until the window closes. It
// must be called from main after option parsing.
func Run(c *client.Client) error {
	if c == nil {
		return fmt.Errorf("nanolathe: run window: logical path %s, providers searched [], expected client with installed retail software cursor", client.CursorGAFPath)
	}
	if !c.HasCursors() {
		return fmt.Errorf("nanolathe: run window: logical path %s, providers searched [], expected installed retail software cursor", client.CursorGAFPath)
	}
	// The concrete PCM device is installed here, once, for the life of the
	// process. It used to be created lazily by the client the first time audio
	// was bound or drained, which is how the device package reached into a
	// package that otherwise touches no hardware [03 §8.1][I6].
	if audio.GlobalOutput() == nil {
		audio.SetGlobalOutput(audiobackend.New())
	}
	width, height := c.Size()
	ebiten.SetWindowSize(width, height)
	if title := c.Title(); title != "" {
		ebiten.SetWindowTitle(title)
	}
	// A software cursor is installed, so hide the window system's pointer and
	// leave the drawn one as the only visible pointer [07 §8].
	ebiten.SetCursorMode(ebiten.CursorModeHidden)
	return ebiten.RunGame(&app{c: c, windowW: width, windowH: height})
}
