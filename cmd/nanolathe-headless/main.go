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

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/survival"

	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
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
	// A mutated battle is not the retail baseline, so say so before it runs;
	// the report carries the canonical set as well
	// (docs/DESIGN_MODS_MUTATORS.md §6.6).
	if !request.Mutators.IsZero() {
		fmt.Fprintf(stderr, "nanolathe: mutators: %s\n", strings.Join(request.Mutators.Describe(), ", "))
	}
	// Restrictions likewise (docs/DESIGN_MODS_MUTATORS.md §15.5).
	if !request.Restrictions.IsZero() {
		fmt.Fprintf(stderr, "nanolathe: unit restrictions: %s\n", request.Restrictions.String())
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
	var modConfig string
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
	var mutatorArgs []string
	flags.Func("mutator", "global multiplier name=factor, repeatable, applied in every gameplay mode (docs/DESIGN_MODS_MUTATORS.md §6): "+mutatorUsage()+"; only these flags select mutators, never the settings file, so a run reproduces from its command line", func(text string) error {
		mutatorArgs = append(mutatorArgs, text)
		return nil
	})
	var restrictArgs []string
	flags.Func("restrict", "unit restriction <unit>=<count>, repeatable: 0 removes the unit from a skirmish or Survival battle and 1..100 caps each player's units of it, in every gameplay mode; none selects no restrictions (docs/DESIGN_MODS_MUTATORS.md §15); only these flags select restrictions, never the settings file, so a run reproduces from its command line", func(text string) error {
		restrictArgs = append(restrictArgs, text)
		return nil
	})
	modSelector := "none"
	flags.Func("mod", "installed mod to mount as the last content root, <id>, <id>@<version> or none (docs/DESIGN_MODS_MUTATORS.md §4.3); only this flag selects one, never the settings file, and nothing is fetched", func(text string) error {
		if _, _, err := modlibrary.ParseSelector(text); err != nil {
			return err
		}
		modSelector = text
		return nil
	})
	flags.StringVar(&modConfig, "mod-config", "", "path of a nanolathe-mod.json whose content layout, limits and rules apply to a manual --root stack, or replace an installed --mod's own; omitted uses the mod's own config, then the saved preference, then plain content (docs/DESIGN_MODS_MUTATORS.md §4.3)")
	flags.StringVar(&request.Map, "map", "", "map name without extension")
	var survivalPace string
	flags.BoolVar(&request.Survival.Enabled, "survival", false, "run a Survival battle on --map (docs/DESIGN_SURVIVAL.md)")
	flags.IntVar(&request.SurvivalBuddies, "survival-buddies", 0, "allied computer players in a Survival battle, 0..2")
	flags.StringVar(&survivalPace, "survival-pace", "normal", "Survival wave pace: normal, relaxed or relentless")
	flags.BoolVar(&request.Survival.NoAir, "survival-no-air", false, "Survival: no air waves")
	flags.BoolVar(&request.Survival.NoNaval, "survival-no-naval", false, "Survival: no naval waves")
	flags.Func("ai-player", "a computer player's AI by lobby row, <row>=<classic|modern> or all=<classic|modern> for every computer row (repeatable; a named row overrides all), in any gameplay mode: row 2 is the --map skirmish's computer player, rows 2 and 3 a Survival battle's buddies; omitted rows play Classic (docs/DESIGN_SESSIONS_AI_SAVE.md \"Modern AI computer player\")", func(text string) error {
		choice, err := session.ParseComputerAI(text)
		if err != nil {
			return fmt.Errorf("nanolathe: invalid computer AI: logical path <command line>, providers searched [ai-player], expected <row>=<classic|modern> or all=<classic|modern>: %w", err)
		}
		request.ComputerAI = append(request.ComputerAI, choice)
		return nil
	})
	flags.StringVar(&request.Mission, "mission", "", "campaign selector, e.g. camps/Arm Campaign.tdf:MISSION0")
	flags.IntVar(&request.Difficulty, "difficulty", 1, "battle difficulty as a setup stores it: 0 easy, 1 medium, 2 hard (skirmish and campaign); a mod whose Community table sets aiDifficultyReversed, as Escalation's does, plays it reversed")
	flags.Int64Var(&seed, "seed", -1, "seed for both deterministic streams; negative derives a pair from the clock")
	flags.Int64Var(&ticks, "ticks", 0, "authoritative tick limit (0 = 18000)")
	flags.StringVar(&reportPath, "report", "", "JSON report path (default stdout)")
	flags.StringVar(&profiles.cpuPath, "cpuprofile", "", "write a pprof CPU profile of the authoritative run to this file")
	flags.StringVar(&profiles.heapPath, "memprofile", "", "write a pprof allocation profile of the authoritative run to this file")
	flags.StringVar(&bench.OutputDir, "sim-benchmark", "", "run the simulation-cost benchmark and write its artifacts to this NEW directory")
	flags.BoolVar(&bench.Checkpoints, "sim-benchmark-checkpoints", false, "record admitted full checkpoints and selected tick rows in the simulation-cost benchmark")
	flags.StringVar(&bench.Map, "sim-benchmark-map", headless.SimBenchDefaultMap, "map for the simulation-cost benchmark scene")
	flags.IntVar(&bench.ArmySize, "sim-benchmark-army-size", headless.SimBenchDefaultArmySize, "placed units per computer army, excluding commander (250..1000; must fit the resolved unit limit)")
	flags.Int64Var(&warmup, "warmup-ticks", int64(headless.SimBenchDefaultWarmupTicks), "unmeasured ticks run before the benchmark window opens")
	flags.Int64Var(&measured, "benchmark-ticks", int64(headless.SimBenchDefaultMeasureTicks), "measured authoritative ticks in the benchmark window")
	flags.IntVar(&unitLimit, "unit-limit", 0, "per-player skirmish unit setting (20..3276); beats a gameplay feature table's limit (benchmark setting 400)")
	flags.IntVar(&bench.CensusCount, "census-samples", headless.SimBenchDefaultCensusCount, "census samples taken across the benchmark window")
	flags.BoolVar(&bench.PhaseTiming, "phase-timing", true, "attribute measured time to the twelve authoritative phases")
	flags.BoolVar(&bench.ThreadTiming, "thread-cpu-timing", false, "diagnostic per-tick CPU samples on a pinned OS thread (clock overhead can materially affect results)")
	flags.BoolVar(&bench.Profiles, "benchmark-profiles", true, "write cpu.pprof and the allocation profile pair for the measured window")
	if err := flags.Parse(args); err != nil {
		return request, reportPath, profiles, bench, err
	}
	if bench.ArmySize < headless.SimBenchMinArmySize || bench.ArmySize > headless.SimBenchMaxArmySize {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: invalid simulation benchmark army size: logical path <command line>, providers searched [sim-benchmark-army-size], expected %d..%d units excluding commander", headless.SimBenchMinArmySize, headless.SimBenchMaxArmySize)
	}
	if pace, err := survival.ParsePace(survivalPace); err != nil {
		return request, reportPath, profiles, bench, err
	} else {
		request.Survival.Pace = pace
	}
	if request.Survival.Enabled && (request.Map == "" || request.Mission != "") {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: survival needs a map: logical path <command line>, providers searched [survival], expected --map and no --mission")
	}
	if len(request.ComputerAI) != 0 && bench.OutputDir != "" {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: a computer AI choice is not applied to the simulation-cost benchmark: logical path <command line>, providers searched [ai-player], expected no --ai-player with --sim-benchmark")
	}
	if len(request.ComputerAI) != 0 && (request.Map == "" || request.Mission != "") {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: a computer AI choice needs a map: logical path <command line>, providers searched [ai-player], expected --map and no --mission")
	}
	if request.SurvivalBuddies < 0 || request.SurvivalBuddies > session.SurvivalMaxBuddies {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: invalid survival buddies: logical path <command line>, providers searched [survival-buddies], expected 0..%d", session.SurvivalMaxBuddies)
	}
	unitLimitSet, gameplaySet := false, false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "unit-limit":
			unitLimitSet = true
		case "gameplay":
			gameplaySet = true
		}
	})
	mod, err := resolveHeadlessMod(modSelector, request.Roots)
	if err != nil {
		return request, reportPath, profiles, bench, err
	}
	config, notice, err := headlessMountConfig(modConfig, mod)
	if err != nil {
		return request, reportPath, profiles, bench, err
	}
	if notice != "" {
		fmt.Fprintln(os.Stderr, "nanolathe: "+notice)
	}
	if mod != nil {
		// The simulation-cost benchmark measures a fixed retail scene.
		if bench.OutputDir != "" {
			return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: a mod is not mounted for the simulation-cost benchmark: logical path <command line>, providers searched [mod], expected no --mod with --sim-benchmark")
		}
		if gameplaySet {
			if err := mod.checkGameplay(request.Gameplay); err != nil {
				return request, reportPath, profiles, bench, err
			}
		}
		request.Roots = mod.roots()
		request.Root = request.Roots[0]
		request.Mod = mod.selector()
	}
	if unitLimitSet && (unitLimit < settings.MinUnitLimit || unitLimit > settings.MaxUnitLimit) {
		return request, reportPath, profiles, bench, fmt.Errorf("nanolathe: invalid unit limit: logical path <command line>, providers searched [unit-limit], expected %d..%d", settings.MinUnitLimit, settings.MaxUnitLimit)
	}
	storedFeatures, _ := settings.Load()
	request.GameplayFeatures = storedFeatures.GameplayFeatures
	bench.GameplayFeatures = storedFeatures.GameplayFeatures
	mutators, err := resolveMutators(mutatorArgs, bench.OutputDir != "")
	if err != nil {
		return request, reportPath, profiles, bench, err
	}
	request.Mutators = mutators
	restrictions, err := resolveRestrictions(restrictArgs, request.Mission != "", bench.OutputDir != "")
	if err != nil {
		return request, reportPath, profiles, bench, err
	}
	request.Restrictions = restrictions
	request.Content, request.ProfileFeatures = config.Content(), config.CommunitySources()
	bench.Content, bench.ProfileFeatures = config.Content(), config.CommunitySources()
	bench.UnitLimit = headless.SimBenchDefaultUnitLimit
	// A limit the player chose beats a feature table's, as on the desktop
	// (settings.UnitLimitSources). The benchmark's own default of 400 is a
	// workload setting, not a choice, so it adds no layer.
	if unitLimitSet {
		request.UnitLimit = unitLimit
		bench.UnitLimit = unitLimit
		request.GameplayFeatures, request.GameplayOverrides = settings.UnitLimitSources(request.GameplayFeatures, request.GameplayOverrides, unitLimit, 0)
		bench.GameplayFeatures, bench.GameplayOverrides = settings.UnitLimitSources(bench.GameplayFeatures, bench.GameplayOverrides, unitLimit, 0)
	} else if bench.OutputDir == "" {
		request.UnitLimit = storedFeatures.ConfiguredUnitLimit()
		request.GameplayFeatures, request.GameplayOverrides = settings.UnitLimitSources(request.GameplayFeatures, request.GameplayOverrides, 0, storedFeatures.UnitLimit)
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

// mutatorUsage names the mutator keys and their step lists from the content
// package's own descriptors, so the flag text cannot fall behind the set: the
// shared list, then each key with a list of its own (Builder speed, and the
// Smart makers switch's off and on).
func mutatorUsage() string {
	spell := func(info content.MutatorInfo) string {
		out := make([]string, len(info.Steps))
		for i, step := range info.Steps {
			out[i] = info.Spell(step)
		}
		return strings.Join(out, ", ")
	}
	shared := spell(content.MutatorInfo{Steps: content.MutatorSteps})
	var keys, own []string
	for _, info := range content.MutatorCatalog() {
		keys = append(keys, info.Key)
		if steps := spell(info); steps != shared {
			own = append(own, info.Key+" one of "+steps)
		}
	}
	usage := "name one of " + strings.Join(keys, ", ") + ", factor one of " + shared
	if len(own) > 0 {
		usage += " (" + strings.Join(own, "; ") + ")"
	}
	return usage
}

// resolveMutators reads the --mutator flags (docs/DESIGN_MODS_MUTATORS.md
// §6.6). The displayless command never reads the settings file's "mutators"
// key: fingerprint locks and benchmark runs must reproduce from their command
// line alone, whatever the player last chose in the window. The
// simulation-cost benchmark measures a fixed scene, so it refuses the flag.
func resolveMutators(args []string, benchmark bool) (content.Mutators, error) {
	expected := "name=factor, " + mutatorUsage()
	if benchmark {
		if len(args) > 0 {
			return content.Mutators{}, fmt.Errorf("nanolathe: mutators are not applied to the simulation-cost benchmark: logical path <command line>, providers searched [mutator], expected no --mutator with --sim-benchmark")
		}
		return content.Mutators{}, nil
	}
	if len(args) == 0 {
		return content.Mutators{}, nil
	}
	values := make(map[string]string, len(args))
	for _, arg := range args {
		name, factor, ok := strings.Cut(arg, "=")
		name = strings.TrimSpace(name)
		if _, repeated := values[name]; !ok || name == "" || repeated {
			return content.Mutators{}, fmt.Errorf("nanolathe: invalid mutator %q: logical path <command line>, providers searched [mutator], expected %s, each name once", arg, expected)
		}
		values[name] = factor
	}
	m, err := content.ParseMutators(values)
	if err != nil {
		return content.Mutators{}, fmt.Errorf("nanolathe: invalid mutator: logical path <command line>, providers searched [mutator], expected %s: %w", expected, err)
	}
	return m, nil
}

// resolveRestrictions reads the --restrict flags
// (docs/DESIGN_MODS_MUTATORS.md §15.5). Like the mutators, the displayless
// command never reads the settings file's "restrictions" key, so a run
// reproduces from its command line. A campaign mission keeps its own
// authored unit list and the simulation-cost benchmark measures a fixed
// scene, so both refuse the flag; names are checked against the running
// content at battle entry.
func resolveRestrictions(args []string, mission, benchmark bool) (content.Restrictions, error) {
	if len(args) == 0 {
		return content.Restrictions{}, nil
	}
	if mission {
		return content.Restrictions{}, fmt.Errorf("nanolathe: unit restrictions do not apply to a campaign mission: logical path <command line>, providers searched [restrict], expected no --restrict with --mission")
	}
	if benchmark {
		return content.Restrictions{}, fmt.Errorf("nanolathe: unit restrictions are not applied to the simulation-cost benchmark: logical path <command line>, providers searched [restrict], expected no --restrict with --sim-benchmark")
	}
	return headless.ParseRestrictFlags(args)
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
