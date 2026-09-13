package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/framediff"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// startCPUProfile begins host-side CPU sampling and returns the stop function.
// The sampler observes the shipping compose path; it neither enters the session
// nor changes what the session draws [I6][I11].
func startCPUProfile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return func() {}, fmt.Errorf("nanolathe: create CPU profile %q: %w", path, err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		file.Close()
		return func() {}, fmt.Errorf("nanolathe: start CPU profile %q: %w", path, err)
	}
	// The stop is idempotent so the normal path can flush the profile before
	// the memory profile is taken while an early return still flushes it.
	var once sync.Once
	return func() {
		once.Do(func() {
			pprof.StopCPUProfile()
			file.Close()
		})
	}, nil
}

// writeMemProfile writes the cumulative allocation profile once the measured
// work is over, so per-frame allocation shows up as a total rather than as a
// live-heap sample.
func writeMemProfile(path string) error {
	if path == "" {
		return nil
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("nanolathe: create memory profile %q: %w", path, err)
	}
	runtime.GC()
	if err := pprof.Lookup("allocs").WriteTo(file, 0); err != nil {
		file.Close()
		return fmt.Errorf("nanolathe: write memory profile %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("nanolathe: close memory profile %q: %w", path, err)
	}
	return nil
}

// shotMillisSource is the capture path's host clock. The windowed loop samples
// a monotonic wall clock, so a capture that only calls the viewer step in a
// tight loop advances the simulation by however much real time the loop took —
// six ticks for nine hundred iterations — and `--shot-ticks` named a count it
// did not deliver. The capture drives this source instead, one 30 Hz tick per
// viewer step, so the flag means the authoritative ticks it says it means and
// two captures of the same seed compose the same frame [01 §4.1][I6].
type shotMillisSource struct{ step uint32 }

// Millis32 returns the smallest millisecond count whose ScaledNow is the
// current step, so each viewer step advances the scaled clock by exactly one.
func (s *shotMillisSource) Millis32() uint32 {
	if s == nil {
		return 0
	}
	return (s.step*1000 + 29) / 30
}

// runShot composes one frame of a battle without opening a window and writes it
// as a PNG. It is the diagnostic path `Client.ComposeFrame` was written for
// [03 §2.4][I6]: the same session composition, the same presentation entry, and
// the same one-pass committed-frame composer the windowed loop calls, with the
// Ebitengine loop left unentered.
//
// It exists so a visual change can be reviewed as a picture rather than as an
// assertion that it should look right. Nothing here is a second renderer: every
// pixel comes from the production composer, and the session is stepped through
// the ordinary viewer step so the frame captured is a genuinely committed one.
func runShot(opts Options, cs *contentSet) error {
	if err := validateShotOptions(opts); err != nil {
		return err
	}
	if opts.ShotModel != "" {
		return runModelShot(opts, cs)
	}
	if opts.Shot == "" {
		return fmt.Errorf("nanolathe: shot: no output path: logical path <command line>, providers searched [none], expected --shot <file.png>")
	}
	request, _, err := headlessFreshBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return err
	}
	sess := authoritative.Session

	// The capture surface defaults to the authored 640x480; `--shot-size`
	// composes at another display mode the way the load transition would have
	// resized the window [07 R-FE-01 §11], so the chrome layout at that mode
	// [07 R-HUD-05] can be reviewed as a picture.
	shotW, shotH := retailScreenW, retailScreenH
	if opts.ShotSize != "" {
		if _, err := fmt.Sscanf(opts.ShotSize, "%dx%d", &shotW, &shotH); err != nil || shotW < settings.MinDisplaymodeWidth || shotH < settings.MinDisplaymodeHeight {
			return fmt.Errorf("nanolathe: shot: --shot-size wants \"WxH\" of at least %dx%d, got %q", settings.MinDisplaymodeWidth, settings.MinDisplaymodeHeight, opts.ShotSize)
		}
	}
	var (
		b  *battleSession
		cl *client.Client
	)
	cl, err = client.New(client.Options{
		Buffer: sess.Snapshot,
		Width:  shotW,
		Height: shotH,
		Title:  "Nanolathe — " + opts.Map,
		Step:   func(delta float64) { b.viewerStep(delta, cl) },
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	cl.SetModelFS(cs.fs)
	// A capture reads the same stored display block the windowed shell
	// installs, so `Anti_Alias` and `Shading` come from the settings file
	// (NANOLATHE_SETTINGS selects it) rather than from the client's built-in
	// defaults. Without this a capture could never show a structure composed
	// at 1x or a model drawn unshaded [07 R-FE-01 §6].
	applyVisualOptions(cl, loadedSettings().Display)
	// The Enhanced effect switches come from the same file, so a modern capture
	// composes under the switches the window would use (§30). The executor is
	// given the same selection where it is handed the display palette.
	cl.SetEffects(presentationEffects(loadedSettings().Presentation))
	// The capture route runs the load-time remaster inline — it has no loader
	// goroutine and no bar to report against — so a 2x capture shows the same
	// synthesized art the window would (DESIGN_GPU_RENDERER §14.4 "When"). At
	// scale 1 the provider is never consulted, so a native capture is
	// unchanged by it.
	b, err = composeBattleEntryWithDetail(sess, authoritative.Session.Catalog, cs, cl, nil, captureDetailArt(opts, cs, sess.World))
	if err != nil {
		return err
	}
	defer b.teardown(cl)
	// The battle composes its camera at the authored size; square it with the
	// capture surface the way the windowed installation point does
	// [07 R-FE-01 §11].
	if b.cam != nil {
		b.cam.ViewW, b.cam.ViewH = int32(shotW), int32(shotH)
		b.cam.Clamp()
	}
	if opts.BattleBenchmark != "" {
		return runBattleBenchmark(opts, b, cl)
	}

	// Sampling starts after content load and battle composition so a profile
	// describes the steady-state loop rather than one-time setup. With
	// `--profile-seconds` it starts later still, at the measured loop itself,
	// so the warm-up ticks do not dilute the per-frame picture.
	var stopCPU = func() {}
	if opts.ProfileSeconds <= 0 {
		stopCPU, err = startCPUProfile(opts.CPUProfile)
		if err != nil {
			return err
		}
	}
	defer func() { stopCPU() }()

	// One tick of wall clock per authoritative tick at the 30 Hz rate [01 §4.1].
	// The viewer step owns the clock, the sub-tick budget and the publication
	// boundary, so driving it is what makes the captured frame a committed one.
	const tickSeconds = 1.0 / 30.0
	millis := &shotMillisSource{}
	b.millisSource = millis
	// The Enhanced trail layer lays its marks per committed tick, and a capture
	// presents only the last one, so the capture route observes each tick as
	// the window would (DESIGN_GPU_RENDERER §15). The executor choice is the
	// same one the capture below installs.
	cl.SetEnhanced(effectiveShotRenderer(opts) != "classic")
	for i := 0; i < opts.ShotTicks; i++ {
		millis.step = uint32(i) + 1
		b.viewerStep(tickSeconds, cl)
		cl.ObserveCommittedTick()
	}

	// A capture has no pointer and no click history, so it composes the battle
	// screen's empty-selection state: with the selected-unit count at zero the
	// command-window switch closes down to the root and opens nothing, so the
	// side rail shows only PANELSIDE's own near-black art [07 §6], and the
	// footer — whose three sources are the hovered gadget, the hovered world
	// unit and the hovered feature, and which never reads the selection —
	// draws nothing but its backdrop [07 R-HUD-03 §1]. That is retail, but it
	// makes a capture useless for reviewing the rail. `--shot-select` runs the
	// ordinary Ctrl+A select-all through the human-command queue before the
	// frame is captured [07 R-CAM-01 §2], so the command page composes; the
	// extra viewer step publishes the selection the composer then reads [I6].
	if opts.ShotSelect {
		b.commitSelection(b.ownSelectableHandles(nil), true)
		b.disarmPlacement()
		millis.step = uint32(opts.ShotTicks) + 1
		b.viewerStep(tickSeconds, cl)
	}

	// `--shot-modal` drives the same activation path the pointer drives, so a
	// capture can show the pause/exit modal stack the composer places at the
	// live surface size [07 "Tab options menu and manual exit"][07 R-HUD-05].
	// Without it no capture can review those windows: they open only from
	// input the capture path has none of.
	if opts.ShotModal != "" {
		var route []string
		switch opts.ShotModal {
		case "options":
		case "exit":
			route = []string{"EXIT"}
		case "confirm":
			route = []string{"EXIT", "MAINMENU"}
		default:
			return fmt.Errorf("nanolathe: shot: --shot-modal wants \"options\", \"exit\" or \"confirm\", got %q", opts.ShotModal)
		}
		b.openBattleMenu()
		for _, button := range route {
			b.activateBattleMenuButton(button, cl)
		}
		millis.step = uint32(opts.ShotTicks) + 2
		b.viewerStep(tickSeconds, cl)
	}

	// `--shot-space` composes the frame Space has been held through: the
	// bottom slide strip's offset is put at its fully raised detent, the
	// state a held Space converges to after eighteen host frames of the
	// 15 ms-throttled ease [07 §6][07 R-HUD-04 §4]. The capture path has no
	// held keys, so the detent is written rather than stepped to; the strip
	// is presentation state and the simulation is unchanged [I6].
	if opts.ShotSpace {
		if state := b.battleState(); state != nil {
			state.PanelOffset, state.PanelTarget = ui.PanelParked, ui.PanelParked
		}
	}

	// The view scale is presentation-only [F-P1-008]; it is applied after the
	// ticks so the simulation is identical to a native capture of the same seed
	// (DESIGN_GPU_RENDERER §14.1).
	if opts.Zoom != 0 && opts.Zoom != camera.ZoomUnit && b.cam != nil {
		fx, fy := int32(shotW/2), int32(shotH/2)
		if opts.ShotFocus != "" {
			if _, err := fmt.Sscanf(opts.ShotFocus, "%d,%d", &fx, &fy); err != nil {
				return fmt.Errorf("nanolathe: shot: --shot-focus wants \"x,y\", got %q", opts.ShotFocus)
			}
		}
		// A capture has no motion to smooth, so the factor is taken outright
		// rather than eased (§16.8).
		jumpBattleZoom(b, fx, fy, opts.Zoom, modernRenderer(opts))
	}

	// Capture-only held input and a prospective product let reviewers inspect
	// the same tactical guides and placement adapter as the window (§20).
	// No construction order is submitted and no extra tick is needed.
	b.tacticalRangesHeld = opts.ShotAlt
	if opts.ShotBuild != "" {
		def, ok := b.cat.Unit(opts.ShotBuild)
		if !ok || def == nil {
			return fmt.Errorf("nanolathe: shot build preview failed: logical path %s, providers searched [compiled catalog], expected unit definition", opts.ShotBuild)
		}
		mx, my := int32((shotW+128)/2), int32(shotH/2)
		if f, ok := b.currentSnapshot(); ok && b.cam != nil {
			for _, v := range f.Units {
				if v.Owner == f.ViewingPlayer && v.Flags&hud.SelectionFlag != 0 {
					mx = b.cam.Zoom.Project(int32(v.X>>16)-b.cam.X) + 48
					my = b.cam.Zoom.Project(int32(v.Z>>16)-int32(v.Y>>17)-b.cam.Z) + 32
					mx, my = max(140, min(int32(shotW)-16, mx)), max(40, min(int32(shotH)-48, my))
					break
				}
			}
		}
		b.battleState().Input.PointerX, b.battleState().Input.PointerY = mx, my
		b.armPlacement(def)
		b.updatePlacement(mx, my)
	}

	// `--profile-seconds` is the render-side measurement path. It drives the
	// two calls the Ebitengine adapter makes — the viewer step and Present —
	// at the 30 Hz rate with the window loop left unentered, so the per-frame
	// cost it reports is the windowed loop's own cost minus the GPU upload
	// [03 §2.4][I6]. It deliberately does not use ComposeFrame: that path
	// allocates a screen-sized image per call for the PNG encoder, which is a
	// capture cost the game does not pay and would dominate the measurement.
	if opts.ProfileSeconds > 0 {
		stopCPU, err = startCPUProfile(opts.CPUProfile)
		if err != nil {
			return err
		}
		frames := opts.ProfileSeconds * 30
		// The two halves are timed apart because the loop's total says nothing
		// about which one to optimise: the viewer step is the authoritative
		// sub-tick and Present is the whole presentation compose, and on this
		// scene they are not the same order of magnitude. Sampling profiles
		// mis-attribute the split badly on some hosts — the allocator's page
		// syscalls swallow the stack — so the split is measured directly here
		// rather than inferred. Reading the clock twice per frame is host-side
		// instrumentation outside the session; it neither enters the sub-tick
		// nor changes what is drawn [I6][I11].
		var stepTotal, presentTotal time.Duration
		started := time.Now()
		for i := 0; i < frames; i++ {
			millis.step = uint32(opts.ShotTicks) + uint32(i) + 2
			mark := time.Now()
			b.viewerStep(tickSeconds, cl)
			mid := time.Now()
			cl.Present()
			done := time.Now()
			stepTotal += mid.Sub(mark)
			presentTotal += done.Sub(mid)
		}
		elapsed := time.Since(started)
		per := func(d time.Duration) float64 {
			return float64(d.Microseconds()) / float64(frames) / 1000
		}
		fmt.Fprintf(os.Stderr, "nanolathe: %d frames in %s (%.2f ms/frame, %.1f fps; step %.2f ms, present %.2f ms)\n",
			frames, elapsed.Round(time.Millisecond),
			per(elapsed), float64(frames)/elapsed.Seconds(),
			per(stepTotal), per(presentTotal))
	}

	shotRenderer := effectiveShotRenderer(opts)
	// The renderer switch is the one sanctioned presentation choice [I11]: both
	// executors replay the same committed frame, and the capture picks which one
	// writes the --shot PNG [DESIGN_GPU_RENDERER.md §2.5]. classic is the
	// reference (C-G11) and its output is unchanged; modern and both run the GPU
	// executor through a hidden one-frame Ebitengine loop.
	// The synthesized 2x art is an Enhanced feature: a modern capture records
	// it and a classic capture records the authored art; "both" records once
	// for the executor under test, the modern one (DESIGN_GPU_RENDERER §14.3).
	cl.SetEnhanced(shotRenderer != "classic")
	// One presented state for either executor, including the both capture.
	cl.BeginPresentationFrame()
	switch shotRenderer {
	case "classic":
		if err := encodeShotPNG(opts.Shot, cl.ComposeFrame()); err != nil {
			return err
		}
	case "modern":
		modern, err := captureModernShot(cl, shotW, shotH, shotSceneLabel(opts), opts.ShotGPUProfileFrames)
		if err != nil {
			return err
		}
		if err := encodeShotPNG(opts.Shot, modern); err != nil {
			return err
		}
	case "both":
		if err := runShotBoth(opts, cl, shotW, shotH); err != nil {
			return err
		}
	default:
		return fmt.Errorf("nanolathe: shot: --shot-renderer wants \"classic\", \"modern\" or \"both\", got %q", opts.ShotRenderer)
	}
	stopCPU()
	return writeMemProfile(opts.MemProfile)
}

// effectiveShotRenderer applies the one routing rule for battle and model
// captures. An omitted shot renderer follows the requested presentation
// renderer, but only the exact modern value selects the GPU path; every other
// renderer value retains the classic default [DESIGN_GPU_RENDERER.md §9].
func effectiveShotRenderer(opts Options) string {
	if opts.ShotRenderer != "" {
		return opts.ShotRenderer
	}
	if opts.Renderer == "modern" {
		return "modern"
	}
	return "classic"
}

func shotSceneLabel(opts Options) string {
	if opts.Map != "" {
		return opts.Map
	}
	return opts.Mission
}

func validateShotOptions(opts Options) error {
	shotRenderer := effectiveShotRenderer(opts)
	if opts.ShotModel != "" {
		if opts.Shot == "" {
			return fmt.Errorf("nanolathe: shot model: requires --shot <file.png>")
		}
		if opts.Renderer != "modern" {
			return fmt.Errorf("nanolathe: shot model: requires --renderer=modern, got %q", opts.Renderer)
		}
		if opts.ShotGPUProfileFrames > 0 {
			return fmt.Errorf("nanolathe: shot model: --shot-gpu-profile-frames is supported by battle captures only; omit --shot-model to profile a committed scene")
		}
		if opts.ShotModelHeading > 65535 {
			return fmt.Errorf("nanolathe: shot model: --shot-model-heading must be 0..65535, got %d", opts.ShotModelHeading)
		}
		// The model preview magnifies through the camera's view scale, which is
		// now an integer (DESIGN_GPU_RENDERER §14.1), so the fractional
		// magnifications this once accepted are gone.
		if opts.ShotModelScale != 0 && opts.ShotModelScale != 1 && opts.ShotModelScale != 2 {
			return fmt.Errorf("nanolathe: shot model: --shot-model-scale must be 1 or 2, got %.3g", opts.ShotModelScale)
		}
		if opts.ShotModelPose != "" && opts.ShotModelPose != "open" && opts.ShotModelPose != "activated" {
			return fmt.Errorf("nanolathe: shot model: --shot-model-pose wants \"open\" or \"activated\", got %q", opts.ShotModelPose)
		}
	}
	if opts.ShotGPUProfileFrames < 0 {
		return fmt.Errorf("nanolathe: shot: --shot-gpu-profile-frames must be nonnegative, got %d", opts.ShotGPUProfileFrames)
	}
	if opts.ShotGPUProfileFrames > 0 && opts.Renderer != "modern" {
		return fmt.Errorf("nanolathe: shot: --shot-gpu-profile-frames requires --renderer=modern, got %q", opts.Renderer)
	}
	if (shotRenderer == "modern" || shotRenderer == "both") && opts.Renderer != "modern" {
		return fmt.Errorf("nanolathe: shot: --shot-renderer=%s requires --renderer=modern, got %q", shotRenderer, opts.Renderer)
	}
	if opts.ShotGPUProfileFrames > 0 && shotRenderer != "modern" && shotRenderer != "both" {
		return fmt.Errorf("nanolathe: shot: --shot-gpu-profile-frames requires --shot-renderer modern or both, got %q", shotRenderer)
	}
	if opts.ProfileSeconds > 0 && opts.Renderer == "modern" {
		return fmt.Errorf("nanolathe: shot: --profile-seconds is CPU-only and cannot run with --renderer=modern; use --shot-gpu-profile-frames")
	}
	if opts.ShotRendererMax < 0 {
		return fmt.Errorf("nanolathe: shot: --shot-renderer-max must be nonnegative, got %d", opts.ShotRendererMax)
	}
	switch opts.ShotRenderer {
	case "", "classic", "modern", "both":
		return nil
	default:
		return fmt.Errorf("nanolathe: shot: --shot-renderer wants \"classic\", \"modern\" or \"both\", got %q", opts.ShotRenderer)
	}
}

// runShotBoth captures the classic and modern frames of the same committed
// state, writes both PNGs, and reports their difference [DESIGN_GPU_RENDERER.md
// §2.5, §6]. The classic capture keeps the ordinary --shot output byte for byte
// (C-G11); the modern capture goes to a sibling `.modern.png`, and the diff
// (differing-pixel count and the largest clusters) is printed to stderr with an
// optional `.diff.png`. It exits non-zero only when the diff exceeds
// --shot-renderer-max controls the explicit comparison acceptance threshold;
// the default remains effectively unbounded for historical capture callers.
func runShotBoth(opts Options, cl *client.Client, shotW, shotH int) error {
	// Each executor records its own native product from the same frozen
	// committed frame. ComposeFrame makes the classic index image, while
	// captureModernShot calls RecordModernFrame to retain the geometry-only model
	// packets the GPU consumes. Reusing the classic-inclusive list here would
	// omit models from the modern replay after cached/live composition.
	classic := cl.ComposeFrame()
	modern, err := captureModernShot(cl, shotW, shotH, shotSceneLabel(opts), opts.ShotGPUProfileFrames)
	if err != nil {
		return err
	}
	return compareShotImages(opts.Shot, classic, modern, opts.ShotRendererMax)
}

func compareShotImages(classicPath string, classic, modern image.Image, maxDiff int) error {
	if err := encodeShotPNG(classicPath, classic); err != nil {
		return err
	}
	modernPath := shotSiblingPath(classicPath, ".modern.png")
	if err := encodeShotPNG(modernPath, modern); err != nil {
		return err
	}
	res, err := framediff.Compare(classic, modern, 8)
	if err != nil {
		return fmt.Errorf("nanolathe: shot: compare classic and modern: %w", err)
	}
	fmt.Fprintf(os.Stderr, "nanolathe: shot-renderer both: %dx%d, %d differing pixels (%.4f%%); classic %s, modern %s\n",
		res.Width, res.Height, res.Count, 100*float64(res.Count)/float64(res.Total), classicPath, modernPath)
	const topClusters = 8
	for i, c := range res.Clusters {
		if i >= topClusters {
			fmt.Fprintf(os.Stderr, "  ... %d more clusters\n", len(res.Clusters)-i)
			break
		}
		fmt.Fprintf(os.Stderr, "  %6d px  at (%d,%d)-(%d,%d)\n", c.Count, c.X0, c.Y0, c.X1, c.Y1)
	}
	if res.Count > 0 {
		diffPath := shotSiblingPath(classicPath, ".diff.png")
		if err := encodeShotPNG(diffPath, framediff.DiffImage(classic, res.Differing, res.Width, res.Height)); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "  diff image %s\n", diffPath)
	}
	if res.Count > maxDiff {
		return fmt.Errorf("nanolathe: shot: modern differs from classic by %d pixels, over --shot-renderer-max %d", res.Count, maxDiff)
	}
	return nil
}

// encodeShotPNG writes img to path as a PNG, wrapping the file operations in the
// same diagnostic shape the classic capture used.
func encodeShotPNG(path string, img image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("nanolathe: create shot %q: %w", path, err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		return fmt.Errorf("nanolathe: write shot %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("nanolathe: close shot %q: %w", path, err)
	}
	return nil
}

// shotSiblingPath derives a companion capture path from the --shot path by
// replacing a trailing ".png" with suffix (e.g. ".modern.png"), or appending
// suffix when the path does not end in ".png".
func shotSiblingPath(shot, suffix string) string {
	if strings.HasSuffix(strings.ToLower(shot), ".png") {
		return shot[:len(shot)-len(".png")] + suffix
	}
	return shot + suffix
}
