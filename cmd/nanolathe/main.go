// Command nanolathe runs the retail engine.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nanolathe/nanolathe/internal/version"
)

// BattleSeeds is the explicit pair selected at a battle boundary. The
// composition layer owns selection; the session receives the pair before it
// performs any setup work [01 §7.1][01 §7.2][R-CORE-02].
type BattleSeeds struct {
	Simulation int32
	CRT        uint32
}

// BattleSeedSource selects one fresh pair for each battle entry. Front-end
// presentation has no access to either session stream [01 §7.3].
type BattleSeedSource interface {
	NextBattleSeeds() BattleSeeds
}

type optionBattleSeedSource struct{ opts Options }

func (s optionBattleSeedSource) NextBattleSeeds() BattleSeeds {
	sim, crt := seedsFor(s.opts)
	return BattleSeeds{Simulation: int32(sim), CRT: crt}
}

func newBattleSeedSource(opts Options) BattleSeedSource {
	return optionBattleSeedSource{opts: opts}
}

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
// Retail seeds them separately at battle entry: the simulation stream from
// QueryPerformanceCounter and the CRT stream from the time source at
// effectively one-second resolution [01 §7.1], [01 §7.2].
// We mirror that split so an unseeded run does not accidentally couple them.
//
// --seed fixes both, which is the only host-level handle a reproducible run
// has. [01 §7.1], [01 §7.2]
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

	// All runtime entry points compose the retail game shell. The shell opens
	// the authored menus, or enters the battle directly when --map is supplied.
	return runGameShell(opts, content)
}
