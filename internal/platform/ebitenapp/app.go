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

// presentationTPS is the window's Update rate. It stays at 30: every per-host-
// frame step the battle takes — input edges, the follow-camera glide's per-frame
// step [07 R-CAM-01 §12], the scroll pass [07 §10], the sub-tick budget
// [01 §4.2] — keeps the cadence retail gives it. Draw is called at the display's
// refresh rate regardless, which is what the Enhanced path presents on
// (docs/DESIGN_GPU_RENDERER.md §13.5).
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
	// It gates the classic path only: modern records and replays on every Draw
	// (§13.5).
	presentPending bool
	// interpolating records that the modern path has enabled the client's
	// blended view; it is armed once, on the first modern Draw, and classic
	// never arms it (§13.5).
	interpolating bool
	// inputStarted anchors the platform-only host clock used to timestamp
	// polled pointer records. It is deliberately outside the client and sim.
	inputStarted time.Time
	// updatedAt is when the last Update returned. It measures how far the
	// window is through the current Update, which is the camera's blend
	// fraction (§13.5) — the camera moves on this grid, not on the simulation's
	// scaled units. Like inputStarted it is platform time and never reaches the
	// client's clock or the sim [I6].
	updatedAt time.Time
	// presentInterval is the minimum spacing between two presented modern
	// frames, zero for the display's own refresh; presentedAt is when the last
	// one was presented. See RunOptions.MaxFPS.
	presentInterval time.Duration
	presentedAt     time.Time
}

// RunOptions are the window's host-side settings, none of which the client or
// the simulation can observe.
type RunOptions struct {
	// MaxFPS caps how often the modern path presents. Zero presents on every
	// Draw, the display's refresh rate. Draw still arrives on the display's
	// vsync grid, so the cap lands on the nearest refresh multiple below it: 60
	// on a 120 Hz display presents every second refresh, and a cap the display
	// cannot divide into (90 on 120 Hz) rounds down the same way. Classic is
	// untouched: it presents once per 30 Hz update whatever the cap says.
	MaxFPS int
}

// Update runs at presentationTPS. Delta is the fixed 1/TPS period: stable
// input pacing for menus and camera, and the session converts to sim ticks via
// its own accumulator (wall-clock time never enters the sim, I6).
func (a *app) Update() error {
	a.syncWindowSize()
	pollInput(a.c.Input(), a.scaledInputNow())
	a.c.SetFocused(ebiten.IsFocused())
	a.stepClient()
	a.syncPointerCapture()
	a.presentPending = true
	a.updatedAt = time.Now()
	if a.c.ExitRequested() {
		a.c.SetPointerCaptured(false)
		a.syncPointerCapture()
		return ebiten.Termination
	}
	return nil
}

func (a *app) stepClient() {
	a.c.Step(1.0 / float64(presentationTPS))
	if !a.c.IsFocused() {
		// Host focus loss ends relative capture; no historical native pointer
		// record is synthesized for the lost interval [01 R-PLAT-01 §6][T25].
		a.c.SetPointerCaptured(false)
	}
}

// Captured mode supplies an unbounded virtual cursor. The battle owner spends
// successive differences, equivalent to recentering after each sampled frame
// [07 R-CAM-01 §11]. Leaving capture restores the native pre-capture position.
// TODO(T25): native capture starts after the host poll, so its restore point
// cannot reproduce a historical queued pointer record's position exactly.
func (a *app) syncPointerCapture() {
	want := ebiten.CursorModeHidden
	if a.c.PointerCaptured() {
		want = ebiten.CursorModeCaptured
	}
	if ebiten.CursorMode() != want {
		ebiten.SetCursorMode(want)
	}
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
	width, height := a.c.Size()
	// Enhanced presents on every Draw — that is the whole of the refresh-rate
	// cadence — so it does not consume the update's pending flag; the blended
	// view differs between two Draws of one update (§13.5).
	if a.mode == RendererModern {
		if !a.presentDue() {
			return
		}
		a.drawModern(screen, width, height)
		return
	}
	if !a.consumePresentation() {
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
	// How far this Draw is through the current Update, in updates: the camera's
	// own fraction (§13.5). Before the first Update there is nothing to measure
	// and the camera stays where it is.
	if !a.updatedAt.IsZero() {
		a.c.SetCameraFraction(float32(time.Since(a.updatedAt).Seconds() * presentationTPS))
	}
	if !a.interpolating {
		// Enhanced is the one presentation path allowed to read two committed
		// ticks; the client blends the world with the fraction the battle
		// produces and the camera with the update phase above (§13.5) [I6].
		a.c.SetInterpolation(true)
		a.interpolating = true
	}
	list := a.c.RecordFrame()
	img := a.gpu.Execute(list, width, height)
	if img == nil {
		return
	}
	screen.DrawImage(img, &ebiten.DrawImageOptions{})
}

// presentDue applies RunOptions.MaxFPS to one modern Draw. The screen is
// retained between Draws (Run configures that), so a skipped Draw leaves the
// last presented frame on the display. The test allows an eighth of the
// interval of slack: Draw calls sit on the vsync grid and jitter by a fraction
// of a refresh, and without the slack a 60 cap on a 120 Hz display would skip
// every refresh that landed a few microseconds early and present at 40.
func (a *app) presentDue() bool {
	if a.presentInterval <= 0 {
		return true
	}
	now := time.Now()
	if !a.presentedAt.IsZero() && now.Sub(a.presentedAt) < a.presentInterval-a.presentInterval/8 {
		return false
	}
	a.presentedAt = now
	return true
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
func Run(c *client.Client, mode RendererMode, options RunOptions) error {
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
	game := &app{c: c, windowW: width, windowH: height, mode: mode}
	if options.MaxFPS > 0 {
		game.presentInterval = time.Second / time.Duration(options.MaxFPS)
	}
	return ebiten.RunGame(game)
}
