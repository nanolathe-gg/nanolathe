package main

import (
	"fmt"
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe/nanolathe/internal/platform/gpurender"
)

// captureModernShot composes one frame through the modern (GPU) executor and
// returns it as an RGBA image, without ever showing a window
// [DESIGN_GPU_RENDERER.md §2.5]. GPU resources (images, shaders, ReadPixels)
// only exist inside a running Ebitengine loop, so the capture enters the loop
// with the window hidden, does all device work in a single Draw, reads the
// expanded surface back there, and terminates after that one frame.
//
// The client is already stepped to the captured tick and its published buffer is
// frozen; this loop never calls Client.Step, so the simulation does not advance
// while the frame is drawn. RecordFrame records the same committed frame the
// classic ComposeFrame would, and the modern executor replays it (C-G1) — the
// classic path is never touched, so the comparison in --shot-renderer both is a
// genuine two-executor parity check (C-G11).
func captureModernShot(cl *client.Client, w, h int, mapName string) (*image.RGBA, error) {
	if cl == nil {
		return nil, fmt.Errorf("nanolathe: shot: modern capture has no client")
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("nanolathe: shot: modern capture wants a positive surface, got %dx%d", w, h)
	}
	game := &modernShotGame{cl: cl, w: w, h: h}

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
	return game.out, nil
}

// modernShotGame is the minimal Ebitengine game that captures exactly one modern
// frame. Update runs first and returns nil once; Draw then does all the device
// work and marks the capture done; the next Update returns ebiten.Termination,
// so the loop ends after a single presented frame.
type modernShotGame struct {
	cl   *client.Client
	w, h int

	gpu  *gpurender.Renderer
	out  *image.RGBA
	err  error
	done bool
}

// Update terminates the loop once the single frame has been captured in Draw.
// Ebitengine always calls Update before Draw, so the first Update returns nil and
// lets one Draw run.
func (g *modernShotGame) Update() error {
	if g.done {
		return ebiten.Termination
	}
	return nil
}

// Draw records the committed frame, replays it through the GPU executor, and
// reads the expanded RGBA back — all inside the loop, where GPU operations are
// valid. ReadPixels returns premultiplied-alpha bytes; the expansion pass forces
// alpha opaque (C-G8), so for these pixels premultiplied equals straight and the
// buffer is a straight RGBA image directly comparable to the classic capture.
func (g *modernShotGame) Draw(screen *ebiten.Image) {
	if g.done {
		return
	}
	g.done = true
	if g.gpu == nil {
		g.gpu = gpurender.New(g.cl.PaletteTables(), g.w, g.h)
	}
	list := g.cl.RecordFrame()
	// The model table the source reads is populated during RecordFrame, so the
	// source is installed after recording and before Execute — the same source the
	// battle app installs, so both modern paths draw models identically (C-G5).
	g.gpu.SetModelSource(ebitenapp.NewModelSource(g.cl))
	img := g.gpu.Execute(list, g.w, g.h)
	if img == nil {
		g.err = fmt.Errorf("nanolathe: shot: modern executor returned no surface for %dx%d", g.w, g.h)
		return
	}
	buf := make([]byte, 4*g.w*g.h)
	img.ReadPixels(buf)
	out := image.NewRGBA(image.Rect(0, 0, g.w, g.h))
	copy(out.Pix, buf)
	g.out = out
}

// Layout fixes the logical resolution at the capture surface; the loop presents
// exactly one frame, so no resize path is exercised.
func (g *modernShotGame) Layout(int, int) (int, int) { return g.w, g.h }
