package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// Options is the command-line surface for the retail runtime and its host
// configuration. Developer probes and capture modes are separate tools.
type Options struct {
	Root       string  // retail install root
	Map        string  // map name without extension, e.g. "ashap plateau"
	Seed       int64   // battle RNG seed for both streams; <0 = derive pair from clock
	Headless   bool    // run the session without opening a window
	Ticks      int     // authoritative tick limit; zero uses the headless default
	Mission    string  // campaign path and mission selector, e.g. "camps/Arm Campaign.tdf:MISSION0"
	Difficulty int     // campaign difficulty
	LoadSave   string  // explicit retail .SAV path to load in the windowed shell
	Report     string  // JSON headless summary path; empty writes to stdout
	Shot       string  // compose one frame to this PNG and exit, opening no window
	ShotTicks  int     // authoritative ticks to advance before the frame is captured
	Remaster   string  // remaster override: a loose directory or HPI mounted above every retail tier
	ShotZoom   float64 // presentation zoom applied before --shot captures (1 = native)
	ShotFocus  string  // "x,y" screen point kept fixed while zooming; default the screen centre
	ShotSelect bool    // run the Ctrl+A select-all before --shot captures, so the command page is open
	ShotSize   string  // "WxH" surface size for --shot; empty composes at the authored 640x480
	ShotModal  string  // battle modal to open before --shot captures: "options", "exit" or "confirm"
	ShotSpace  bool    // hold Space for --shot captures, so the bottom slide strip is fully raised
	Renderer   string  // start-up presentation executor: "classic" (default) or "modern"

	// ShotRenderer selects which executor --shot captures through:
	// "classic" (default, the software composer), "modern" (the GPU executor,
	// captured through a hidden one-frame Ebitengine loop), or "both" (classic
	// to --shot, modern to a sibling path, plus their diff)
	// [DESIGN_GPU_RENDERER.md §2.5]. ShotRendererMax is the largest differing-
	// pixel count "both" tolerates before the command exits non-zero; while the
	// modern executor is Clear+Expand only (WU-2.1) the whole play area differs,
	// so the default is effectively unbounded and the diff is an artifact for the
	// reviewer, not a build failure [DESIGN_GPU_RENDERER.md §6, C-G10].
	ShotRenderer    string // --shot executor: "classic", "modern" or "both"
	ShotRendererMax int    // with "both", exit non-zero when the diff exceeds this

	// Host-side profiling. None of these reach the session: a profiled run
	// draws the same numbers in the same order as an unprofiled one, so the
	// shipping path is what gets measured rather than a special build [I11].
	CPUProfile     string // pprof CPU profile of the --shot compose path
	MemProfile     string // pprof allocation profile of the --shot compose path
	ProfileSeconds int    // with --shot, drive the real viewer loop headlessly for this many seconds of battle time before capturing
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
	set.StringVar(&opts.Remaster, "remaster", "", "remastered-art override: a loose directory or .hpi mounted above every retail archive")
	set.Float64Var(&opts.ShotZoom, "shot-zoom", 1, "presentation zoom for --shot, 0.25..4 (1 = native)")
	set.StringVar(&opts.ShotFocus, "shot-focus", "", "screen point \"x,y\" kept fixed by --shot-zoom (default the screen centre)")
	set.BoolVar(&opts.ShotSelect, "shot-select", false, "select the viewing player's units before --shot captures, so the side rail's command page is open")
	set.StringVar(&opts.ShotSize, "shot-size", "", "surface size \"WxH\" for --shot, one of the display modes (default 640x480)")
	set.StringVar(&opts.ShotModal, "shot-modal", "", "open a battle modal before --shot captures: \"options\" (Tab), \"exit\" or \"confirm\"")
	set.BoolVar(&opts.ShotSpace, "shot-space", false, "hold Space for --shot captures, so the bottom slide strip (Game Time / Total Units / Game Speed) is fully raised")
	set.StringVar(&opts.CPUProfile, "cpuprofile", "", "write a pprof CPU profile of the --shot compose path to this file")
	set.StringVar(&opts.MemProfile, "memprofile", "", "write a pprof allocation profile of the --shot compose path to this file")
	set.IntVar(&opts.ProfileSeconds, "profile-seconds", 0, "with --shot, run the real viewer loop headlessly for this many seconds of battle time and report ms per frame")
	set.StringVar(&opts.Renderer, "renderer", "classic", "start-up presentation renderer: \"classic\" (software) or \"modern\" (GPU); any other value is classic")
	set.StringVar(&opts.ShotRenderer, "shot-renderer", "classic", "which executor --shot captures through: \"classic\" (software), \"modern\" (GPU, hidden one-frame loop) or \"both\" (classic + modern + diff)")
	set.IntVar(&opts.ShotRendererMax, "shot-renderer-max", math.MaxInt32, "with --shot-renderer both, exit non-zero when the diff exceeds this many pixels (default effectively unbounded)")
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
