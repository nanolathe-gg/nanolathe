// Command nanolathe runs the retail engine.
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

	// Native save/load: headless continuation through internal/save's StateV1
	// box [PLAN_14 C18]. --load wins over a fresh skirmish.
	if opts.Headless && opts.Load != "" {
		return runLoadAndContinue(opts, content, out)
	}
	// Headless --map/--mission runs the REAL integrated session per Gate 5 [PLAN_14]:
	// session.NewSkirmish / NewMission → Step loop → deterministic summary. (--save rides it.)
	if opts.Headless && (opts.Map != "" || opts.Mission != "") {
		if err := runSessionHeadless(opts, content, out); err != nil {
			return err
		}
		return nil
	}
	// Windowed play goes through the game shell (menus → battle view);
	// --map skips menus and enters the battle directly [PLAN_14].
	if !opts.Headless {
		return runGameShell(opts, content)
	}

	return fmt.Errorf("nanolathe: headless mode requires --map or --mission")
}
