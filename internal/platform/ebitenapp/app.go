package ebitenapp

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/audiobackend"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/platform/gpurender"
)

const presentationTPS = 30

// RendererMode selects which executor Draw presents through — the one sanctioned
// presentation switch of docs/DESIGN_GPU_RENDERER.md §2.4 [I11]. Both executors
// replay the same recorded frame; the simulation cannot tell which is active.
type RendererMode int

const (
	// RendererClassic uploads the client's software-composed RGBA framebuffer, the
	// default and the reference executor (C-G11).
	RendererClassic RendererMode = iota
	// RendererModern replays the recorded draw list through gpurender on the GPU
	// (docs/DESIGN_GPU_RENDERER.md §2.3).
	RendererModern
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
	// mode selects the executor Draw presents through. gpu is the modern
	// executor, built lazily on the first modern Draw so its device textures and
	// offscreen never exist in a classic run. Both live here, off the client
	// (docs/DESIGN_GPU_RENDERER.md §2.4).
	mode RendererMode
	gpu  *gpurender.Renderer
	// windowW/windowH are the size last pushed to the window system. The
	// client owns the logical size and the adapter only follows it, so the
	// load transition's Client.Resize moves the window on the next update
	// without the shell ever reaching a device [07 R-FE-01 §11][I6].
	windowW, windowH int
	// presentPending is set by the 30 Hz update and consumed by Draw. Draw can
	// still be called at the monitor's refresh rate, so the retained-screen
	// mode configured by Run lets those extra calls leave the frame untouched.
	presentPending bool
	// inputStarted anchors the platform-only host clock used to timestamp
	// polled pointer records. It is deliberately outside the client and sim.
	inputStarted time.Time
}

// Update runs at presentationTPS. Delta is the fixed 1/TPS period: stable
// input pacing for menus and camera, and the session converts to sim ticks via
// its own accumulator (wall-clock time never enters the sim, I6).
func (a *app) Update() error {
	a.syncWindowSize()
	pollInput(a.c.Input(), a.scaledInputNow())
	a.c.SetFocused(ebiten.IsFocused())
	a.c.Step(1.0 / float64(presentationTPS))
	a.presentPending = true
	if a.c.ExitRequested() {
		return ebiten.Termination
	}
	return nil
}

func (a *app) scaledInputNow() uint32 {
	if a.inputStarted.IsZero() {
		a.inputStarted = time.Now()
	}
	millis := uint32(time.Since(a.inputStarted) / time.Millisecond)
	return uint32(clock.ScaledNow(millis))
}

// Draw presents one composed frame. The image is recreated only when the
// logical size changes; WritePixels replaces its contents wholesale.
func (a *app) Draw(screen *ebiten.Image) {
	if !a.consumePresentation() {
		return
	}
	width, height := a.c.Size()
	if a.mode == RendererModern {
		a.drawModern(screen, width, height)
		return
	}
	if a.img == nil || a.img.Bounds().Dx() != width || a.img.Bounds().Dy() != height {
		a.img = ebiten.NewImage(width, height)
	}
	a.img.WritePixels(a.c.Present())
	screen.DrawImage(a.img, &ebiten.DrawImageOptions{})
}

// drawModern presents through the modern (GPU) executor: the client records the
// frame, the renderer replays that list and expands it, and the expanded surface
// is drawn to the screen. The client never composes bytes in this path — calling
// c.Frame here would replay the list through the classic sink and double the work
// (docs/DESIGN_GPU_RENDERER.md §2.4). The renderer is built lazily on first use
// from the installed palette.
func (a *app) drawModern(screen *ebiten.Image, width, height int) {
	if a.gpu == nil {
		a.gpu = gpurender.New(a.c.PaletteTables(), width, height)
	}
	list := a.c.RecordFrame()
	img := a.gpu.Execute(list, width, height)
	if img == nil {
		return
	}
	screen.DrawImage(img, &ebiten.DrawImageOptions{})
}

func (a *app) consumePresentation() bool {
	if !a.presentPending {
		return false
	}
	a.presentPending = false
	return true
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

// windowOwned records that this process has entered the window layer: Run sets
// it immediately before the first Ebitengine window call and it stays set for
// the life of the process. It is the seam every query that would otherwise
// reach the window system from outside the game loop consults first.
//
// It exists because the window layer is brought up lazily, inside whichever
// window-API call happens to be first, and that bring-up needs the process's
// own main thread — the one Run occupies. A process that owns no window (the
// headless run, `--shot`, or a test driving the shell directly) has no such
// thread, so the bring-up faults there rather than reporting that there is no
// display. Asking the flag instead keeps those processes out of the window
// layer entirely.
var windowOwned atomic.Bool

// DesktopSize reports the current monitor's size in logical pixels, or (0, 0)
// when this process owns no window, or owns one whose monitor cannot be
// identified.
//
// (0, 0) is the headless answer, not a sentinel invented here: it is what the
// monitor query itself reported with no display attached, and callers already
// treat it as "no desktop metrics" — retailDisplayModes gates every optional
// row on a minimum size, so a zero desktop offers exactly the unconditional
// rows.
//
// The display-mode table the `VIDSLDR` slider indexes is gated on the desktop
// size in retail's windowed (GDI) presentation: 640x480, 800x600 and 1024x768
// unconditionally, then 1280x1024 only when the desktop is at least 1280x1024
// and 1600x1200 only when the desktop is at least 1600x1200, both axes
// inclusive [07 R-FE-02 §9]. This is the screen-metrics query that gate reads,
// and in the windowed build it still reads the live monitor.
func DesktopSize() (int, int) {
	if !windowOwned.Load() {
		return 0, 0
	}
	monitor := ebiten.Monitor()
	if monitor == nil {
		return 0, 0
	}
	return monitor.Size()
}

// Run starts the windowed main loop and blocks until the window closes. It
// must be called from main after option parsing. mode selects the start-up
// executor (docs/DESIGN_GPU_RENDERER.md §2.4); an unrecognised value presents
// through the classic executor.
func Run(c *client.Client, mode RendererMode) error {
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
		be := audiobackend.New()
		audio.SetGlobalOutput(be)
		// Kick off the host device's asynchronous bring-up now, before the
		// window is shown and before any cue can be queued, rather than
		// letting the wait land on whichever click happens to play first.
		// This is platform work, not retail behaviour — see Backend.WarmUp.
		be.WarmUp()
	}
	width, height := c.Size()
	// From here on this process owns a window: every window-API call below, and
	// everything the game loop reaches through app.Update, runs on this thread
	// with the window layer brought up. Arm the seam before the first of them.
	windowOwned.Store(true)
	ebiten.SetWindowSize(width, height)
	if title := c.Title(); title != "" {
		ebiten.SetWindowTitle(title)
	}
	// A software cursor is installed, so hide the window system's pointer and
	// leave the drawn one as the only visible pointer [07 §8].
	ebiten.SetCursorMode(ebiten.CursorModeHidden)
	// Draw remains VSync-driven even when TPS is lower. Retain the screen so
	// calls between updates can skip composition, upload, and drawing without
	// clearing the last presented frame.
	ebiten.SetScreenClearedEveryFrame(false)
	ebiten.SetTPS(presentationTPS)
	return ebiten.RunGame(&app{c: c, windowW: width, windowH: height, mode: mode})
}
