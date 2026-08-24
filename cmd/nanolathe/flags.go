package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Options is the full command-line surface. Later phases add fields; they do
// not rename existing ones, because the gate commands in docs/PLAN_*.md are
// written against these names.
type Options struct {
	Root     string // retail install root
	Map      string // map name without extension, e.g. "ashap plateau"
	Mission  string // campaign mission reference, e.g. "camps/arm campaign.tdf:MISSION0"
	AI       string // AI profile name; empty disables the planner
	Headless bool   // no window
	Ticks    int    // headless tick budget; 0 = run until the session ends
	Seed     int64  // simulation RNG seed; <0 = derive from the clock
	Dump     string // headless diagnostic dump verb
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
	set.StringVar(&opts.Dump, "dump", "", "headless diagnostic dump (manifest, catalog, providers)")
	set.Usage = func() {
		fmt.Fprintf(out, "nanolathe — a reimplementation of the Total Annihilation engine\n\n")
		fmt.Fprintf(out, "usage: nanolathe [flags]\n\nflags:\n")
		set.PrintDefaults()
		fmt.Fprintf(out, "\ndiagnostic dumps:\n")
		fmt.Fprintf(out, "  -dump manifest    every logical path, its winning provider and hash\n")
		fmt.Fprintf(out, "  -dump providers   the mounted provider list in precedence order\n")
		fmt.Fprintf(out, "  -dump rng         headless tick loop; both stream states and draw counts\n")
	}
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, ErrHelp
		}
		return opts, err
	}
	return opts, nil
}
