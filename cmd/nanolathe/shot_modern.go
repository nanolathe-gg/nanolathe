package main

import (
	"fmt"
	"image"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"
)

// captureModernShot composes one frame through the modern (GPU) executor and
// returns it as an RGBA image, without ever showing a window
// [DESIGN_GPU_RENDERER.md §2.5]. GPU resources (images, shaders, ReadPixels)
// only exist inside a running Ebitengine loop, so the capture enters the loop
// with the window hidden, does all device work in Draw, reads the expanded
// surface back there, and terminates after the requested finite sequence.
//
// The client is already stepped to the captured tick and its published buffer is
// frozen; this loop never calls Client.Step, so the simulation does not advance
// while the frame is drawn. It records geometry once and replays that cloned
// list (C-G1). The explicit both route first composes its classic image, then
// uses this same geometry recording path from the unchanged committed state.
func captureModernShot(cl *client.Client, w, h int, mapName string, profileFrames int) (*image.RGBA, error) {
	if cl == nil {
		return nil, fmt.Errorf("nanolathe: shot: modern capture has no client")
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("nanolathe: shot: modern capture wants a positive surface, got %dx%d", w, h)
	}
	if profileFrames < 0 {
		return nil, fmt.Errorf("nanolathe: shot: modern capture profile frame count must be nonnegative, got %d", profileFrames)
	}
	started := time.Now()
	// The capture caller already advanced this presentation frame. Repeated
	// composition, including the both route, must retain its stock pair.
	live := cl.RecordModernFrame()
	if live == nil {
		return nil, fmt.Errorf("nanolathe: shot: modern capture recorded no frame")
	}
	list := live.Clone()
	game := &modernShotGame{cl: cl, list: &list, w: w, h: h, profileFrames: profileFrames, preparation: time.Since(started), preparationLabel: "modern geometry/draw-list recording (CPU)"}

	// SetWindowVisible(false) before RunGame runs the game without ever showing
	// the window (Ebitengine window contract). The size is set so the default
	// screen matches the capture surface; the pixels are read from the renderer's
	// own w×h output image rather than the screen, so the host's HiDPI device
	// scale factor never enters the capture.
	ebiten.SetWindowVisible(false)
	ebiten.SetWindowSize(w, h)
	if profileFrames > 0 {
		// Profiling is opt-in. Remove pacing and permit a hidden window to keep
		// drawing so the observed cadence reflects device backpressure and host
		// scheduling rather than vsync or focus throttling.
		ebiten.SetVsyncEnabled(false)
		ebiten.SetTPS(ebiten.SyncWithFPS)
		ebiten.SetRunnableOnUnfocused(true)
	}
	if mapName != "" {
		ebiten.SetWindowTitle("Nanolathe — " + mapName)
	}
	if err := ebiten.RunGame(game); err != nil {
		return nil, fmt.Errorf("nanolathe: shot: modern capture loop: %w", err)
	}
	if game.err != nil {
		return nil, game.err
	}
	if game.out == nil {
		return nil, fmt.Errorf("nanolathe: shot: modern capture produced no frame")
	}
	ms := game.modelStats
	fmt.Fprintf(os.Stderr, "nanolathe: modern model route: scene=%q gpu=%d skipped=%d shadows=%d shadows-omitted=%d no-body=%d lane-subjects=%d lane-shadows=%d lane-faces=%d lane-overflow=%d lane-pages=%d lane-rows=%d\n", mapName, ms.GPU, ms.Skipped, ms.Shadows, ms.ShadowsOmitted, ms.NoBody, ms.DirectSubjects, ms.DirectShadows, ms.DirectFaces, ms.DirectOverflow, ms.DirectPages, ms.DirectAtlasRows)
	// The Enhanced lighting census: the selected sources by family and the
	// ground discs they batched (DESIGN_GPU_RENDERER §31).
	fmt.Fprintf(os.Stderr, "nanolathe: modern lighting: sources=%d explosion=%d nano=%d fire=%d projectile=%d wreck=%d ground=%d lit-faces=%d lit-smoke=%d\n",
		ms.BattleLights, ms.BattleLightKinds[0], ms.BattleLightKinds[1], ms.BattleLightKinds[2],
		ms.BattleLightKinds[3], ms.BattleLightKinds[4], ms.GroundLights, ms.LitModelFaces, ms.LitSmokeSprites)
	if game.profileFrames > 0 {
		got := 0
		if game.profileStats != nil {
			got = len(game.profileStats.submission)
		}
		if got != game.profileFrames {
			return nil, fmt.Errorf("nanolathe: shot: GPU profile captured %d/%d frames", got, game.profileFrames)
		}
		printGPUProfile(game.profileStats, w, h, mapName, game.preparation, game.preparationLabel)
	}
	return game.out, nil
}

