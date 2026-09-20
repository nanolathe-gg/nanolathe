package main

import (
	"bufio"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/film"
	"github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"
)

// The film capture route: `--film <script>` composes a scripted sequence of
// presented frames offline and writes them to a directory or to a pipe
// (docs/FILM_CAPTURE.md).
//
// It is `--shot` extended along time. The scene is deterministic, the camera
// follows the script's keys rather than the mouse, and each authoritative tick
// is presented several times at exact blend fractions, so a 60 or 120 FPS
// sequence comes out of a 30 Hz simulation with no wall-clock sampling
// anywhere (DESIGN_GPU_RENDERER §13.5). Nothing is paced: a frame that takes
// half a second to compose still lands on its own slot in the sequence, which
// is why a capture can afford the whole Enhanced pass at any resolution.
//
// The sequence is composed inside ONE Ebitengine loop, because a process may
// enter RunGame only once and AppKit requires the process main thread.

func runFilm(opts Options, cs *contentSet) error {
	data, err := os.ReadFile(opts.Film)
	if err != nil {
		return fmt.Errorf("nanolathe: film: read script: logical path %s, providers searched [filesystem], expected a film script", opts.Film)
	}
	script, err := film.Parse(data)
	if err != nil {
		return err
	}
	surfaceW, surfaceH := script.Surface()
	sink, err := newFilmSink(opts.FilmOut, script.Width, script.Height)
	if err != nil {
		return err
	}
	defer sink.close()

	frames := script.TotalFrames()
	if opts.FilmFrames > 0 && opts.FilmFrames < frames {
		frames = opts.FilmFrames
	}
	fmt.Fprintf(os.Stderr, "nanolathe: film: %d frames, %d ticks, %.1fs at %d FPS, %dx%d from a %dx%d surface, %d shots\n",
		frames, script.TotalTicks(), script.Duration(), script.FPS, script.Width, script.Height, surfaceW, surfaceH, len(script.Shots))

	game := &filmGame{
		script: script, sink: sink, frames: frames,
	}
	game.loadScene = func(scene film.Scene) error { return game.startScene(opts, cs, scene) }
	defer func() { game.cam.teardown(game.cl) }()
	ebiten.SetWindowVisible(false)
	ebiten.SetWindowSize(min(surfaceW, 1920), min(surfaceH, 1080))
	if err := ebiten.RunGame(game); err != nil {
		return fmt.Errorf("nanolathe: film: capture loop: %w", err)
	}
	if game.err != nil {
		return game.err
	}
	if err := sink.close(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "nanolathe: film: wrote %d frames to %s\n", game.drawn, opts.FilmOut)
	return nil
}

