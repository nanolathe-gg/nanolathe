// Command nanolathe runs the engine. Phase 0 boots headless: it resolves a
// retail install, reports what it found, and fails with a diagnostic naming
// the missing product and every provider searched.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/version"
)

// mainOptions parses command-line options without exiting so the Darwin entry
// point can decide whether it must hand the process main thread to AppKit.
func mainOptions(args []string, out io.Writer) (Options, int, bool) {
	opts, err := parseFlags(args, out)
	if err != nil {
		if errors.Is(err, ErrHelp) {
			return Options{}, 0, false
		}
		return Options{}, 2, false
	}
	return opts, 0, true
}

// runOptions executes a parsed command line and returns the process exit code.
func runOptions(opts Options, out, errOut *os.File) int {
	if err := run(opts, out); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

// wantsViewer is shared by run and the Darwin entry point. On macOS the latter
// must start CocoaRunApp on the process main thread before Kaiju creates a
// window; headless and dump commands must not enter the AppKit run loop.
func wantsViewer(opts Options) bool {
	return !opts.Headless && opts.Map != "" && opts.Dump == ""
}

// shouldRunShot reports whether run() will dispatch to runShot [P5].
func shouldRunShot(opts Options) bool {
	return opts.Shot != ""
}

// shouldRunHeadlessSession reports whether run() will dispatch to
// runSessionHeadless [P5][P6]. It mirrors the condition in run() after the
// shot and load checks: shot takes precedence over headless, and Mission is
// included alongside Map.
func shouldRunHeadlessSession(opts Options) bool {
	if opts.Shot != "" {
		return false
	}
	if opts.Headless && opts.Load != "" {
		return false
	}
	return opts.Headless && (opts.Map != "" || opts.Mission != "") && opts.Dump == ""
}

// dispatchKind is the testable form of run()'s branch ordering [P5][P6].
// It returns the name of the branch run() would take for opts, without
// performing I/O.
func dispatchKind(opts Options) string {
	if opts.Dump == "route" {
		return "route"
	}
	if opts.Headless && opts.Load != "" {
		return "load"
	}
	if opts.Shot != "" {
		return "shot"
	}
	if opts.Headless && (opts.Map != "" || opts.Mission != "") && opts.Dump == "" {
		return "headless"
	}
	if !opts.Headless && opts.Dump == "" {
		return "shell"
	}
	if wantsViewer(opts) {
		return "viewer"
	}
	return "report"
}

// seedsFor resolves the two stream seeds.
//
// Retail seeds them separately: the simulation stream at battle entry from
// QueryPerformanceCounter, the CRT stream at process start from the time
// source at effectively one-second resolution [01 §2.1], [01 §7.1], [01 §7.2].
// We mirror that split so an unseeded run does not accidentally couple them.
//
// --seed fixes both, which is the only handle a reproducible run has: RNG state
// is not saved and is reseeded on load [08 "Scheduler and random state in
// saves"]. PLAN_00 C4, PLAN_03 C10.
func seedsFor(opts Options) (sim, crt uint32) {
	if opts.Seed >= 0 {
		return uint32(opts.Seed), uint32(opts.Seed)
	}
	now := time.Now()
	return uint32(now.UnixNano()), uint32(now.Unix())
}

func run(opts Options, out *os.File) error {
	fmt.Fprintf(out, "%s\n", version.ProfileID())

	content, err := openContent(opts)
	if err != nil {
		return err
	}
	defer content.Close()

	// Seed both streams before any subsystem can draw (PLAN_03 C10). rng.Global
	// is nil until this runs, so a missed seeding is a crash, not a run that
	// looks deterministic and reproduces nothing (I4).
	simSeed, crtSeed := seedsFor(opts)
	rng.SeedGlobal(simSeed, crtSeed)
	fmt.Fprintf(out, "seed: sim=%d crt=%d\n", simSeed, crtSeed)

	// --dump route: headless route diagnostic [PLAN_07] task C14–C16
	if opts.Dump == "route" {
		if opts.Map == "" {
			return fmt.Errorf("nanolathe: --dump route requires --map")
		}
		return runGate2RouteDump(opts, content, out)
	}

	// Native save/load: headless continuation through internal/save's StateV1
	// box [PLAN_14 C18]. --load wins over a fresh skirmish.
	if opts.Headless && opts.Load != "" {
		return runLoadAndContinue(opts, content, out)
	}
	// Programmatic screenshot: compose frames headless and write a PNG.
	// Must precede headless-session branch: --headless --map shadows --shot
	// was unreachable [P5]. Shot path handles map/session itself via runShot.
	if opts.Shot != "" {
		return runShot(opts, content, out)
	}
	// Headless --map/--mission runs the REAL integrated session per Gate 5 [PLAN_14]:
	// session.NewSkirmish / NewMission → Step loop → deterministic summary. (--save rides it.)
	// Include Mission in dispatch: --headless --mission was silently falling through to report [P6].
	if opts.Headless && (opts.Map != "" || opts.Mission != "") && opts.Dump == "" {
		if err := runSessionHeadless(opts, content, out); err != nil {
			return err
		}
		return nil
	}
	// Windowed play goes through the game shell (menus → battle view);
	// --map skips menus and enters the battle directly [PLAN_14].
	if !opts.Headless && opts.Dump == "" {
		return runGameShell(opts, content)
	}
	// Gate 1: windowed terrain viewer when --map is set, --headless is false,
	// and no --dump is requested. This opens the Kaiju window and draws real
	// TNT terrain with camera pan and FNT overlay [PLAN_04A].
	if wantsViewer(opts) {
		if err := runViewer(opts, content); err != nil {
			// If viewer fails (e.g., no display), fall back to headless report
			// so CI and headless environments still produce useful output.
			fmt.Fprintf(os.Stderr, "nanolathe: viewer: %v (falling back to headless report)\n", err)
		} else {
			return nil
		}
	}

	return report(opts, content, out)
}
