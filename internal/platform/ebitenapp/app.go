package ebitenapp

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/audiobackend"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"
)

// presentationTPS is the host work rate. It stays at 30: every per-host-
// frame step the battle takes — input edges, the follow-camera glide's per-frame
// step [07 R-CAM-01 §12], the scroll pass [07 §10], the sub-tick budget
// [01 §4.2] — keeps the cadence retail gives it. Draw is called at the display's
// refresh rate, alongside lightweight input polling, which is what Enhanced presents on
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
	sourcesPrepared  bool
	// rendererToggles is the client's executor-swap request count this adapter
	// has already acted on. Update compares it with the client's own count, so
	// one F10 press swaps once (docs/DESIGN_GPU_RENDERER.md §14.6).
	rendererToggles int
	// scrollPointScale converts native window points to the letterboxed surface.
	scrollPointScale float64
	// windowW/windowH are the last selected host size. Logical menu/battle
	// transitions do not change them (DESIGN_PRESENTATION_CLIENT §2.1).
	windowW, windowH       int
	options                RunOptions
	fullscreen             bool
	fullscreenEnterHeld    bool
	fullscreenPresentation *nativeFullscreenPresentation
	// cursorClip keeps the pointer on the presented canvas in fullscreen
	// (presentedCursorRect); a no-op on hosts without a native clip.
	cursorClip nativeCursorClip
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
	// updatedAt is when the last host body returned. It measures how far the
	// window is through the current host step, which is the camera's blend
	// fraction (§13.5) — the camera moves on this grid, not on the simulation's
	// scaled units. Like inputStarted it is platform time and never reaches the
	// client's clock or the sim [I6].
	updatedAt time.Time
	// presentInterval is the minimum spacing between two presented modern
	// frames, zero for the display's own refresh; presentedAt is when the last
	// one was presented; refresh measures the window's current refresh period
	// from Draw arrivals. See RunOptions.MaxFPS and presentDue.
	presentInterval time.Duration
	presentedAt     time.Time
	refresh         refreshEstimate
	// pipe is the record/submit pipeline's host state
	// (docs/DESIGN_GPU_RENDERER.md §13.10). It is modern-only: the classic path
	// never launches a pre-record and never joins one it did not launch.
	pipe pipeline
	// ledger decides whether a scheduled host body runs in Update or at the end
	// of the modern Draw that follows it, and guarantees one body per step either
	// way (§13.10).
	ledger updateLedger
	// Input snapshots arrive at display refresh. Only the fixed host clock may
	// issue a ledger call; its queued sample survives until the deferred body.
	hostClock     hostClock
	hostInput     hostInputBuffer
	hostSamples   []sampledInput
	inputPolls    int64
	inputPollTime time.Duration // measured only with --stats; elapsed, not CPU time
	// exitPending records an exit request seen from a Draw tail, where a
	// Termination cannot be returned; the next Update call returns it.
	exitPending bool
	// bodies counts update bodies run, for the pipeline readout's cadence
	// sanity line: bodies per second must stay at the update rate however the
	// deferral moves them.
	bodies int64
	// loopThreadRaised records that the game loop goroutine has pinned and
	// raised its thread (thread_priority_darwin.go).
	loopThreadRaised bool
	// fpsCounter measures completed modern presentations, including paused
	// foreground redraws, rather than Ebitengine's uncapped Draw callbacks.
	fpsCounter fpsCounter
	// fpsSim accumulates client/authoritative step wall time until the next
	// completed modern Draw. The host step may run in Update or the Draw tail.
	fpsSim time.Duration
	// fpsGraph is a small host-only bitmap, reused while the overlay is visible.
	fpsGraph       *ebiten.Image
	fpsGraphPixels []byte
	// fpsPasses is the device passes the last presented frame issued
	// (gpurender.ModelStats.Passes), shown while the overlay is visible.
	fpsPasses int
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
	// Effects supplies the committed live Enhanced effect selection
	// (DESIGN_GPU_RENDERER §30). Nil leaves the client's own selection alone,
	// which is every effect on.
	Effects func() drawlist.Effects
	// ShowFPS enables a battle-only counter on the modern presentation surface.
	// It is a host display preference and never reaches the client or session.
	ShowFPS func() bool
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

