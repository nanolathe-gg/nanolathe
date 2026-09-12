package ebitenapp

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/audiobackend"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"
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
	// reference executor (C-G11).
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
	paused pausedWorld
	c      *client.Client
	img    *ebiten.Image
	// mode selects the executor Draw presents through. gpu is the modern
	// executor, built lazily on the first modern Draw so its device textures and
	// offscreen never exist in a classic run. Both live here, off the client
	// (docs/DESIGN_GPU_RENDERER.md §2.4).
	mode             RendererMode
	gpu              *gpurender.Renderer
	sourceGeneration uint64
	// rendererToggles is the client's executor-swap request count this adapter
	// has already acted on. Update compares it with the client's own count, so
	// one F10 press swaps once (docs/DESIGN_GPU_RENDERER.md §14.6).
	rendererToggles int
	// scrollPointScale converts native window points to the letterboxed surface.
	scrollPointScale float64
	// windowW/windowH are the last selected host size. Logical menu/battle
	// transitions do not change them (DESIGN_PRESENTATION_CLIENT §2.1).
	windowW, windowH    int
	options             RunOptions
	fullscreen          bool
	fullscreenEnterHeld bool
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
	// pipe is the record/submit pipeline's host state
	// (docs/DESIGN_GPU_RENDERER.md §13.10). It is modern-only: the classic path
	// never launches a pre-record and never joins one it did not launch.
	pipe pipeline
	// ledger decides whether an Update call's body runs there or at the end of
	// the modern Draw that follows it, and guarantees one body per call either
	// way (§13.10).
	ledger updateLedger
	// exitPending records an exit request seen from a Draw tail, where a
	// Termination cannot be returned; the next Update call returns it.
	exitPending bool
	// bodies counts update bodies run, for the pipeline readout's cadence
	// sanity line: bodies per second must stay at the update rate however the
	// deferral moves them.
	bodies int64
}

// RunOptions are the window's host-side settings, none of which the client or
// the simulation can observe.
type RunOptions struct {
	// Stats enables periodic and final host pipeline/cadence diagnostics on
	// stderr. F11 captures retain their counters independently of this option.
	Stats bool
	// MaxFPS caps how often the modern path presents. Zero presents on every
	// Draw, the display's refresh rate. Draw still arrives on the display's
	// vsync grid, so the cap lands on the nearest refresh multiple below it: 60
	// on a 120 Hz display presents every second refresh, and a cap the display
	// cannot divide into (90 on 120 Hz) rounds down the same way. Classic is
	// untouched: it presents once per 30 Hz update whatever the cap says.
	MaxFPS int
	// PresentationSettings supplies committed live executor and FPS preferences
	// (DESIGN_GPU_RENDERER §13.5, §14.6). Nil keeps the Run mode and MaxFPS.
	PresentationSettings func() (RendererMode, int)
	// RendererChanged reports an F10 executor swap synchronously. The owner must
	// update PresentationSettings before the next poll so it preserves the swap.
	// Applying an external preference does not invoke this callback.
	RendererChanged func(RendererMode)
	// WindowSize supplies the selected host window size independently of the
	// logical menu/battle canvas. Nil follows the client's logical size.
	WindowSize func() (int, int)
	Fullscreen bool
	// FullscreenChanged persists an observed platform change, including native
	// window controls. It runs on the game loop, never on a simulation tick.
	FullscreenChanged func(bool)
}

// Update runs at presentationTPS. Delta is the fixed 1/TPS period: stable
// input pacing for menus and camera, and the session converts to sim ticks via
// its own accumulator (wall-clock time never enters the sim, I6).
//
// The body of an update does not always run here. Under the modern executor it
// is deferred to the end of the Draw this call precedes, so that the pipeline's
// pre-record for the FOLLOWING frame is taken after the update's writes rather
// than before them (updateBody, §13.10). Ebitengine takes this call's input
// snapshot immediately before it, so the deferred body reads that snapshot and
// each snapshot is still consumed exactly once. What the deferral costs is one
// presented frame of latency for host-step-dependent content: a list recorded
// during the previous frame's flush cannot contain input that arrived after
// it. Cursor position alone is refreshed from the current platform snapshot
// immediately before replay, without changing the client's command input.
func (a *app) Update() error {
	// The pipeline's barrier. The body below writes client state — input, focus,
	// the step and its publication, the pointer mode, the executor swap — and
	// none of it may run while the pre-record is still reading
	// (docs/DESIGN_GPU_RENDERER.md §13.10). A deferring call writes nothing, so
	// the record it leaves running is still valid at the Draw that consumes it;
	// the join costs nothing there because Draw would join immediately after.
	a.c.JoinPreRecord()
	if a.exitPending {
		return a.terminate()
	}
	// The ledger decides where this call's body runs. The modern executor with
	// a live Draw tail defers it; everything else runs it here and now.
	for range a.ledger.call(a.mode == RendererModern && a.gpu != nil) {
		if a.exitPending {
			break
		}
		a.updateBody()
	}
	if a.exitPending {
		return a.terminate()
	}
	return nil
}

