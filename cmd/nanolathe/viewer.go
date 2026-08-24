// Package main – Gate 1 terrain viewer.
//
// This is the minimal vertical slice that makes the engine visible.
// It loads real TNT terrain and palette data, creates a camera and a
// Kaiju window, and drives the frame loop with correct alpha handling.
// It is intentionally small: no units, no fog, no sim subsystems beyond
// a stub tick for the overlay.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/world"
)

// runViewer implements Gate 1: windowed terrain viewer.
// It is called from run() when --map is set and --headless is false.
// It blocks until the window closes.
func runViewer(opts Options, cs *contentSet) error {
	// Compile catalog (needed for world.Load to resolve OTA overrides).
	cat, err := content.Compile(cs.fs)
	if err != nil {
		return fmt.Errorf("nanolathe: catalog: %w", err)
	}
	// Load terrain for the requested map.
	terrain, err := world.Load(cs.fs, cat, opts.Map)
	if err != nil {
		return fmt.Errorf("nanolathe: terrain %q: %w", opts.Map, err)
	}
	// Load palette tables [03 §4.3] C7. If they fail, viewer still opens with
	// fallback grayscale so the window itself is testable.
	var pal *palette.Tables
	if p, err := palette.Load(cs.fs); err == nil {
		pal = p
	} else {
		fmt.Fprintf(os.Stderr, "nanolathe: palette: %v (using fallback)\n", err)
	}
	// Load a debug font for the overlay [03 §7.1] C8. Try a few names.
	var fnt *formats.FNT
	for _, name := range []string{"fonts/smlfont.fnt", "fonts/armfont.fnt", "fonts/hatt12.fnt"} {
		if data, err := cs.fs.ReadFileLimit(name, 1<<20); err == nil {
			if parsed, err := formats.LoadFNT(data); err == nil {
				fnt = parsed
				break
			}
		}
	}
	// Camera: negotiated window 640x480 per Gate 1, map size in pixels.
	const winW, winH = 640, 480
	mapW := int32(terrain.CellW * 16) // map pixels [03 §2.1]
	mapH := int32(terrain.CellH * 16)
	cam := &camera.Camera{
		X:     0,
		Z:     0,
		ViewW: int32(winW),
		ViewH: int32(winH),
		MapW:  mapW,
		MapH:  mapH,
	}
	// Center camera if map smaller than view (negative-maximum domain [07 §10] C3).
	// Pan(0,0) triggers the clamp order.
	cam.Pan(0, 0)

	// Snapshot buffer for overlay tick/alpha. Gate 1 has no real sim, but we
	// still publish a stub tick so the overlay and interpolation are visibly correct.
	buf := &snapshot.Buffer{}
	var tick uint32
	// Seed for overlay RNG draws (not sim). Use time-derived seed for determinism of overlay.
	// The overlay just shows the numbers; no stream is advanced here.

	// Create client. The single Updater (C12) is registered inside Launch.
	// Step is called first inside that updater (C9) and owns clock/sub-ticks/snapshot
	// in later phases; for Gate 1 it just advances the stub tick and handles camera.
	cl := &client.Client{}
	_ = cl // placeholder to avoid unused; real creation below with options.

	opts2 := client.Options{
		Buffer:   buf,
		Width:    winW,
		Height:   winH,
		Title:    "Nanolathe — " + opts.Map,
		Headless: false,
		Step: func(delta float64) {
			// Advance stub tick at 30 Hz: delta is seconds since last Updater call.
			// For Gate 1 we don't have a real clock; just increment tick when
			// enough time has passed. Use a simple accumulator.
			// This is presentation-only; no sim state is written (I6).
			// For determinism of overlay, just bump tick every ~33ms.
			// We use time-based stepping here to make the overlay visibly count.
			// A more accurate clock would use the fixed 30 Hz budget [01 §4.2],
			// but Gate 1 has no settlement, so this is sufficient.
			// Increment tick roughly at 30 Hz for overlay.
			// delta is e.g. 0.016 at 60 fps; accumulate.
			// Use a package-level accumulator via closure.
			// (We keep it simple: just increment every call modulo 2 to approximate 30 Hz tick.)
			// Instead, publish every Step call with tick++ and let alpha interpolate.
			// The viewer's alpha ramp already proves interpolation.
			tick++
			f := &snapshot.Frame{Tick: tick}
			buf.Publish(f)

			// Camera pan: WASD + edge pan, capped per [07 §10] C2/C3.
			// We need rawTimeDelta in ms: delta*1000.
			rawDelta := int32(delta * 1000)
			if rawDelta < 0 {
				rawDelta = 0
			}
			if rawDelta == 0 {
				rawDelta = 16 // fallback for zero deltas
			}
			// Scroll setting byte: 8 is a comfortable default (8*16=128 cap).
			const scrollSetting = 8
			in := cl.Input()
			kbd := in.Kbd
			mouse := in.Mouse

			// WASD handling: each key moves one direction per frame, using Scroll
			// which caps at 128 [07 §10] C2. Zero delta skips movement.
			if kbd.KeyHeld(input.KeyW) || kbd.KeyHeld(input.KeyUp) {
				cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
			}
			if kbd.KeyHeld(input.KeyS) || kbd.KeyHeld(input.KeyDown) {
				cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
			}
			if kbd.KeyHeld(input.KeyA) || kbd.KeyHeld(input.KeyLeft) {
				cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
			}
			if kbd.KeyHeld(input.KeyD) || kbd.KeyHeld(input.KeyRight) {
				cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
			}
			// Edge pan: when mouse near viewport edge, pan similarly.
			const edge = 8
			if mouse.X < edge {
				cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
			} else if mouse.X > float32(winW-edge) {
				cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
			}
			if mouse.Y < edge {
				cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
			} else if mouse.Y > float32(winH-edge) {
				cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
			}
		},
	}
	// Create client with the step closure capturing cl.
	// We need to create the client first so the closure can capture it.
	// Re-create with the same opts but now that cl exists, the closure's host
	// lookup will succeed after Launch.
	client2, err := client.New(opts2)
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	cl = client2
	// Install world, palette, camera, font into the client for Frame to draw [PLAN_04A].
	cl.SetTerrain(terrain)
	cl.SetCamera(cam)
	if pal != nil {
		cl.SetPalette(pal)
	}
	if fnt != nil {
		cl.SetFNT(fnt)
	}
	// Also need to update the closure's captured cl variable to the new client.
	// The closure above captured the old cl (empty). Replace Step to capture the
	// new client2's host correctly. The closure's host lookup uses cl.Host() which
	// now points to the new client2 after assignment, so no need to recreate.
	// But the Step in opts2 still references the old cl variable (which now points
	// to client2 after assignment). Since Go closures capture variables by reference,
	// updating cl updates the closure's view. Good.

	// Diagnostics before blocking.
	fmt.Fprintf(os.Stderr, "nanolathe: viewer: opening window %dx%d for map %q (%dx%d cells)\n", winW, winH, opts.Map, terrain.CellW, terrain.CellH)
	if pal != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: viewer: palette loaded (%d base)\n", len(pal.Base))
	}
	if fnt != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: viewer: font loaded height %d\n", fnt.Height)
	}
	// Verify content database is reachable before blocking — if it is not,
	// bootstrap.Main will log via slog and return immediately with no window,
	// which looks like "no window" with only seed printed.
	if _, err := os.Stat("content"); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: viewer: warning: content directory not found at ./content: %v (try `make kaiju-content`)\n", err)
	}

	err = client.RunGame(cl)
	fmt.Fprintf(os.Stderr, "nanolathe: viewer: window closed\n")
	return err
}

// Ensure imports are used.
var _ = time.Now
var _ = input.KeyA