// modernShotGame is the minimal Ebitengine game that captures one modern frame,
// or runs a finite warm-up and profiling sequence before retaining its final
// frame. Update returns termination only after Draw has completed that sequence.
type modernShotGame struct {
	cl   *client.Client
	list *drawlist.List
	w, h int

	gpu              *gpurender.Renderer
	modelStats       gpurender.ModelStats
	out              *image.RGBA
	err              error
	done             bool
	profileFrames    int
	profileStats     *gpuProfileStats
	profileWindow    *gpuProfileWindow
	readback         []byte
	preparation      time.Duration
	preparationLabel string
}

// Update terminates the loop once Draw has completed the capture or profiling
// sequence. Ebitengine always calls Update before Draw, so the first Update
// returns nil and lets the first device-backed draw run.
func (g *modernShotGame) Update() error {
	if g.done {
		return ebiten.Termination
	}
	return nil
}

// Draw replays the prepared committed frame through the GPU executor. A profile
// deliberately does no ReadPixels during warm-up or measured draws: it times
// Execute plus screen.DrawImage enqueue and observes draw-start cadence. One
// final readback happens only after the cadence and submission arrays are full.
func (g *modernShotGame) Draw(screen *ebiten.Image) {
	if g.done {
		return
	}
	if g.gpu == nil {
		var err error
		g.gpu, err = gpurender.NewChecked(g.cl.PaletteTables(), g.w, g.h)
		if err != nil {
			g.err = fmt.Errorf("nanolathe: shot: modern renderer setup: %w", err)
			g.done = true
			return
		}
	}
	if g.profileFrames > 0 && g.profileStats == nil {
		g.profileStats = newGPUProfileStats(g.profileFrames)
		g.profileWindow = newGPUProfileWindow(g.profileFrames)
		g.readback = make([]byte, 4*g.w*g.h)
	}
	var profileMeasured bool
	if g.profileFrames > 0 {
		drawStart := time.Now()
		var cadence time.Duration
		var hasCadence bool
		profileMeasured, cadence, hasCadence = g.profileWindow.observeDraw(drawStart)
		if hasCadence {
			g.profileStats.addCadence(cadence)
		}
	}
	var submitStart time.Time
	if profileMeasured {
		submitStart = time.Now()
	}
	g.gpu.SetDisplayPalette(g.cl.DisplayPalette())
	g.gpu.SetGlow(g.cl.Glow())
	g.gpu.SetEffects(g.cl.Effects())
	img := g.gpu.Execute(g.list, g.w, g.h)
	if img == nil {
		g.err = fmt.Errorf("nanolathe: shot: modern executor returned no surface for %dx%d", g.w, g.h)
		g.done = true
		return
	}
	screen.DrawImage(img, &ebiten.DrawImageOptions{})
	if profileMeasured {
		g.profileStats.addSubmission(time.Since(submitStart))
	}
	g.modelStats = g.gpu.ModelStats()

	profileComplete := g.profileFrames > 0 && g.profileWindow.complete() && g.profileStats.complete()
	if g.profileFrames == 0 || profileComplete {
		if g.readback == nil {
			g.readback = make([]byte, 4*g.w*g.h)
		}
		// ReadPixels returns premultiplied-alpha bytes; expansion forces alpha
		// opaque (C-G8), so the retained buffer is directly comparable to the
		// classic capture. It is intentionally outside every profile sample.
		img.ReadPixels(g.readback)
		out := image.NewRGBA(image.Rect(0, 0, g.w, g.h))
		copy(out.Pix, g.readback)
		g.out = out
		g.done = true
	}
}

// Layout fixes the logical resolution at the capture surface; no resize path is
// exercised during the finite capture loop.
func (g *modernShotGame) Layout(int, int) (int, int) { return g.w, g.h }
