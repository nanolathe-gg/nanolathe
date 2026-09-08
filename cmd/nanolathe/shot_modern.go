package main

import (
	"fmt"
	"image"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/platform/gpurender"
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
// while the frame is drawn. Modern-only capture records geometry once and
// replays that cloned list (C-G1). Explicit both capture passes a snapshot whose
// classic image remains a diagnostic reference; its pixels never enter the GPU.
func captureModernShot(cl *client.Client, w, h int, mapName string, profileFrames int, prepared *client.ComposedFrameSnapshot, preparation time.Duration, preparationLabel string) (*image.RGBA, error) {
	if cl == nil {
		return nil, fmt.Errorf("nanolathe: shot: modern capture has no client")
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("nanolathe: shot: modern capture wants a positive surface, got %dx%d", w, h)
	}
	if profileFrames < 0 {
		return nil, fmt.Errorf("nanolathe: shot: modern capture profile frame count must be nonnegative, got %d", profileFrames)
	}
	if prepared == nil {
		started := time.Now()
		live := cl.RecordFrame()
		if live == nil {
			return nil, fmt.Errorf("nanolathe: shot: modern capture recorded no frame")
		}
		list := live.Clone()
		prepared = &client.ComposedFrameSnapshot{List: list}
		preparation = time.Since(started)
		preparationLabel = "modern geometry/draw-list recording (CPU)"
	}
	game := &modernShotGame{cl: cl, list: &prepared.List, w: w, h: h, profileFrames: profileFrames, preparation: preparation, preparationLabel: preparationLabel}

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
	fmt.Fprintf(os.Stderr, "nanolathe: modern model route: scene=%q gpu=%d skipped=%d shadows=%d shadows-omitted=%d reveal-outline-omitted=%d waterline-digger-omitted=%d staging-commands-omitted=%d staged-groups=%d composed-groups=%d structure-resolves=%d no-body=%d unsupported-geometry=%d unsupported-face=%d missing-texture=%d folded-faces=%d folded-strips=%d\n", mapName, ms.GPU, ms.Skipped, ms.Shadows, ms.ShadowsOmitted, ms.RevealOrOutlineOmitted, ms.WaterlineOrDiggerOmitted, ms.StagingCommandsOmitted, ms.StagedGroups, ms.ComposedGroups, ms.StructureResolves, ms.NoBody, ms.UnsupportedGeometry, ms.UnsupportedFace, ms.MissingTexture, ms.FoldedFaces, ms.FoldedStrips)
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