// startScene replaces all battle-bound state at a cut. Draw calls it only
// after the previous frame has finished recording and readback, so teardown
// joins the client workers before retiring the renderer's uploaded sources.
func (g *filmGame) startScene(opts Options, cs *contentSet, scene film.Scene) error {
	g.cam.teardown(g.cl)
	g.cam, g.cl, g.advance = nil, nil, nil
	if g.gpu != nil {
		g.gpu.ResetSources()
		g.gpu = nil
	}
	surfaceW, surfaceH := g.script.Surface()
	// The script owns the scene, so its map and seed outrank the command line.
	if scene.Map != "" {
		opts.Map = scene.Map
	}
	if scene.Seed != 0 {
		opts.Seed = int64(scene.Seed)
	}
	if opts.Seed < 0 {
		opts.Seed = 1 // Capture default; never derive a film seed from host time.
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
	anchorX, anchorZ, err := stageFilmScene(scene, sess)
	if err != nil {
		return err
	}
	if !scene.Fog {
		revealFilmScene(sess)
	}
	// Explosion shake reads as capture judder under a scripted camera move, so
	// a film turns it off through the `+noshake` developer switch's own bit.
	if !sess.NoShake() {
		sess.ToggleNoShake()
	}
	if scene.Opening {
		filmOpeningMex(sess)
		if !scene.Fog {
			// The reveal command lands on the first tick, which the arrival
			// holds: the intro would fade in one line-of-sight disc and the
			// rest of the map would pop in at handoff. Lift the fog before the
			// opening frame is published so the whole view arrives together.
			sess.RevealStagedMap()
		}
	}

	var (
		b  *battleSession
		cl *client.Client
	)
	cl, err = client.New(client.Options{
		Buffer: sess.Snapshot,
		Width:  surfaceW,
		Height: surfaceH,
		Step:   func(delta float64) { b.viewerStep(delta, cl) },
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	cl.SetModelFS(cs.unmappedMount)
	// A film states its own look rather than inheriting the operator's saved
	// display preferences: two captures of the same script must match.
	cl.SetAntiAlias(true)
	cl.SetFeatureShadows(true)
	cl.SetShadowOptions(true, true, true)
	cl.SetEffects(drawlist.Effects{Water: true, Lighting: true, Finish: true, Distortion: true, Marks: true})
	cl.SetGlow(true)
	cl.SetEnhanced(true)
	cl.SetInterpolation(true)
	// The detail view's 2x art is loaded when any shot rises above native, and
	// skipped otherwise: synthesizing it costs seconds for pixels a native
	// capture cannot show (DESIGN_GPU_RENDERER §14.1, §14.4).
	detailOpts := opts
	detailOpts.Zoom = camera.Zoom(int32(g.script.MaxZoom()*float64(camera.ZoomUnit) + 0.5))
	b, err = composeBattleEntryWithDetail(sess, sess.Catalog, cs, cl, nil, captureDetailArt(detailOpts, cs, sess.World))
	if err != nil {
		cl.Close()
		return err
	}
	if !g.script.Messages {
		// The message column draws inside the world viewport, so a clean
		// capture has to silence it at the ring rather than crop it away. One
		// authored line shows none [07 R-HUD-03 §14.3].
		cl.ConfigureMessageLines(1, 0)
	}
	b.cam.ViewW, b.cam.ViewH = int32(surfaceW), int32(surfaceH)
	b.cam.JumpToBattleViewCenter(anchorX, anchorZ)
	b.cam.Clamp()
	millis := &shotMillisSource{}
	b.millisSource = millis

	const tickSeconds = 1.0 / film.SimulationTPS
	step := uint32(0)
	advance := func() {
		step++
		millis.step = step
		// Through the client, not straight into the battle: the Enhanced
		// camera sample the blend needs is taken at the end of Client.Step
		// (DESIGN_GPU_RENDERER §13.5).
		cl.Step(tickSeconds)
		cl.ObserveCommittedTick()
	}
	for i := 0; i < scene.PreTicks; i++ {
		advance()
		// Drain each committed tick so the first captured frame does not
		// replay the whole lead-in's retained sound and status queue.
		cl.TickPresentationAudio()
	}

	g.opening = false
	if scene.Opening {
		// The arrival is a presentation clock the film drives itself, frame
		// by frame, from the shot's own time (GPU §36).
		if !b.beginArrival(cl) {
			cl.Close()
			return fmt.Errorf("nanolathe: film: the opening needs a fresh scene with a local commander and no pre_ticks")
		}
		g.opening = true
	}
	g.cl, g.cam, g.advance = cl, b, advance
	g.anchorX, g.anchorZ = anchorX, anchorZ
	return nil
}

// filmGame drives the capture: one authoritative step per group of presented
// frames, each frame composed at its own exact blend fraction.
type filmGame struct {
	script    *film.Script
	cl        *client.Client
	sink      filmSink
	frames    int
	anchorX   int32
	anchorZ   int32
	advance   func()
	loadScene func(film.Scene) error
	cam       *battleSession
	opening   bool // the arrival clock is still running

	gpu      *gpurender.Renderer
	overlay  *image.RGBA
	drawn    int
	err      error
	done     bool
	readback []byte
}

func (g *filmGame) Update() error {
	if g.done {
		return ebiten.Termination
	}
	return nil
}

func (g *filmGame) Draw(screen *ebiten.Image) {
	if g.done {
		return
	}
	cursor, ok := g.script.At(g.drawn)
	if !ok {
		g.done = true
		return
	}
	if !g.prepareScene(cursor) {
		return
	}
	w, h := g.script.Surface()
	if g.gpu == nil {
		gpu, err := gpurender.NewChecked(g.cl.PaletteTables(), w, h)
		if err != nil {
			g.fail(fmt.Errorf("nanolathe: film: modern renderer setup: %w", err))
			return
		}
		g.gpu, g.readback = gpu, make([]byte, 4*w*h)
		g.overlay = image.NewRGBA(image.Rect(0, 0, g.script.Width, g.script.Height))
	}
	shot := &g.script.Shots[cursor.Shot]
	if cursor.Phase == 0 {
		// The camera is installed for the tick this step is about to publish,
		// so the sample the step takes is the far end of the interval these
		// frames blend across (§13.5).
		g.applyCamera(shot, float64(cursor.ShotTick+1))
		g.advance()
		g.cl.BumpPresentationEpoch()
		if cursor.Cut {
			// A cut is not a camera move: the step above already sampled the
			// incoming shot's camera, so collapsing the blend onto that sample
			// keeps the outgoing framing from sliding into it.
			g.cl.SnapCameraBlend()
			g.census(shot.Name)
		}
	}
	if g.opening {
		// After the step, which may add its own cooling time, and before the
		// frame is recorded: the film's clock is the only one that counts.
		// The authored half-second of black before the reveal is for a window
		// settling; a film trims most of it.
		seconds := (float64(cursor.ShotTick)+cursor.Fraction)/film.SimulationTPS + 0.3
		g.cl.SetArrivalSeconds(float32(seconds))
		if float32(seconds) >= drawlist.ArrivalCoolingEndSeconds {
			g.opening = false
		}
	}
	g.cl.SetTickFraction(float32(cursor.Fraction))
	g.cl.SetCameraFraction(float32(cursor.Fraction))

	g.cl.BeginPresentationFrame()
	list := g.cl.RecordModernFrame()
	if list == nil {
		g.fail(fmt.Errorf("nanolathe: film: frame %d recorded nothing", g.drawn))
		return
	}
	g.gpu.SetDisplayPalette(g.cl.DisplayPalette())
	g.gpu.SetGlow(g.cl.Glow())
	g.gpu.SetEffects(g.cl.Effects())
	img := g.gpu.Execute(list, w, h)
	if img == nil {
		g.fail(fmt.Errorf("nanolathe: film: frame %d composed no surface", g.drawn))
		return
	}
	screen.DrawImage(img, &ebiten.DrawImageOptions{})
	img.ReadPixels(g.readback)

	g.crop()
	film.Letterbox(g.overlay, g.script.Letterbox)
	shotTime := float64(cursor.ShotTick) + cursor.Fraction
	for _, cue := range shot.Text {
		cue.Draw(g.overlay, shotTime)
	}
	if err := g.sink.write(g.overlay, g.drawn); err != nil {
		g.fail(err)
		return
	}
	g.drawn++
	if g.drawn >= g.frames {
		g.done = true
	}
}

// census reports, at each cut, where the fires are relative to the scene
// anchor. Framing a burning treeline is otherwise a guess per render.
func (g *filmGame) census(shot string) {
	cur := g.cl.Buffer().Current()
	if cur == nil {
		return
	}
	burning, sx, sz := 0, int64(0), int64(0)
	for _, f := range cur.Features {
		if f.IsBurning {
			burning++
			sx += int64(f.X.Int()) - int64(g.anchorX)
			sz += int64(f.Z.Int()) - int64(g.anchorZ)
		}
	}
	if burning > 0 {
		sx, sz = sx/int64(burning), sz/int64(burning)
	}
	fmt.Fprintf(os.Stderr, "nanolathe: film: shot %q units=%d features=%d burning=%d (mean offset %d,%d) projectiles=%d\n",
		shot, len(cur.Units), len(cur.Features), burning, sx, sz, len(cur.Projectiles))
}

// prepareScene is also the capture's error boundary: a failed incoming scene
// ends the stream before any frame from that shot is written.
func (g *filmGame) prepareScene(cursor film.Cursor) bool {
	if scene := g.script.SceneAtCut(cursor); scene != nil {
		if err := g.loadScene(*scene); err != nil {
			g.fail(fmt.Errorf("nanolathe: film: shot %q: load scene: %w", g.script.Shots[cursor.Shot].Name, err))
			return false
		}
	}
	return true
}

// applyCamera installs the shot's camera for one tick as an exact continuous
// view. The integer jump would snap every sample to a whole world pixel and
// floor the centre-to-origin conversion again as the factor changes, which is
// a visible lateral shimmer during a push (camera.SetPresentationView).
func (g *filmGame) applyCamera(shot *film.Shot, tick float64) {
	x, z, zoom, world := shot.CameraAt(tick)
	cam := g.cam.cam
	if !world {
		x += float64(g.anchorX)
		z += float64(g.anchorZ)
	}
	// The map's own floor is the camera's to apply: it keeps the request, which
	// is what turns a floor above the icon cutoff into the strategic view.
	zoom = min(max(zoom, camera.ZoomFloor.Float()), camera.ZoomMax.Float())
	// The origin is the world point under surface pixel zero; the battle
	// viewport starts OriginX columns in and is inset OriginY rows top and
	// bottom, so its centre sits (W+OriginX)/2 across and H/2 down [03 §4.1].
	cam.SetPresentationView(camera.PresentationView{
		X:      x - float64(cam.ViewW+camera.OriginX)/(2*zoom),
		Z:      z - float64(cam.ViewH)/(2*zoom),
		Factor: zoom,
	})
	g.cam.zoom.Reset()
}

// crop copies the written frame out of the composed surface. A clean capture
// composes the chrome and writes only the world viewport inside it, so the
// interface never reaches the file and the frame is exactly its scripted size.
func (g *filmGame) crop() {
	surfaceW, _ := g.script.Surface()
	x0, y0 := g.script.CropOrigin()
	stride := 4 * g.script.Width
	for y := 0; y < g.script.Height; y++ {
		src := 4 * ((y+y0)*surfaceW + x0)
		dst := y * stride
		copy(g.overlay.Pix[dst:dst+stride], g.readback[src:src+stride])
	}
}

func (g *filmGame) fail(err error) {
	g.err, g.done = err, true
}

func (g *filmGame) Layout(int, int) (int, int) { return g.script.Surface() }

// filmSink is where composed frames go: a directory of PNGs to inspect, or a
// raw RGBA stream for an encoder.
type filmSink interface {
	write(img *image.RGBA, index int) error
	close() error
}

func newFilmSink(out string, w, h int) (filmSink, error) {
	if out == "" {
		return nil, fmt.Errorf("nanolathe: film: --film-out wants a directory or \"-\" for a raw RGBA stream on stdout")
	}
	if out == "-" {
		// The frame stream owns the descriptor outright. Anything else that
		// prints to stdout — a load diagnostic, a dependency's warning — would
		// shift every later frame by its own length and quietly ruin a render
		// that has already cost minutes, so stdout is pointed at stderr for
		// the rest of the run and the encoder gets pixels only.
		stream := os.Stdout
		os.Stdout = os.Stderr
		return &filmRawSink{w: bufio.NewWriterSize(stream, 1<<20), frameBytes: 4 * w * h}, nil
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	return &filmPNGSink{dir: out}, nil
}

type filmPNGSink struct{ dir string }

func (s *filmPNGSink) write(img *image.RGBA, index int) error {
	f, err := os.Create(filepath.Join(s.dir, fmt.Sprintf("frame%06d.png", index)))
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func (s *filmPNGSink) close() error { return nil }

// filmRawSink streams packed RGBA, which is what the readback already is: an
// encoder reads it with no intermediate file and no PNG round trip.
type filmRawSink struct {
	w          *bufio.Writer
	frameBytes int
	closed     bool
}

func (s *filmRawSink) write(img *image.RGBA, _ int) error {
	if len(img.Pix) != s.frameBytes {
		return fmt.Errorf("nanolathe: film: frame is %d bytes, expected %d", len(img.Pix), s.frameBytes)
	}
	_, err := s.w.Write(img.Pix)
	return err
}

func (s *filmRawSink) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.w.Flush()
}
