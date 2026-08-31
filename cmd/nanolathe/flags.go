package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Options is the command-line surface for the retail runtime and its host
// configuration. Developer probes and capture modes are separate tools.
type Options struct {
	Root       string // retail install root
	Map        string // map name without extension, e.g. "ashap plateau"
	Seed       int64  // battle RNG seed for both streams; <0 = derive pair from clock
	Headless   bool   // run the session without opening a window
	Ticks      int    // authoritative tick limit; zero uses the headless default
	Mission    string // campaign path and mission selector, e.g. "camps/Arm Campaign.tdf:MISSION0"
	Difficulty int    // campaign difficulty
	LoadSave   string // explicit retail .SAV path to load in the windowed shell
	Report     string // JSON headless summary path; empty writes to stdout
	Shot       string // compose one frame to this PNG and exit, opening no window
	ShotTicks  int    // authoritative ticks to advance before the frame is captured
}

// ErrHelp reports that usage was requested and printed.
var ErrHelp = errors.New("help requested")

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

func parseFlags(args []string, out io.Writer) (Options, error) {
	var opts Options
	set := flag.NewFlagSet("nanolathe", flag.ContinueOnError)
	set.SetOutput(out)
	set.StringVar(&opts.Root, "root", defaultRoot(), "retail install root (or $NANOLATHE_TA_ROOT)")
	set.StringVar(&opts.Map, "map", "", "map name without extension, e.g. \"ashap plateau\"")
	set.Int64Var(&opts.Seed, "seed", -1, "battle RNG seed for both streams; negative derives a pair from the clock")
	set.BoolVar(&opts.Headless, "headless", false, "run a skirmish or mission without opening a window")
	set.IntVar(&opts.Ticks, "ticks", 0, "headless authoritative tick limit (0 = until result or 18000 ticks)")
	set.StringVar(&opts.Mission, "mission", "", "campaign selector, e.g. \"camps/Arm Campaign.tdf:MISSION0\"")
	set.IntVar(&opts.Difficulty, "difficulty", 1, "campaign difficulty")
	set.StringVar(&opts.LoadSave, "load-save", "", "load an existing retail .SAV in the windowed shell")
	set.StringVar(&opts.Report, "report", "", "write the headless JSON summary to this file (default stdout)")
	set.StringVar(&opts.Shot, "shot", "", "compose one battle frame to this PNG and exit, opening no window")
	set.IntVar(&opts.ShotTicks, "shot-ticks", 90, "authoritative ticks to advance before --shot captures the frame")
	set.Usage = func() {
		fmt.Fprintf(out, "nanolathe — a reimplementation of the Total Annihilation engine\n\n")
		fmt.Fprintf(out, "usage: nanolathe [flags]\n\nflags:\n")
		set.PrintDefaults()
	}
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, ErrHelp
		}
		return opts, err
	}
	return opts, nil
}