// Update refreshes input once per display frame. The separate host clock runs
// camera/menu work at 30 Hz; the session still owns its authoritative sub-tick
// accumulator. Input between host steps is retained instead of discarded.
// Modern may defer a host body to the Draw tail, preserving the record/submit
// pipeline (§13.10). Its sample is queued until that body actually runs.
func (a *app) Update() error {
	if !a.loopThreadRaised {
		RaiseCurrentThread()
		a.loopThreadRaised = true
	}
	if err := a.gpu.FogContentError(); err != nil {
		a.c.JoinPreRecord()
		return err
	}
	if a.exitPending {
		a.c.JoinPreRecord()
		return a.terminate()
	}
	var pollStart time.Time
	if a.options.Stats {
		pollStart = time.Now()
	}
	a.hostInput.add(readInput(a.scaledInputNow()))
	a.inputPolls++
	if a.options.Stats {
		a.inputPollTime += time.Since(pollStart)
	}
	steps := a.hostClock.advance(time.Now())
	if steps == 0 {
		return nil
	}
	// The pipeline's barrier. The body below writes client state — input, focus,
	// the step and its publication, the pointer mode, the executor swap — and
	// none of it may run while the pre-record is still reading
	// (docs/DESIGN_GPU_RENDERER.md §13.10). A deferring call writes nothing, so
	// the record it leaves running is still valid at the Draw that consumes it;
	// the join costs nothing there because Draw would join immediately after.
	a.c.JoinPreRecord()
	// The ledger decides where this call's body runs. The modern executor with
	// a live Draw tail defers it; everything else runs it here and now.
	for range steps {
		a.hostSamples = append(a.hostSamples, a.hostInput.take())
		for range a.ledger.call(a.mode == RendererModern && a.gpu != nil) {
			if a.exitPending {
				break
			}
			a.updateBody()
		}
		if a.exitPending {
			break
		}
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
	sample := a.hostSamples[0]
	copy(a.hostSamples, a.hostSamples[1:])
	a.hostSamples[len(a.hostSamples)-1] = sampledInput{}
	a.hostSamples = a.hostSamples[:len(a.hostSamples)-1]
	if a.scrollPointScale > 0 {
		sample.panX *= a.scrollPointScale
		sample.panY *= a.scrollPointScale
	}
	if a.consumeFullscreenShortcut(&sample) {
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
	}
	a.observeFullscreen(ebiten.IsFullscreen())
	a.fullscreenPresentation.update(a.fullscreen, ebiten.IsFocused())
	applyInput(a.c.Input(), sample)
	a.c.SetFocused(ebiten.IsFocused())
	a.stepClient()
	a.syncRendererSources()
	a.syncWindowSize()
	a.syncPointerCapture()
	a.syncCursorClip()
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
	a.cursorClip.release()
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
	if a.options.Effects != nil {
		// The client owns the recorder-side gates; the executor is given the
		// same selection beside the palette on each present (§30).
		a.c.SetEffects(a.options.Effects())
	}
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
		// Original presents the committed tick after each update, on the
		// synchronous path (§13.13).
		a.c.SetAsyncSimulation(false)
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
	var started time.Time
	if a.mode == RendererModern && a.options.ShowFPS != nil && a.options.ShowFPS() {
		started = time.Now()
	}
	a.c.Step(1.0 / float64(presentationTPS))
	if !started.IsZero() {
		// Under the asynchronous simulation the step no longer contains the
		// sub-ticks; the batch it joined reports its own goroutine time (§13.13).
		a.fpsSim += time.Since(started) + a.c.TakeSimulationTime()
	}
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

// syncCursorClip runs after the capture mode is settled, so a capture that
// just ended (and cleared the host clip) is re-confined in the same update.
// Windowed play leaves the pointer free (DESIGN_PRESENTATION_CLIENT §2.1).
func (a *app) syncCursorClip() {
	width, height := a.c.Size()
	a.cursorClip.update(a.fullscreen, a.c.IsFocused(), a.c.PointerCaptured(), width, height)
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
	// The arrival is taken before the pre-record join: it is the refresh this
	// Draw belongs to, and the join's wait is not part of it.
	arrived := time.Now()
	showFPS := a.mode == RendererModern && a.options.ShowFPS != nil && a.options.ShowFPS()
	var drawStarted time.Time
	if showFPS {
		drawStarted = arrived
	}
	a.beginDraw()
	a.c.SetFrameTiming(showFPS)
	width, height := a.c.Size()
	// Enhanced presents on every Draw — that is the whole of the refresh-rate
	// cadence — so it does not consume the update's pending flag; the blended
	// view differs between two Draws of one update (§13.5).
	if a.mode == RendererModern {
		if !a.presentDue(arrived) {
			return
		}
		if showFPS && a.fpsCounter.target != a.presentInterval {
			// A live cap change starts a new history with one budget.
			a.fpsCounter = fpsCounter{target: a.presentInterval}
			a.fpsSim = 0
		} else if !showFPS {
			a.fpsCounter = fpsCounter{}
			a.fpsSim = 0
		}
		blend, record, submit := a.drawModern(screen, width, height, showFPS)
		if showFPS {
			completed := time.Now()
			a.fpsCounter.observe(completed, completed.Sub(drawStarted), a.fpsSim, blend, record, submit)
			a.fpsSim = 0
		}
		return
	}
	a.fpsCounter = fpsCounter{}
	a.fpsSim = 0
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
func (a *app) drawModern(screen *ebiten.Image, width, height int, showFPS bool) (blend, record, submit time.Duration) {
	if !a.sourcesPrepared {
		// Direct --map entry and the first classic-to-modern switch have no
		// modern loading callback. Prepare once before their first battle draw.
		a.prepareBattlePresentation()
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
		// The window runs the session's sub-ticks on their own goroutine while
		// it presents; the next host step starts it (§13.13).
		a.c.SetAsyncSimulation(true)
		a.interpolating = true
	}
	// Every read this Draw makes of the committed frames — the displayed
	// resources, the digest, a synchronous record — goes through the pair
	// pinned here (§13.13).
	a.c.PinPresentation()
	// Audio stays here, on the game goroutine and once per presented frame,
	// whether the list was pre-recorded or not [03 §8.3] C18. The caption ring
	// it can write is in the pipeline's digest, so a drain that changed what
	// the recorder reads discards the pre-record rather than presenting a list
	// recorded before it (§13.10).
	a.c.BeginPresentationFrame()
	// The one wall-clock sample this presented frame takes. The digest below
	// compares it and a synchronous record consumes it, so the miss path records
	// the frame the digest named instead of a second, later sample (§13.10).
	tick16 := a.c.ResolveTickFraction()
	// Present at the fraction the list was predicted for when the prediction
	// held to within one present interval, and take the exact path when it did
	// not (§13.10). The interval is measured, so the tolerance follows the
	// display the window is actually running on.
	// The interval is the one the outstanding prediction was made over, so a
	// frame that arrived late cannot widen the tolerance by its own lateness.
	paused, pausedRecord, pausedSubmit := a.drawPaused(screen, width, height, showFPS)
	if paused {
		record, submit = pausedRecord, pausedSubmit
	} else {
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
			var started time.Time
			if showFPS {
				started = time.Now()
			}
			list = a.c.RecordModernFrame()
			if showFPS {
				record = time.Since(started)
			}
		} else if showFPS {
			record = time.Duration(a.c.PreRecordNanos())
		}
		if showFPS {
			blend = time.Duration(a.c.FrameBlendNanos())
		}
		a.gpu.SetDisplayPalette(a.c.DisplayPalette())
		a.gpu.SetGlow(a.c.Glow())
		a.gpu.SetGlowStrength(a.c.GlowStrength())
		a.gpu.SetGlowFamilies(a.c.GlowFamilies())
		a.gpu.SetEffects(a.c.Effects())
		// Place only the cursor from a fresh host sample after the recorder
		// joins. Command input remains on the ordinary host step [07 §8].
		x, y := ebiten.CursorPosition()
		a.c.PositionPresentationCursor(list, x, y)
		var submitStarted time.Time
		if showFPS {
			submitStarted = time.Now()
		}
		img := a.gpu.Execute(list, width, height)
		if showFPS {
			submit = time.Since(submitStarted)
			a.fpsPasses = a.gpu.ModelStats().Passes
		}
		if img != nil {
			a.c.CommitStrategicPresentation()
			screen.DrawImage(img, &ebiten.DrawImageOptions{})
			a.c.MarkArrivalPresented()
		}
	}
	// The overlay is drawn after the GPU surface and outside the recorded list:
	// screenshots and the simulation remain independent of host frame timing.
	if showFPS {
		a.drawFPSOverlay(screen, width)
	}
	// Execute has enqueued this frame and copied what the device needs, so the
	// list and the recorder's scratch are free again. Spend the flush and the
	// swap that follow this Draw on the update this frame owes and then on
	// recording the next frame (§13.10).
	a.reportPipelinePeriodically()
	sampledAt := now
	if a.ledger.tail() {
		// The host step scheduled before this Draw. Running it here,
		// after Execute rather than before the Draw, is what lets the launch
		// below happen after the last client write of the period instead of
		// before it: a frame that crosses an update is otherwise the one frame
		// a pre-record can never serve. It consumes the queued input sample
		// for this host step exactly once.
		a.updateBody()
		sampledAt = time.Now()
		// This frame has already been presented; the body may have released a
		// tick, so the prediction below needs a base taken after it. It is the
		// next frame's sample, not a second one for this frame — the fraction
		// the presented list was recorded with is settled and untouched by it.
		tick16 = a.c.ResolveTickFraction()
	}
	// The next Draw sits one present period after this one began; the tick
	// fraction was sampled at sampledAt, which is later than this Draw's start
	// when an update body ran in between.
	a.launchPreRecord(now, sampledAt, period, tick16)
	a.pipe.observeTick(sampledAt, tick16)
	return blend, record, submit
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
	a.sourcesPrepared = false
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

// presentDue applies RunOptions.MaxFPS to one modern Draw arriving at now.
// The screen is retained between Draws (Run configures that), so a skipped Draw
// leaves the last presented frame on the display.
//
// While the window refreshes faster than the cap, a Draw that arrives sooner
// than the cap's interval, less an eighth of it for vsync jitter, is skipped:
// a 60 cap presents every other refresh at 120 Hz. The refresh is not fixed,
// though: a ProMotion panel drops to 60 Hz while a battle runs and switches
// back later. Timed in the window, at 60 Hz that test skipped the Draw after
// any present more than 2 ms late — the next refresh then arrives early
// relative to it — so one hitch cost two refreshes and the counter read 55-59.
// When the measured refresh is no faster than the cap, every refresh is due;
// only a Draw less than half a refresh after the last present, one of a burst
// of back-to-back Draws, is skipped.
func (a *app) presentDue(now time.Time) bool {
	refresh := a.refresh.observe(now)
	if a.presentInterval > 0 && !a.presentedAt.IsZero() {
		allowance := a.presentInterval / 8
		threshold := a.presentInterval - allowance
		if refresh >= threshold {
			threshold = refresh / 2
		}
		if now.Sub(a.presentedAt) < threshold {
			return false
		}
	}
	a.presentedAt = now
	return true
}

// refreshEstimate measures the refresh period the window is running at from
// Draw arrivals: half the median spacing of two consecutive Draws. A heavy
// frame and the light one after it arrive long then short, as do a late
// present and the refresh after it, and a stall arrives once; pairs absorb the
// first two and the median the third.
type refreshEstimate struct {
	last      time.Time
	intervals [16]time.Duration
	count     int
	next      int
}

func (r *refreshEstimate) observe(now time.Time) time.Duration {
	if !r.last.IsZero() {
		if d := now.Sub(r.last); d > 0 {
			r.intervals[r.next] = d
			r.next = (r.next + 1) % len(r.intervals)
			r.count = min(r.count+1, len(r.intervals))
		}
	}
	r.last = now
	if r.count < 4 {
		return 0
	}
	var pairs [len(r.intervals) - 1]time.Duration
	start := (r.next - r.count + len(r.intervals)) % len(r.intervals)
	for i := range r.count - 1 {
		pairs[i] = r.intervals[(start+i)%len(r.intervals)] + r.intervals[(start+i+1)%len(r.intervals)]
	}
	window := pairs[:r.count-1]
	slices.Sort(window)
	return window[len(window)/2] / 2
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
	a.c.SetOutsideSize(outsideWidth, outsideHeight)
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
// treat it as "no desktop metrics": the options list keeps its fixed presets
// and saved selection, omitting desktop-gated and monitor-derived additions.
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

// DesktopPixelSize reports the current monitor's size in physical pixels, or
// (0, 0) under the same conditions as DesktopSize. It is the Nanolathe host
// query behind the options page's native-resolution choice
// (DESIGN_PRESENTATION_CLIENT §2.1): under desktop display scaling (Windows
// 125%, a Retina panel) DesktopSize's device-independent size is smaller than
// the panel, so a 3440x1440 monitor at 125% reports 2752x1152.
//
// Ebitengine exposes the device-independent size and the device scale factor,
// not the monitor's pixel bounds, and on Windows and Linux the size it reports
// is the pixel count divided by the scale factor and then truncated. The pixel
// count is recovered from those two by monitorPixels.
func DesktopPixelSize() (int, int) {
	if !windowOwned.Load() {
		return 0, 0
	}
	monitor := ebiten.Monitor()
	if monitor == nil {
		return 0, 0
	}
	w, h := monitor.Size()
	scale := monitor.DeviceScaleFactor()
	return monitorPixels(w, scale), monitorPixels(h, scale)
}

// monitorPixels recovers one physical monitor dimension from its truncated
// device-independent size. Every pixel count p with trunc(p / scale) == dip lies
// in [dip*scale, (dip+1)*scale); at a scale factor up to 2 that interval holds
// at most two consecutive integers, and monitor panels have even dimensions,
// so the even candidate is taken. On macOS the device-independent size is
// exact and the interval starts at the answer, which is then also even. A
// missing or unit scale leaves the size as reported.
//
// TODO(question): Ebitengine does not expose a monitor's pixel bounds; if a
// later release does, read them directly instead of reconstructing them. A
// scale factor above 2 can leave more than one even candidate, and this takes
// the first.
func monitorPixels(dip int, scale float64) int {
	if dip <= 0 {
		return 0
	}
	if !(scale > 1) {
		return dip
	}
	const slack = 1e-6
	lo := int(math.Ceil(float64(dip)*scale - slack))
	hi := max(lo, int(math.Ceil(float64(dip+1)*scale-slack))-1)
	for p := lo; p <= hi; p++ {
		if p%2 == 0 {
			return p
		}
	}
	return lo
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
	game.c.SetBattlePresentationPreparer(game.prepareBattlePresentation)
	defer game.c.SetBattlePresentationPreparer(nil)
	game.c.SetDebugDeviceCapture(game.writeDebugDeviceCapture)
	// The pre-record goroutine is one of the frame's critical threads; the
	// main (render) thread is raised below and the game loop at its first
	// Update (thread_priority_darwin.go).
	game.c.SetWorkerThreadSetup(RaiseCurrentThread)
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
	ebiten.SetTPS(ebiten.SyncWithFPS)
	game.syncPresentationSettings()
	stopScrollMonitor, err := startNativeScrollMonitor()
	if err != nil {
		return err
	}
	defer stopScrollMonitor()
	game.fullscreenPresentation = startNativeFullscreenPresentation()
	defer game.fullscreenPresentation.close()
	defer game.cursorClip.release()
	RaiseCurrentThread()
	return ebiten.RunGame(game)
}
