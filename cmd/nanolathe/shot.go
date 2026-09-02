package main

import (
	"fmt"
	"image/png"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"

	"github.com/nanolathe/nanolathe/internal/client"
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

	var (
		b  *battleSession
		cl *client.Client
	)
	cl, err = client.New(client.Options{
		Buffer: sess.Snapshot,
		Width:  retailScreenW,
		Height: retailScreenH,
		Title:  "Nanolathe — " + opts.Map,
		Step:   func(delta float64) { b.viewerStep(delta, cl) },
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	cl.SetModelFS(cs.fs)
	b, err = composeBattleEntry(sess, authoritative.Session.Catalog, cs, cl, nil)
	if err != nil {
		return err
	}
	defer b.teardown(cl)

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
	for i := 0; i < opts.ShotTicks; i++ {
		millis.step = uint32(i) + 1
		b.viewerStep(tickSeconds, cl)
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

	// Zoom is presentation-only [F-P1-008]; it is applied after the ticks so
	// the simulation is identical to an unzoomed capture of the same seed.
	if opts.ShotZoom != 0 && opts.ShotZoom != 1 && b.cam != nil {
		fx, fy := int32(retailScreenW/2), int32(retailScreenH/2)
		if opts.ShotFocus != "" {
			if _, err := fmt.Sscanf(opts.ShotFocus, "%d,%d", &fx, &fy); err != nil {
				return fmt.Errorf("nanolathe: shot: --shot-focus wants \"x,y\", got %q", opts.ShotFocus)
			}
		}
		b.cam.SetScaleAbout(fx, fy, float32(opts.ShotZoom))
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
		started := time.Now()
		for i := 0; i < frames; i++ {
			millis.step = uint32(opts.ShotTicks) + uint32(i) + 2
			b.viewerStep(tickSeconds, cl)
			cl.Present()
		}
		elapsed := time.Since(started)
		fmt.Fprintf(os.Stderr, "nanolathe: %d frames in %s (%.2f ms/frame, %.1f fps)\n",
			frames, elapsed.Round(time.Millisecond),
			float64(elapsed.Microseconds())/float64(frames)/1000,
			float64(frames)/elapsed.Seconds())
	}

	img := cl.ComposeFrame()
	file, err := os.Create(opts.Shot)
	if err != nil {
		return fmt.Errorf("nanolathe: create shot %q: %w", opts.Shot, err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		return fmt.Errorf("nanolathe: write shot %q: %w", opts.Shot, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("nanolathe: close shot %q: %w", opts.Shot, err)
	}
	stopCPU()
	return writeMemProfile(opts.MemProfile)
}
