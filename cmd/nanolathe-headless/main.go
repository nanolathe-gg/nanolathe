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
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/headless"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	request, reportPath, profiles, err := parse(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 1
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

func parse(args []string, output io.Writer) (headless.Request, string, profileOptions, error) {
	var request headless.Request
	var reportPath string
	var profiles profileOptions
	var seed int64
	var ticks int64
	flags := flag.NewFlagSet("nanolathe-headless", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&request.Root, "root", defaultRoot(), "retail install root (or $NANOLATHE_TA_ROOT)")
	flags.StringVar(&request.Map, "map", "", "map name without extension")
	flags.StringVar(&request.Mission, "mission", "", "campaign selector, e.g. camps/Arm Campaign.tdf:MISSION0")
	flags.IntVar(&request.Difficulty, "difficulty", 1, "battle difficulty: 0 easy, 1 medium, 2 hard (skirmish and campaign)")
	flags.Int64Var(&seed, "seed", -1, "seed for both deterministic streams; negative derives a pair from the clock")
	flags.Int64Var(&ticks, "ticks", 0, "authoritative tick limit (0 = 18000)")
	flags.StringVar(&reportPath, "report", "", "JSON report path (default stdout)")
	flags.StringVar(&profiles.cpuPath, "cpuprofile", "", "write a pprof CPU profile of the authoritative run to this file")
	flags.StringVar(&profiles.heapPath, "memprofile", "", "write a pprof allocation profile of the authoritative run to this file")
	if err := flags.Parse(args); err != nil {
		return request, reportPath, profiles, err
	}
	if ticks < 0 || uint64(ticks) > uint64(^uint32(0)) {
		return request, reportPath, profiles, fmt.Errorf("nanolathe: tick limit is outside the non-negative 32-bit battle boundary")
	}
	if request.Difficulty < 0 || request.Difficulty > 2 {
		return request, reportPath, profiles, fmt.Errorf("nanolathe: difficulty %d is outside the 0..2 battle vocabulary: logical path <command line>, providers searched [none], expected 0 easy, 1 medium, or 2 hard", request.Difficulty)
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
	return request, reportPath, profiles, nil
}

func defaultRoot() string {
	if root := os.Getenv("NANOLATHE_TA_ROOT"); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "TotalAnnihilation"
	}
	return filepath.Join(home, "TotalAnnihilation")
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
