package main

import (
	"fmt"
	"image"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/platform/ebitenapp"
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
// while the frame is drawn. ComposeFrameSnapshot prepares the committed draw
// list once, and the modern executor replays that same list (C-G1). In both
// mode the classic image and GPU list come from that one snapshot, making the
// comparison a genuine two-executor parity check (C-G11).
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
		snapshot := cl.ComposeFrameSnapshot()
		prepared = &snapshot
		preparation = time.Since(started)
		preparationLabel = "CPU compose+snapshot"
	}
	game := &modernShotGame{cl: cl, list: &prepared.List, w: w, h: h, profileFrames: profileFrames, preparation: preparation, preparationLabel: preparationLabel}

	// SetWindowVisible(false) before RunGame runs the game without ever showing
	// the window (Ebitengine window contract). The size is set so the default
	// screen matches the capture surface; the pixels are read from the renderer's
	// own w×h output image rather than the screen, so the host's HiDPI device
	// scale factor never enters the capture.
	ebiten.SetWindowVisible(false)
	ebiten.SetWindowSize(w, h)
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
	out              *image.RGBA
	err              error
	done             bool
	profileFrames    int
	profileStats     *gpuProfileStats
	profileFrame     int
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

// Draw replays the prepared committed frame through the GPU executor and reads
// the expanded RGBA back — all inside the loop, where GPU operations are valid.
// ReadPixels returns premultiplied-alpha bytes; the expansion pass forces alpha
// opaque (C-G8), so for these pixels premultiplied equals straight and the
// buffer is a straight RGBA image directly comparable to the classic capture.
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
		g.readback = make([]byte, 4*g.w*g.h)
	}
	if g.profileFrames > 0 {
		g.profileFrame++
	}
	// The model table was populated while the prepared snapshot was recorded, so
	// the source is installed before every replay — the same source the battle
	// app installs, so both modern paths draw models identically (C-G5).
	g.gpu.SetModelSource(ebitenapp.NewModelSource(g.cl))
	submitStart := time.Now()
	img := g.gpu.Execute(g.list, g.w, g.h)
	submission := time.Since(submitStart)
	if img == nil {
		g.err = fmt.Errorf("nanolathe: shot: modern executor returned no surface for %dx%d", g.w, g.h)
		g.done = true
		return
	}
	if g.profileFrames > 0 {
		// ReadPixels is intentionally inside the timed region. It synchronizes
		// deferred device work, so this is a diagnostic of Execute plus the
		// readback stall, not a claim about GPU execution time [DESIGN_GPU_RENDERER.md §6].
		if g.profileFrame > gpuProfileWarmupFrames {
			syncStart := submitStart
			img.ReadPixels(g.readback)
			synchronized := time.Since(syncStart)
			g.profileStats.add(submission, synchronized, synchronized-submission)
		} else {
			img.ReadPixels(g.readback)
		}
	} else {
		g.readback = make([]byte, 4*g.w*g.h)
		img.ReadPixels(g.readback)
	}
	// Only the final frame is retained as a capture. Keeping intermediate
	// frames device-backed avoids letting PNG-image allocations perturb later
	// measured frames.
	if g.profileFrames == 0 || g.profileFrame >= gpuProfileWarmupFrames+g.profileFrames {
		out := image.NewRGBA(image.Rect(0, 0, g.w, g.h))
		copy(out.Pix, g.readback)
		g.out = out
	}
	screen.DrawImage(img, &ebiten.DrawImageOptions{})
	if g.profileFrames == 0 || g.profileFrame >= gpuProfileWarmupFrames+g.profileFrames {
		g.done = true
	}
}

// Layout fixes the logical resolution at the capture surface; no resize path is
// exercised during the finite capture loop.
func (g *modernShotGame) Layout(int, int) (int, int) { return g.w, g.h }