// updateBody is one update: the whole of what Update used to do inline. It runs
// either from Update or from the tail of the modern Draw that call precedes,
// and never from both — the ledger is what keeps that true.
//
// An exit request cannot terminate from a Draw, so it is recorded and the next
// Update call returns the Termination.
func (a *app) updateBody() {
	// Tell the pipeline the writes below happened, so a record taken before
	// them is stale (§13.10).
	a.c.BumpPresentationEpoch()
	a.syncWindowSize()
	sample := readInput(a.scaledInputNow())
	if a.scrollPointScale > 0 {
		sample.panX *= a.scrollPointScale
		sample.panY *= a.scrollPointScale
	}
	if a.consumeFullscreenShortcut(&sample) {
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
	}
	a.observeFullscreen(ebiten.IsFullscreen())
	applyInput(a.c.Input(), sample)
	a.c.SetFocused(ebiten.IsFocused())
	a.stepClient()
	a.syncRendererSources()
	a.syncWindowSize()
	a.syncPointerCapture()
	a.syncPresentationSettings()
	a.serviceRendererRequest()
	a.presentPending = true
	a.updatedAt = time.Now()
	a.bodies++
	if a.c.ExitRequested() {
		a.exitPending = true
	}
}

// terminate ends the run: the pointer goes back to the window system and the
// pipeline prints its final readout when statistics were requested.
func (a *app) terminate() error {
	a.c.SetPointerCaptured(false)
	a.syncPointerCapture()
	a.reportPipeline()
	return ebiten.Termination
}

// serviceRendererRequest applies the client's pending executor swaps — F10 of
// docs/DESIGN_GPU_RENDERER.md §14.6. The swap happens between two Updates, so
// no Draw ever sees a half-changed adapter, and the retained screen keeps the
// last presented frame on the display across it.
//
// Classic cannot present a blended view, so interpolation is turned off when it
// takes over and the modern path re-arms it itself on its next Draw (§13.5).
// The classic path also presents only on a pending update, so the swap sets
// that flag: without it the swapped-in executor would wait for the next 30 Hz
// Update before anything reached the screen.
func (a *app) serviceRendererRequest() {
	requested := a.c.RendererToggleCount()
	if requested == a.rendererToggles {
		return
	}
	a.rendererToggles = requested
	mode := RendererModern
	if a.mode == RendererModern {
		mode = RendererClassic
	}
	a.setRenderer(mode)
	if a.options.RendererChanged != nil {
		a.options.RendererChanged(a.mode)
	}
	fmt.Fprintf(os.Stderr, "nanolathe: renderer %s\n", rendererName(a.mode))
}

// syncPresentationSettings runs after the host step commits options and before
// F10 is serviced, so the shortcut remains the last selection of this update.
func (a *app) syncPresentationSettings() {
	maxFPS := a.options.MaxFPS
	if a.options.PresentationSettings != nil {
		mode, fps := a.options.PresentationSettings()
		a.setRenderer(mode)
		maxFPS = fps
	}
	a.presentInterval = 0
	if maxFPS > 0 {
		a.presentInterval = time.Second / time.Duration(maxFPS)
	}
}

// setRenderer shares the pipeline and interpolation cleanup for F10 and live
// preferences (DESIGN_GPU_RENDERER §13.10, §14.6).
func (a *app) setRenderer(mode RendererMode) {
	if mode == a.mode {
		return
	}
	a.c.CancelPreRecord()
	a.pipe.armed = false
	a.mode = mode
	if mode != RendererModern {
		a.c.SetInterpolation(false)
		// Original draws the authored art: the synthesized 2x tiles and
		// sprites are an Enhanced feature (DESIGN_GPU_RENDERER §14.3).
		a.c.SetEnhanced(false)
		a.interpolating = false
	} else {
		a.c.SetEnhanced(true)
	}
	a.presentPending = true
}

