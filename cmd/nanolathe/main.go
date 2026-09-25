// Command nanolathe runs the retail engine.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/install"
	"github.com/nanolathe-gg/nanolathe/internal/platform/benchlock"
	"github.com/nanolathe-gg/nanolathe/internal/version"
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
		if errors.Is(err, errHeadlessTickLimit) {
			return 2
		}
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
	// Installer diagnostics are host policy (DESIGN_CONTENT_VFS §5). Resolve
	// and validate before the banner, benchmark lock, or game startup.
	if opts.ListInstalls || opts.CheckInstall {
		if (opts.ListInstalls && opts.CheckInstall) || opts.Map != "" || opts.Mission != "" || opts.LoadSave != "" || opts.Headless || opts.Shot != "" || opts.BattleBenchmark != "" {
			return &missingProductError{what: "incompatible installation diagnostic flags", logical: "<command line>", expected: "one of --list-installs or --check-install without a game, capture, or benchmark mode"}
		}
		if opts.ListInstalls {
			explicit := opts.Roots
			if len(explicit) == 0 && opts.Root != "" {
				explicit = []string{opts.Root}
			}
			roots, err := install.Resolve(explicit)
			if err != nil {
				return err
			}
			for _, root := range roots {
				if _, err := fmt.Fprintln(out, root); err != nil {
					return err
				}
			}
			return nil
		}
		content, err := openContent(opts)
		if err != nil {
			return err
		}
		return content.Close()
	}

	if opts.InstallMod != "" {
		return runInstallMod(opts, out)
	}

	if opts.BattleBenchmark != "" {
		path, err := benchlock.Path()
		if err != nil {
			return fmt.Errorf("nanolathe: locate benchmark lock: %w", err)
		}
		lock, err := benchlock.Acquire(path, func() {
			fmt.Fprintf(os.Stderr, "nanolathe: battle benchmark waiting for lock %s\n", path)
		})
		if err != nil {
			return fmt.Errorf("nanolathe: acquire benchmark lock %s: %w", path, err)
		}
		defer lock.Close()
	}

	if opts.LoadSave != "" && (opts.Map != "" || opts.Mission != "" || opts.Headless || opts.Shot != "") {
		return fmt.Errorf("nanolathe: --load-save cannot be combined with --map, --mission, --headless, or --shot")
	}
	// A file-less headless report owns stdout as one JSON document. Windowed
	// runs and headless runs with a separate report file retain the profile
	// banner on stdout.
	if (!opts.Headless || opts.Report != "") && opts.Shot == "" && opts.Film == "" {
		fmt.Fprintf(out, "%s\n", version.ProfileID())
	}

	content, err := openContent(opts)
	if err != nil {
		// Start-up never fails because of the saved mod choice
		// (docs/DESIGN_MODS_MUTATORS.md §4.3 "A missing mod at start").
		var saved *savedModError
		if !errors.As(err, &saved) {
			return err
		}
		if content, err = startWithoutSavedMod(opts, saved.mod, saved.err); err != nil {
			return err
		}
	}
	defer content.Close()
	// launch keeps the command line as given, for a start that must remount
	// without the saved mod after mounting it.
	launch := opts
	opts.Root, opts.Roots = content.root, content.roots
	// The mount boundary owns profile selection, so the resolved name — not
	// the selector the command line carried — is what the reports state
	// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
	opts.ContentProfile = content.profile
	// Mutators and the running mod's gameplay minimum
	// (docs/DESIGN_MODS_MUTATORS.md §4.3, §6). Captures, benchmarks and
	// displayless runs take neither the saved mod nor the saved mutators.
	if opts.Mutators, err = resolveStartupMutators(opts); err != nil {
		return err
	}
	// Only a command line that names both is refused: a saved mod never stops
	// the start, and the window raises the selection to its minimum visibly.
	if minimum, ok := modMinimumGameplay(content.mod); ok && opts.GameplaySet && !content.savedMod && gameplayBelow(opts.Gameplay, minimum) {
		return &missingProductError{what: "gameplay mode is below the mod's minimum", logical: "<command line>", providers: []string{"--gameplay", "--mod"}, expected: gameplayLabel(minimum) + " or Modern for " + content.mod.Name}
	}

	if opts.Film != "" {
		return runFilm(opts, content)
	}

	if opts.ShotDebris != "" {
		return runDebrisShot(opts, content)
	}

	if opts.Shot != "" {
		return runShot(opts, content)
	}

	if opts.Headless {
		return runHeadless(opts, content, out)
	}

	// All runtime entry points compose the retail game shell. The shell opens
	// the authored menus, or enters the battle directly when --map is supplied.
	return runGameShell(launch, opts, content)
}
