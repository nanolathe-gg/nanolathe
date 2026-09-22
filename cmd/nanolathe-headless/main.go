// Command nanolathe-headless runs a bounded authoritative session without a
// graphical or audio-device dependency.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/settings"

	// The rule sets this build can select beyond the two reserved ones, so a
	// displayless run can reproduce a third-party set's session
	// (docs/DESIGN_GAMEPLAY_RULES.md §8).
	_ "github.com/nanolathe-gg/nanolathe/mods"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	request, reportPath, profiles, bench, err := parse(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	// The simulation-cost benchmark is a second mode of the same displayless
	// host: the same composition seam, the same Step loop, a fixture scene and
	// host-side measurement around it (docs/SIM_BENCHMARK.md).
	if bench.OutputDir != "" {
		bench.Root = request.Root
		bench.Roots = request.Roots
		bench.Difficulty = request.Difficulty
		bench.Seed = request.SimulationSeed
		bench.Log = stderr
		if _, err := headless.RunSimBenchmark(bench); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	// Profiling wraps the session but never enters it: the sampler is a host
	// concern and the authoritative run is bit-identical with or without it
	// [I6]. Elapsed wall time is reported alongside so a profile always comes
	// with the ticks-per-second it was measured at.
	stopCPU, err := profiles.startCPU()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	started := time.Now()
	report, runErr := headless.Run(request)
	elapsed := time.Since(started)
	stopCPU()
	if err := profiles.writeHeap(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if profiles.enabled() {
		fmt.Fprintf(stderr, "nanolathe: %d ticks in %s (%.1f ticks/s)\n",
			report.Tick, elapsed.Round(time.Millisecond), float64(report.Tick)/elapsed.Seconds())
	}
	if report.ScenarioIdentity != "" {
		if err := writeReport(reportPath, stdout, report); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if runErr == nil {
		return 0
	}
	if errors.Is(runErr, headless.ErrTickLimit) {
		return 2
	}
	fmt.Fprintln(stderr, runErr)
	return 1
}

// profileOptions names the two host-side sampler outputs. Neither reaches the
// session: a profiled run draws the same numbers in the same order as an
// unprofiled one, so a profile can be taken of the shipping path rather than of
// a special build [I11].
type profileOptions struct {
	cpuPath  string
	heapPath string
}

func (p profileOptions) enabled() bool { return p.cpuPath != "" || p.heapPath != "" }

// startCPU begins CPU sampling and returns the stop function. The stop is safe
// to call when no profile was requested.
func (p profileOptions) startCPU() (func(), error) {
	if p.cpuPath == "" {
		return func() {}, nil
	}
	file, err := os.Create(p.cpuPath)
	if err != nil {
		return func() {}, fmt.Errorf("nanolathe: create CPU profile %q: %w", p.cpuPath, err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		file.Close()
		return func() {}, fmt.Errorf("nanolathe: start CPU profile %q: %w", p.cpuPath, err)
	}
	return func() {
		pprof.StopCPUProfile()
		file.Close()
	}, nil
}

// writeHeap writes the allocation profile after the session has finished, so
// the cumulative allocation counters cover the whole run.
func (p profileOptions) writeHeap() error {
	if p.heapPath == "" {
		return nil
	}
	file, err := os.Create(p.heapPath)
	if err != nil {
		return fmt.Errorf("nanolathe: create memory profile %q: %w", p.heapPath, err)
	}
	runtime.GC()
	if err := pprof.Lookup("allocs").WriteTo(file, 0); err != nil {
		file.Close()
		return fmt.Errorf("nanolathe: write memory profile %q: %w", p.heapPath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("nanolathe: close memory profile %q: %w", p.heapPath, err)
	}
	return nil
}

func parse(args []string, output io.Writer) (headless.Request, string, profileOptions, headless.SimBenchOptions, error) {
	var request headless.Request
	var reportPath string
	var profiles profileOptions
	var bench headless.SimBenchOptions
	var seed int64
	var ticks int64
	var unitLimit int
	var warmup, measured int64
	var contentProfile string
	flags := flag.NewFlagSet("nanolathe-headless", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Func("root", "content root; repeat in load order (later roots win); omitted uses $NANOLATHE_TA_ROOT or installation discovery", func(root string) error {
		if root == "" {
			return fmt.Errorf("content root must not be empty")
		}
		request.Roots = append(request.Roots, root)
		if len(request.Roots) == 1 {
			request.Root = root
		}
		return nil
	})
	flags.Func("gameplay-feature", "community feature override name=value (repeatable; Strict ignores overrides)", func(text string) error {
		v, err := community.ParseOverride(text)
		if err == nil {
			request.GameplayOverrides = append(request.GameplayOverrides, v)
			bench.GameplayOverrides = append(bench.GameplayOverrides, v)
		}
		return err
	})
	flags.Func("gameplay", "gameplay rule set: modern (default), community-3.9, strict-3.1, or a registered set's name (see mods/)", func(text string) error {
		mode, err := gameplay.Parse(text)
		request.Gameplay = mode
		bench.Gameplay = mode
		return err
	})
	flags.StringVar(&contentProfile, "content-profile", "", "content profile: "+strings.Join(contentprofiles.Names(), ", ")+", or the path of a profile JSON file; omitted detects it from the mounted content set (docs/DESIGN_CONTENT_VFS.md §5)")
	flags.StringVar(&request.Map, "map", "", "map name without extension")
	flags.StringVar(&request.Mission, "mission", "", "campaign selector, e.g. camps/Arm Campaign.tdf:MISSION0")
	flags.IntVar(&request.Difficulty, "difficulty", 1, "battle difficulty: 0 easy, 1 medium, 2 hard (skirmish and campaign)")
	flags.Int64Var(&seed, "seed", -1, "seed for both deterministic streams; negative derives a pair from the clock")
	flags.Int64Var(&ticks, "ticks", 0, "authoritative tick limit (0 = 18000)")
	flags.StringVar(&reportPath, "report", "", "JSON report path (default stdout)")
	flags.StringVar(&profiles.cpuPath, "cpuprofile", "", "write a pprof CPU profile of the authoritative run to this file")
	flags.StringVar(&profiles.heapPath, "memprofile", "", "write a pprof allocation profile of the authoritative run to this file")
	flags.StringVar(&bench.OutputDir, "sim-benchmark", "", "run the simulation-cost benchmark and write its artifacts to this NEW directory")
	flags.StringVar(&bench.Map, "sim-benchmark-map", headless.SimBenchDefaultMap, "map for the simulation-cost benchmark scene")
	flags.Int64Var(&warmup, "warmup-ticks", int64(headless.SimBenchDefaultWarmupTicks), "unmeasured ticks run before the benchmark window opens")
	flags.Int64Var(&measured, "benchmark-ticks", int64(headless.SimBenchDefaultMeasureTicks), "measured authoritative ticks in the benchmark window")
	flags.IntVar(&unitLimit, "unit-limit", 0, "per-player skirmish unit setting (20..3276); gameplay feature table may override it (benchmark setting 400)")
	flags.IntVar(&bench.CensusCount, "census-samples", headless.SimBenchDefaultCensusCount, "census samples taken across the benchmark window")
	flags.BoolVar(&bench.PhaseTiming, "phase-timing", true, "attribute measured time to the twelve authoritative phases")
	flags.BoolVar(&bench.Profiles, "benchmark-profiles", true, "write cpu.pprof and the allocation profile pair for the measured window")
	if err := flags.Parse(args); err != nil {
		return request, reportPath, profiles, bench, err
	}
	unitLimitSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "unit-limit" {
			unitLimitSet = true
		}
	})
	if unitLimitSet && (unitLimit < settings.MinUnitLimit || unitLimit > settings.MaxUnitLimit) {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: invalid unit limit: logical path <command line>, providers searched [unit-limit], expected %d..%d", settings.MinUnitLimit, settings.MaxUnitLimit)
	}
	// Precedence is explicit flag, stored preference, then detection — the
	// same order the unit limit follows. An unknown selector is rejected at
	// the mount boundary, where the mounted providers can be named.
	if contentProfile == "" {
		stored, _ := settings.Load()
		contentProfile = stored.ContentProfile
	}
	storedFeatures, _ := settings.Load()
	request.GameplayFeatures = storedFeatures.GameplayFeatures
	bench.GameplayFeatures = storedFeatures.GameplayFeatures
	request.ContentProfile = contentProfile
	bench.ContentProfile = contentProfile
	bench.UnitLimit = headless.SimBenchDefaultUnitLimit
	if unitLimitSet {
		request.UnitLimit = unitLimit
		bench.UnitLimit = unitLimit
	} else if bench.OutputDir == "" {
		stored, _ := settings.Load()
		request.UnitLimit = stored.UnitLimit
	}
	if ticks < 0 || uint64(ticks) > uint64(^uint32(0)) {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: tick limit is outside the non-negative 32-bit battle boundary")
	}
	if warmup < 0 || uint64(warmup) > uint64(^uint32(0)) || measured <= 0 || uint64(measured) > uint64(^uint32(0)) {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: benchmark window is outside the non-negative 32-bit battle boundary: logical path <command line>, providers searched [none], expected a warm-up of 0 or more ticks and a measured window of 1 or more")
	}
	bench.WarmupTicks = uint32(warmup)
	bench.MeasureTicks = uint32(measured)
	if request.Difficulty < 0 || request.Difficulty > 2 {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: difficulty %d is outside the 0..2 battle vocabulary: logical path <command line>, providers searched [none], expected 0 easy, 1 medium, or 2 hard", request.Difficulty)
	}
	if seed < 0 {
		now := time.Now()
		request.SimulationSeed = uint32(now.UnixNano())
		request.CRTSeed = uint32(now.Unix())
	} else {
		request.SimulationSeed = uint32(seed)
		request.CRTSeed = uint32(seed)
	}
	request.TickLimit = uint32(ticks)
	return request, reportPath, profiles, bench, nil
}

func writeReport(path string, stdout io.Writer, report headless.Report) error {
	w := stdout
	var file *os.File
	if path != "" {
		var err error
		file, err = os.Create(path)
		if err != nil {
			return fmt.Errorf("nanolathe: create headless report %q: %w", path, err)
		}
		w = file
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(report)
	if file != nil {
		closeErr := file.Close()
		if writeErr == nil && closeErr != nil {
			return fmt.Errorf("nanolathe: close headless report %q: %w", path, closeErr)
		}
	}
	if writeErr != nil {
		return fmt.Errorf("nanolathe: write headless report %q: %w", path, writeErr)
	}
	return nil
}