func rendererName(mode RendererMode) string {
	if mode == RendererModern {
		return "modern"
	}
	return "classic"
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
	a.beginDraw()
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
	a.paused.clear()
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
	// The pipeline's second barrier: this Draw is about to settle fractions,
	// drain audio and either consume or replace the pre-recorded list, and none
	// of that may overlap the record still running (§13.10).
	a.c.JoinPreRecord()
	now := time.Now()
	period := a.pipe.observeDraw(now, a.presentInterval)
	// How far this Draw is through the current Update, in updates: the camera's
	// own fraction (§13.5). Before the first Update there is nothing to measure
	// and the camera stays where it is.
	if !a.updatedAt.IsZero() {
		a.c.SetCameraFraction(float32(now.Sub(a.updatedAt).Seconds() * presentationTPS))
	}
	if !a.interpolating {
		// Enhanced is the one presentation path allowed to read two committed
		// ticks; the client blends the world with the fraction the battle
		// produces and the camera with the update phase above (§13.5) [I6].
		a.c.SetInterpolation(true)
		a.c.SetEnhanced(true)
		a.interpolating = true
	}
	// Audio stays here, on the game goroutine and once per presented frame,
	// whether the list was pre-recorded or not [03 §8.3] C18. The caption ring
	// it can write is in the pipeline's digest, so a drain that changed what
	// the recorder reads discards the pre-record rather than presenting a list
	// recorded before it (§13.10).
	a.c.BeginPresentationFrame()
	tick16 := a.c.ResolveTickFraction()
	// Present at the fraction the list was predicted for when the prediction
	// held to within one present interval, and take the exact path when it did
	// not (§13.10). The interval is measured, so the tolerance follows the
	// display the window is actually running on.
	// The interval is the one the outstanding prediction was made over, so a
	// frame that arrived late cannot widen the tolerance by its own lateness.
	if !a.drawPaused(screen, width, height) {
		tolerance := fractionTolerance(a.pipe.tolerancePeriod(a.presentInterval, ebiten.ActualFPS()))
		list, hit := a.c.TakePreRecord(a.c.PresentationDigest(), tolerance)
		switch {
		case hit:
			a.pipe.hits++
		case a.pipe.armed:
			a.pipe.misses++
		default:
			a.pipe.synchronous++
		}
		a.pipe.armed = false
		if !hit {
			list = a.c.RecordModernFrame()
		}
		a.gpu.SetDisplayPalette(a.c.DisplayPalette())
		a.gpu.SetGlow(a.c.Glow())
		// Ebitengine has already sampled this Update's pointer even when its
		// client input publication is deferred to the Draw tail. Place only the
		// cursor from that newer sample after the recorder joins [07 §8].
		x, y := ebiten.CursorPosition()
		a.c.PositionPresentationCursor(list, x, y)
		if img := a.gpu.Execute(list, width, height); img != nil {
			a.c.CommitStrategicPresentation()
			screen.DrawImage(img, &ebiten.DrawImageOptions{})
		}
	}
	// Execute has enqueued this frame and copied what the device needs, so the
	// list and the recorder's scratch are free again. Spend the flush and the
	// swap that follow this Draw on the update this frame owes and then on
	// recording the next frame (§13.10).
	a.reportPipelinePeriodically()
	sampledAt := now
	if a.ledger.tail() {
		// The update Ebitengine asked for before this Draw. Running it here,
		// after Execute rather than before the Draw, is what lets the launch
		// below happen after the last client write of the period instead of
		// before it: a frame that crosses an update is otherwise the one frame
		// a pre-record can never serve. The input snapshot it reads is the one
		// Ebitengine took for that Update call, so no snapshot is consumed
		// twice and none is dropped.
		a.updateBody()
		sampledAt = time.Now()
		tick16 = a.c.ResolveTickFraction()
	}
	// The next Draw sits one present period after this one began; the tick
	// fraction was sampled at sampledAt, which is later than this Draw's start
	// when an update body ran in between.
	a.launchPreRecord(now, sampledAt, period, tick16)
	a.pipe.observeTick(sampledAt, tick16)
}

// beginDraw is shared by both executors: an F10 transition can happen in the
// preceding modern Draw tail, before the next Update has a chance to join.
func (a *app) beginDraw() {
	a.c.JoinPreRecord()
	a.syncRendererSources()
	if a.mode == RendererClassic {
		// Joining alone leaves a speculative CRT snapshot pending; discard before
		// classic advances presentation so a later modern miss cannot rewind it.
		a.c.CancelPreRecord()
		a.pipe.armed = false
	}
}

// syncRendererSources follows terrain bindings, including returning to menus.
// It runs only after the recorder has joined and the client has finished a step.
func (a *app) syncRendererSources() {
	generation := a.c.TerrainGeneration()
	if generation == a.sourceGeneration {
		return
	}
	a.c.CancelPreRecord()
	a.pipe.armed = false
	a.paused.clear()
	a.paused.inputs = client.PausedWorldInputs{}
	if a.gpu != nil {
		a.gpu.ResetSources()
	}
	a.sourceGeneration = generation
}

