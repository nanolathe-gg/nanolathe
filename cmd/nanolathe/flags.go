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
	Root        string // retail install root
	Map         string // map name without extension, e.g. "ashap plateau"
	Mission     string // campaign mission reference, e.g. "camps/arm campaign.tdf:MISSION0"
	AI          string // AI profile name; empty disables the planner
	Headless    bool   // no window
	Ticks       int    // headless tick budget; 0 = run until the session ends
	Seed        int64  // simulation RNG seed; <0 = derive from the clock
	Save        string // write a native save after a headless run [PLAN_14 C18]
	Load        string // restore a native save before ticking [PLAN_14 C18]
	UntilResult bool   // run until terminal result (victory/defeat/draw) [ON-07]
	MaxTick     int    // guard: maximum ticks when using --until-result [ON-07]
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
	set.StringVar(&opts.Mission, "mission", "", "campaign mission, e.g. \"camps/arm campaign.tdf:MISSION0\"")
	set.StringVar(&opts.AI, "ai", "", "AI profile name; empty disables the planner")
	set.BoolVar(&opts.Headless, "headless", false, "run without a window")
	set.IntVar(&opts.Ticks, "ticks", 0, "headless tick budget; 0 runs until the session ends")
	set.Int64Var(&opts.Seed, "seed", -1, "simulation RNG seed; negative derives one from the clock")
	set.StringVar(&opts.Save, "save", "", "write a native save here after the headless run")
	set.StringVar(&opts.Load, "load", "", "restore a native save before ticking")
	set.BoolVar(&opts.UntilResult, "until-result", false, "run until terminal result (victory/defeat/draw) [ON-07]")
	set.IntVar(&opts.MaxTick, "max-tick", 0, "guard: maximum ticks when using --until-result [ON-07]")
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
