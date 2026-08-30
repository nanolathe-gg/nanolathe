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
	"time"

	"github.com/nanolathe/nanolathe/internal/headless"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	request, reportPath, err := parse(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	report, runErr := headless.Run(request)
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

func parse(args []string, output io.Writer) (headless.Request, string, error) {
	var request headless.Request
	var reportPath string
	var seed int64
	var ticks int64
	flags := flag.NewFlagSet("nanolathe-headless", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&request.Root, "root", defaultRoot(), "retail install root (or $NANOLATHE_TA_ROOT)")
	flags.StringVar(&request.Map, "map", "", "map name without extension")
	flags.StringVar(&request.Mission, "mission", "", "campaign selector, e.g. camps/Arm Campaign.tdf:MISSION0")
	flags.IntVar(&request.Difficulty, "difficulty", 1, "campaign difficulty")
	flags.Int64Var(&seed, "seed", -1, "seed for both deterministic streams; negative derives a pair from the clock")
	flags.Int64Var(&ticks, "ticks", 0, "authoritative tick limit (0 = 18000)")
	flags.StringVar(&reportPath, "report", "", "JSON report path (default stdout)")
	if err := flags.Parse(args); err != nil {
		return request, reportPath, err
	}
	if ticks < 0 || uint64(ticks) > uint64(^uint32(0)) {
		return request, reportPath, fmt.Errorf("nanolathe: tick limit is outside the non-negative 32-bit battle boundary")
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
	return request, reportPath, nil
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