// launchPreRecord rechecks the executor after the deferred Update: that body
// can process F10, so entering this Draw as modern is not sufficient.
func (a *app) launchPreRecord(now, sampledAt time.Time, period time.Duration, tick16 int32) {
	if a.mode == RendererModern && !a.exitPending && !a.c.PresentationPaused() && period > 0 {
		if nextTick16, nextCamera16, ok := a.pipe.predictNext(now.Add(period), sampledAt, a.updatedAt, tick16); ok {
			a.c.StartPreRecord(nextTick16, nextCamera16, true)
			a.pipe.armed = true
			a.pipe.launchPeriod = period
		}
	}
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

// syncWindowSize follows the selected host size. Ebitengine also remembers
// this size while fullscreen for restoration when returning to a window.
func (a *app) syncWindowSize() {
	width, height := a.desiredWindowSize()
	if width == a.windowW && height == a.windowH {
		return
	}
	a.windowW, a.windowH = width, height
	ebiten.SetWindowSize(width, height)
}

// desiredWindowSize keeps the OS window stable when the logical canvas changes
// for menus/loading/results. This is Nanolathe host presentation policy
// (DESIGN_PRESENTATION_CLIENT §2.1), not a change to retail's logical layout.
func (a *app) desiredWindowSize() (int, int) {
	if a.options.WindowSize != nil {
		if w, h := a.options.WindowSize(); w > 0 && h > 0 {
			return w, h
		}
	}
	return a.c.Size()
}

// Consume Alt+Enter before input publication so fullscreen cannot also activate
// the selected menu button or submit battle chat. Suppress the whole Enter hold,
// even if Alt is released first; holding the chord toggles only once.
func (a *app) consumeFullscreenShortcut(sample *sampledInput) bool {
	enter := sample.keys[input.KeyEnter]
	toggle := enter && sample.modifiers.Alt && !a.fullscreenEnterHeld
	if toggle {
		a.fullscreenEnterHeld = true
	}
	if a.fullscreenEnterHeld {
		sample.keys[input.KeyEnter] = false
		chars := sample.characters[:0]
		for _, r := range sample.characters {
			if r != '\r' && r != '\n' {
				chars = append(chars, r)
			}
		}
		sample.characters = chars
	}
	if !enter {
		a.fullscreenEnterHeld = false
	}
	return toggle
}

func (a *app) observeFullscreen(fullscreen bool) {
	if fullscreen == a.fullscreen {
		return
	}
	a.fullscreen = fullscreen
	a.presentPending = true
	if a.options.FullscreenChanged != nil {
		a.options.FullscreenChanged(fullscreen)
	}
}

// Layout keeps the logical resolution fixed; Ebitengine letterboxes if the
// window is resized.
func (a *app) Layout(outsideWidth, outsideHeight int) (int, int) {
	w, h := a.c.Size()
	if outsideWidth > 0 && outsideHeight > 0 {
		a.scrollPointScale = max(float64(w)/float64(outsideWidth), float64(h)/float64(outsideHeight))
	}
	return w, h
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

// Run starts the desktop main loop and blocks until the window closes. It
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
	game := &app{c: c, mode: mode, options: options, fullscreen: options.Fullscreen}
	game.c.SetDebugDeviceCapture(game.writeDebugDeviceCapture)
	defer game.c.SetDebugDeviceCapture(nil)
	defer game.paused.clear()
	width, height := game.desiredWindowSize()
	game.windowW, game.windowH = width, height
	// From here on this process owns a window: every window-API call below, and
	// everything the game loop reaches through app.Update, runs on this thread
	// with the window layer brought up. Arm the seam before the first of them.
	windowOwned.Store(true)
	// Native title-bar controls are host policy (DESIGN_PRESENTATION_CLIENT
	// §2.1). macOS supports fullscreen without drag resizing; other desktops
	// require a resizable window to expose their maximize button.
	if runtime.GOOS == "darwin" {
		ebiten.SetWindowResizingMode(ebiten.WindowResizingModeOnlyFullscreenEnabled)
	} else {
		ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	}
	ebiten.SetWindowSize(width, height)
	ebiten.SetFullscreen(options.Fullscreen)
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
	game.syncPresentationSettings()
	stopScrollMonitor, err := startNativeScrollMonitor()
	if err != nil {
		return err
	}
	defer stopScrollMonitor()
	return ebiten.RunGame(game)
}
